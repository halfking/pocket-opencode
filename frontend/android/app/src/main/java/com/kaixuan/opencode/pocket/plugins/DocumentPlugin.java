package com.kaixuan.opencode.pocket.plugins;

import android.content.ContentResolver;
import android.content.ContentValues;
import android.graphics.Bitmap;
import android.graphics.Color;
import android.graphics.pdf.PdfRenderer;
import android.net.Uri;
import android.os.Build;
import android.os.Environment;
import android.os.ParcelFileDescriptor;
import android.provider.MediaStore;
import android.util.Base64;

import com.getcapacitor.JSObject;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.CapacitorPlugin;

import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.io.InputStream;
import java.io.OutputStream;

/**
 * Document — 应用内置的文档能力（不依赖任何第三方 PDF 库）。
 *
 * 背景（2026-10-01 真机复现）：Android WebView 内核不渲染 PDF，
 *   - 预览侧：<iframe src="blob:...pdf"> 在 WebView 126 上是全白（实测截图），
 *     只能改用系统自带的 android.graphics.pdf.PdfRenderer 逐页栅格化后交给前端 <img>。
 *   - 导出侧：原先走 @capacitor/share，会拉起系统「打开方式」选择框
 *     （真机上 MiuiChooserActivity，标题即 download.ts 里的「保存或分享文件」，
 *      候选只有 QQ 等），用户点任何入口都被系统弹窗打断。
 *     改为 MediaStore 静默落盘到系统「下载」目录，不再有任何系统弹窗。
 *
 * 线程：Capacitor 插件方法默认跑在插件线程池上，栅格化/文件 IO 不占 UI 线程。
 */
@CapacitorPlugin(name = "Document")
public class DocumentPlugin extends Plugin {

    /** 单页栅格化宽度上限，防止超大页面把内存打爆。 */
    private static final int MAX_RENDER_WIDTH = 2400;
    private static final int MIN_RENDER_WIDTH = 320;
    private static final long MAX_FILE_BYTES = 64L * 1024 * 1024;

    // ---------------------------------------------------------------- pdf

    /** 打开缓存目录里的文件；path 支持相对 Cache 的相对路径或绝对路径。 */
    private File resolveFile(String path) {
        if (path == null || path.trim().isEmpty()) return null;
        String p = path.trim();
        if (p.startsWith("file://")) p = p.substring("file://".length());
        File f = p.startsWith("/") ? new File(p) : new File(getContext().getCacheDir(), p);
        return f.exists() && f.isFile() ? f : null;
    }

    @PluginMethod
    public void pdfInfo(PluginCall call) {
        File file = resolveFile(call.getString("path"));
        if (file == null) {
            call.reject("pdf file not found in cache");
            return;
        }
        ParcelFileDescriptor pfd = null;
        try {
            pfd = ParcelFileDescriptor.open(file, ParcelFileDescriptor.MODE_READ_ONLY);
            PdfRenderer renderer = new PdfRenderer(pfd);
            try {
                JSObject ret = new JSObject();
                ret.put("pageCount", renderer.getPageCount());
                call.resolve(ret);
            } finally {
                renderer.close();
            }
        } catch (Throwable t) {
            call.reject("pdf open failed: " + describe(t));
        } finally {
            closeQuietly(pfd);
        }
    }

    /**
     * 栅格化单页并回传 PNG（base64 data URL）。page 为 0 基。
     * width 是目标像素宽度，前端按 devicePixelRatio 传。
     */
    @PluginMethod
    public void renderPdfPage(PluginCall call) {
        File file = resolveFile(call.getString("path"));
        if (file == null) {
            call.reject("pdf file not found in cache");
            return;
        }
        int page = call.getInt("page", 0);
        int width = call.getInt("width", 1080);

        ParcelFileDescriptor pfd = null;
        PdfRenderer renderer = null;
        Bitmap bitmap = null;
        try {
            pfd = ParcelFileDescriptor.open(file, ParcelFileDescriptor.MODE_READ_ONLY);
            renderer = new PdfRenderer(pfd);
            int pageCount = renderer.getPageCount();
            if (page < 0 || page >= pageCount) {
                call.reject("page out of range: " + page + "/" + pageCount);
                return;
            }
            int targetW = Math.max(MIN_RENDER_WIDTH, Math.min(MAX_RENDER_WIDTH, width));
            PdfRenderer.Page pdfPage = renderer.openPage(page);
            try {
                int srcW = Math.max(1, pdfPage.getWidth());
                int srcH = Math.max(1, pdfPage.getHeight());
                int targetH = Math.max(1, Math.round((float) targetW * srcH / srcW));

                bitmap = Bitmap.createBitmap(targetW, targetH, Bitmap.Config.ARGB_8888);
                // PdfRenderer 透明底，前端是深色 sheet，不铺白会出现黑底
                bitmap.eraseColor(Color.WHITE);
                pdfPage.render(bitmap, null, null, PdfRenderer.Page.RENDER_MODE_FOR_DISPLAY);

                ByteArrayOutputStream bos = new ByteArrayOutputStream();
                bitmap.compress(Bitmap.CompressFormat.PNG, 100, bos);
                String dataUrl = "data:image/png;base64,"
                        + Base64.encodeToString(bos.toByteArray(), Base64.NO_WRAP);

                JSObject ret = new JSObject();
                ret.put("page", page);
                ret.put("pageCount", pageCount);
                ret.put("width", targetW);
                ret.put("height", targetH);
                ret.put("image", dataUrl);
                call.resolve(ret);
            } finally {
                pdfPage.close();
            }
        } catch (Throwable t) {
            call.reject("pdf render failed: " + describe(t));
        } finally {
            if (bitmap != null) bitmap.recycle();
            if (renderer != null) {
                try { renderer.close(); } catch (Throwable ignored) { }
            }
            closeQuietly(pfd);
        }
    }

    // ------------------------------------------------------------- 导出落盘

    /**
     * 静默写入系统「下载」目录。API 29+ 走 MediaStore（无需任何运行时权限）；
     * 更低版本回落到 Environment 公共下载目录（需要 WRITE_EXTERNAL_STORAGE）。
     */
    @PluginMethod
    public void saveToDownloads(PluginCall call) {
        File file = resolveFile(call.getString("path"));
        if (file == null) {
            call.reject("file not found in cache");
            return;
        }
        String filename = call.getString("filename", file.getName());
        String mimeType = call.getString("mimeType", "application/octet-stream");
        filename = sanitize(filename);

        if (file.length() > MAX_FILE_BYTES) {
            call.reject("file too large to save: " + file.length() + " bytes");
            return;
        }

        try {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                saveViaMediaStore(file, filename, mimeType, call);
            } else {
                saveViaPublicDir(file, filename, call);
            }
        } catch (Throwable t) {
            call.reject("save failed: " + describe(t));
        }
    }

    private void saveViaMediaStore(File file, String filename, String mimeType, PluginCall call) throws Exception {
        ContentResolver resolver = getContext().getContentResolver();
        ContentValues cv = new ContentValues();
        cv.put(MediaStore.Downloads.DISPLAY_NAME, filename);
        cv.put(MediaStore.Downloads.MIME_TYPE, mimeType);
        // 写完再置 0，避免其他 App 读到半截文件
        cv.put(MediaStore.Downloads.IS_PENDING, 1);

        Uri uri = resolver.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, cv);
        if (uri == null) throw new IllegalStateException("MediaStore insert returned null");

        boolean ok = false;
        try (InputStream in = new FileInputStream(file); OutputStream out = resolver.openOutputStream(uri)) {
            if (out == null) throw new IllegalStateException("openOutputStream returned null");
            copy(in, out);
            ok = true;
        } finally {
            ContentValues done = new ContentValues();
            done.put(MediaStore.Downloads.IS_PENDING, ok ? 0 : 1);
            resolver.update(uri, done, null, null);
            if (!ok) resolver.delete(uri, null, null);
        }

        JSObject ret = new JSObject();
        ret.put("name", filename);
        ret.put("uri", uri.toString());
        ret.put("bytes", file.length());
        ret.put("location", "Download");
        call.resolve(ret);
    }

    private void saveViaPublicDir(File file, String filename, PluginCall call) throws Exception {
        File dir = Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DOWNLOADS);
        if (!dir.exists() && !dir.mkdirs()) throw new IllegalStateException("cannot create Downloads dir");
        File target = uniqueFile(dir, filename);
        try (InputStream in = new FileInputStream(file); OutputStream out = new FileOutputStream(target)) {
            copy(in, out);
        }
        JSObject ret = new JSObject();
        ret.put("name", target.getName());
        ret.put("uri", Uri.fromFile(target).toString());
        ret.put("bytes", target.length());
        ret.put("location", "Download");
        call.resolve(ret);
    }

    // ------------------------------------------------------------- helpers

    /** 同名文件不覆盖，按 "name (1).ext" 递增。 */
    private File uniqueFile(File dir, String filename) {
        File target = new File(dir, filename);
        if (!target.exists()) return target;
        int dot = filename.lastIndexOf('.');
        String stem = dot > 0 ? filename.substring(0, dot) : filename;
        String ext = dot > 0 ? filename.substring(dot) : "";
        for (int i = 1; i < 1000; i++) {
            File candidate = new File(dir, stem + " (" + i + ")" + ext);
            if (!candidate.exists()) return candidate;
        }
        return new File(dir, stem + "-" + System.currentTimeMillis() + ext);
    }

    /** 文件名不能带路径分隔符，否则会写到目录外。 */
    private static String sanitize(String name) {
        String n = name.replace('\\', '_').replace('/', '_').trim();
        if (n.isEmpty() || n.equals(".") || n.equals("..")) n = "download";
        return n;
    }

    private static void copy(InputStream in, OutputStream out) throws Exception {
        byte[] buf = new byte[16 * 1024];
        int n;
        while ((n = in.read(buf)) > 0) out.write(buf, 0, n);
        out.flush();
    }

    private static String describe(Throwable t) {
        String m = t.getMessage();
        return (m == null || m.isEmpty()) ? t.getClass().getSimpleName() : m;
    }

    private static void closeQuietly(ParcelFileDescriptor pfd) {
        if (pfd == null) return;
        try { pfd.close(); } catch (Throwable ignored) { }
    }
}
