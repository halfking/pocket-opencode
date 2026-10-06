package com.kaixuan.opencode.pocket.plugins;

import android.annotation.SuppressLint;
import android.content.pm.PackageManager;
import android.media.AudioFormat;
import android.media.AudioRecord;
import android.media.MediaRecorder;

import androidx.core.content.ContextCompat;

import com.getcapacitor.JSObject;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.CapacitorPlugin;

import com.k2fsa.sherpa.onnx.EndpointConfig;
import com.k2fsa.sherpa.onnx.EndpointRule;
import com.k2fsa.sherpa.onnx.FeatureConfig;
import com.k2fsa.sherpa.onnx.HomophoneReplacerConfig;
import com.k2fsa.sherpa.onnx.OfflineRecognizer;
import com.k2fsa.sherpa.onnx.OfflineRecognizerConfig;
import com.k2fsa.sherpa.onnx.OfflineModelConfig;
import com.k2fsa.sherpa.onnx.OfflineSenseVoiceModelConfig;
import com.k2fsa.sherpa.onnx.OfflineStream;
import com.k2fsa.sherpa.onnx.OnlineCtcFstDecoderConfig;
import com.k2fsa.sherpa.onnx.OnlineLMConfig;
import com.k2fsa.sherpa.onnx.OnlineModelConfig;
import com.k2fsa.sherpa.onnx.OnlineRecognizer;
import com.k2fsa.sherpa.onnx.OnlineRecognizerConfig;
import com.k2fsa.sherpa.onnx.OnlineRecognizerResult;
import com.k2fsa.sherpa.onnx.OnlineStream;
import com.k2fsa.sherpa.onnx.OnlineParaformerModelConfig;
import com.k2fsa.sherpa.onnx.OnlineZipformer2CtcModelConfig;
import com.k2fsa.sherpa.onnx.OnlineNeMoCtcModelConfig;
import com.k2fsa.sherpa.onnx.OnlineToneCtcModelConfig;
import com.k2fsa.sherpa.onnx.OnlineTransducerModelConfig;
import com.k2fsa.sherpa.onnx.QnnConfig;

import org.apache.commons.compress.archivers.tar.TarArchiveEntry;
import org.apache.commons.compress.archivers.tar.TarArchiveInputStream;
import org.apache.commons.compress.compressors.bzip2.BZip2CompressorInputStream;

import java.io.BufferedInputStream;
import java.io.File;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.util.Locale;
import java.util.concurrent.atomic.AtomicBoolean;

/**
 * cap-sherpa — sherpa-onnx 本地 ASR 插件（Phase 4 落地，替换 7 月骨架）。
 *
 * 双引擎（模型首次使用时从 GitHub Releases 下载到应用私有目录，不进 APK）：
 *   zipformer   流式转写（实时出字）。int8 encoder 组合 ≈50MB，中英双语，
 *               modified_beam_search + 端点检测（2.4s/1.2s/20s 三规则）。
 *   sensevoice  整段转写（本地高精档）。int8 ≈227MB，中英日韩粤，
 *               逆文本归一（自动标点）。与云端精翻构成三层金字塔
 *               （端=快，本地高精=隐私档，云=准，网关=路由/审计/归一）。
 *
 * 配方（模型文件名、modelType、端点规则、解码方法）已在桌面 JVM 侧用同一组
 * onnx 实测验证（RTF≈0.03，中文输出正确）；本插件按 Android AAR 的 Kotlin
 * 构造签名等价移植。精度同源实测（48s 中文音频）：SenseVoice ≈97% 字准带
 * 标点，zipformer ≈85% 无标点，小米云端逐字全对——实时通道的 interim 灰字
 * 由 zipformer 出，final 黑字交给 SenseVoice/云端覆盖。
 *
 * 方法签名与 frontend/src/native/sherpa.ts 对齐（status 为本轮新增）；
 * extractEmbedding 留 Phase 5，Web 端兜底路径不受影响。
 */
@CapacitorPlugin(name = "Sherpa")
public class SherpaPlugin extends Plugin {

  private static final String TAG = "SherpaPlugin";

  // ── 模型注册表 ──────────────────────────────────────────────────────
  private static final String MODEL_ZIPFORMER = "zipformer";
  private static final String MODEL_SENSEVOICE = "sensevoice";

  private static final String ZIPFORMER_DIR =
      "sherpa-onnx-streaming-zipformer-small-bilingual-zh-en-2023-02-16-mobile";
  private static final String ZIPFORMER_URL =
      "https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/"
          + ZIPFORMER_DIR + ".tar.bz2";
  private static final String ZIPFORMER_ENCODER = "encoder-epoch-99-avg-1.int8.onnx";
  private static final String ZIPFORMER_DECODER = "decoder-epoch-99-avg-1.onnx";
  private static final String ZIPFORMER_JOINER = "joiner-epoch-99-avg-1.int8.onnx";

  private static final String SENSEVOICE_DIR =
      "sherpa-onnx-sense-voice-zh-en-ja-ko-yue-int8-2024-07-17";
  private static final String SENSEVOICE_URL =
      "https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/"
          + SENSEVOICE_DIR + ".tar.bz2";
  private static final String SENSEVOICE_MODEL = "model.int8.onnx";

  private static final int SAMPLE_RATE = 16000;

  // ── 运行态 ──────────────────────────────────────────────────────────
  private OnlineRecognizer onlineRecognizer;
  private OfflineRecognizer offlineRecognizer;
  private final Object recognizerLock = new Object();

  private AudioRecord audioRecord;
  private Thread captureThread;
  private OnlineStream captureStream;
  private final AtomicBoolean listening = new AtomicBoolean(false);

  /** 实时会话中已确认的句段（端点 final）逐段累积。 */
  private final StringBuilder sessionFinals = new StringBuilder();
  private volatile float lastConfidence = 0.0f;

  // ── 插件方法 ────────────────────────────────────────────────────────

  /** 确保模型就位并初始化识别器。options: { model?: 'zipformer'|'sensevoice' } */
  @PluginMethod
  public void preload(PluginCall call) {
    final String model;
    try {
      model = modelName(call);
    } catch (IllegalArgumentException e) {
      call.reject(e.getMessage(), errCode("invalid_argument"));
      return;
    }
    runInBackground(() -> {
      try {
        ensureModel(model);
        initRecognizer(model);
        JSObject ret = new JSObject();
        ret.put("status", "ready");
        ret.put("model", model);
        ret.put("dir", modelDir(model).getAbsolutePath());
        call.resolve(ret);
      } catch (Exception e) {
        call.reject("preload failed: " + e.getMessage(), toJsError(e));
      }
    });
  }

  /** 转写本地 WAV 文件（16k 单声道 PCM16）。options: { audioPath } */
  @PluginMethod
  public void transcribe(PluginCall call) {
    String audioPath = call.getString("audioPath");
    if (audioPath == null || audioPath.isEmpty()) {
      call.reject("audioPath is required", errCode("invalid_argument"));
      return;
    }
    runInBackground(() -> {
      try {
        float[] samples = readWav16kMono(new File(audioPath));
        long t0 = System.currentTimeMillis();
        String engine;
        String text;
        float confidence;
        // 有 SenseVoice 模型走本地高精档；否则流式模型整段过（zipformer-only 用户）。
        if (modelPresent(MODEL_SENSEVOICE)) {
          ensureModel(MODEL_SENSEVOICE);
          initRecognizer(MODEL_SENSEVOICE);
          synchronized (recognizerLock) {
            OfflineStream stream = offlineRecognizer.createStream();
            stream.acceptWaveform(samples, SAMPLE_RATE);
            offlineRecognizer.decode(stream);
            text = offlineRecognizer.getResult(stream).getText();
            stream.release();
          }
          // SenseVoice 无逐 token 置信度输出：固定档位表示「本地高精通道可用」，
          // 语义是允许通过 stt.ts 的 minConfidence 闸，不是校准概率。
          confidence = 0.8f;
          engine = "sensevoice";
        } else {
          ensureModel(MODEL_ZIPFORMER);
          initRecognizer(MODEL_ZIPFORMER);
          text = recognizeWithOnline(samples);
          confidence = lastConfidence;
          engine = "zipformer";
        }
        float rtf = samples.length / (float) SAMPLE_RATE
            / Math.max(1f, (System.currentTimeMillis() - t0) / 1000.0f);
        JSObject ret = new JSObject();
        ret.put("text", text == null ? "" : text.trim());
        ret.put("confidence", confidence);
        ret.put("rtf", rtf);
        ret.put("engine", engine);
        call.resolve(ret);
      } catch (WavFormatException e) {
        call.reject(e.getMessage(), errCode("unsupported_format"));
      } catch (Exception e) {
        call.reject("transcribe failed: " + e.getMessage(), toJsError(e));
      }
    });
  }

  /** 声纹提取（ECAPA-TDNN）——Phase 5，接口占位与 sherpa.ts 对齐。 */
  @PluginMethod
  public void extractEmbedding(PluginCall call) {
    call.reject("extractEmbedding not implemented (Phase 5); web fallback path remains active",
        errCode("not_implemented"));
  }

  /** 开始端点驱动的流式识别（原生 AudioRecord 采集，partialResult 事件出字）。 */
  @PluginMethod
  public void startListening(PluginCall call) {
    if (listening.get()) {
      call.reject("already listening", errCode("busy"));
      return;
    }
    if (ContextCompat.checkSelfPermission(getContext(), android.Manifest.permission.RECORD_AUDIO)
        != PackageManager.PERMISSION_GRANTED) {
      call.reject("RECORD_AUDIO permission not granted", errCode("permission"));
      return;
    }
    runInBackground(() -> {
      try {
        ensureModel(MODEL_ZIPFORMER);
        initRecognizer(MODEL_ZIPFORMER);
        startCapture();
        call.resolve();
      } catch (Exception e) {
        call.reject("startListening failed: " + e.getMessage(), toJsError(e));
      }
    });
  }

  /** 停止采集并返回整段最终文本（已确认句段 + 残余尾段）。 */
  @PluginMethod
  public void stopListening(PluginCall call) {
    if (!listening.get()) {
      call.reject("not listening", errCode("idle"));
      return;
    }
    runInBackground(() -> {
      try {
        String text = stopCapture();
        JSObject finalObj = new JSObject();
        finalObj.put("text", text);
        finalObj.put("confidence", lastConfidence);
        finalObj.put("rtf", 0.0);
        finalObj.put("engine", "zipformer");
        JSObject ret = new JSObject();
        ret.put("final", finalObj);
        call.resolve(ret);
      } catch (Exception e) {
        call.reject("stopListening failed: " + e.getMessage(), toJsError(e));
      }
    });
  }

  /** 模型就位状态查询（JS 侧用于决定本地/云端路由）。 */
  @PluginMethod
  public void status(PluginCall call) {
    JSObject ret = new JSObject();
    ret.put("zipformerReady", modelPresent(MODEL_ZIPFORMER));
    ret.put("sensevoiceReady", modelPresent(MODEL_SENSEVOICE));
    ret.put("listening", listening.get());
    ret.put("modelsDir", modelsRoot().getAbsolutePath());
    call.resolve(ret);
  }

  // ── 识别核心 ────────────────────────────────────────────────────────

  private void initRecognizer(String model) {
    synchronized (recognizerLock) {
      if (MODEL_SENSEVOICE.equals(model)) {
        if (offlineRecognizer != null) return;
        File dir = modelDir(MODEL_SENSEVOICE);
        OfflineModelConfig mc = new OfflineModelConfig();
        mc.setSenseVoice(new OfflineSenseVoiceModelConfig(
            new File(dir, SENSEVOICE_MODEL).getAbsolutePath(),
            "",     // language 空 = 自动
            true,   // 逆文本归一（自动标点）
            new QnnConfig("", "", "")));
        mc.setTokens(new File(dir, "tokens.txt").getAbsolutePath());
        mc.setNumThreads(2);
        mc.setDebug(false);
        mc.setProvider("cpu");
        mc.setModelType("sense_voice");
        OfflineRecognizerConfig cfg = new OfflineRecognizerConfig();
        cfg.setFeatConfig(new FeatureConfig(SAMPLE_RATE, 80, 0.0f));
        cfg.setModelConfig(mc);
        offlineRecognizer = new OfflineRecognizer(null, cfg);
      } else {
        if (onlineRecognizer != null) return;
        File dir = modelDir(MODEL_ZIPFORMER);
        OnlineTransducerModelConfig transducer = new OnlineTransducerModelConfig(
            new File(dir, ZIPFORMER_ENCODER).getAbsolutePath(),
            new File(dir, ZIPFORMER_DECODER).getAbsolutePath(),
            new File(dir, ZIPFORMER_JOINER).getAbsolutePath(),
            new QnnConfig("", "", ""));
                // AAR v1.13.8 的 Kotlin 数据类对所有子配置与非 Qnn 字段做运行时非空
        // 校验（编译期查不出）：不用到的模型形态必须给空配置对象而非 null。
        OnlineModelConfig mc = new OnlineModelConfig(
            transducer,
            new OnlineParaformerModelConfig("", ""),
            new OnlineZipformer2CtcModelConfig(""),
            new OnlineNeMoCtcModelConfig(""),
            new OnlineToneCtcModelConfig(""),
            new File(dir, "tokens.txt").getAbsolutePath(),
            2, false, "cpu", "zipformer", "", "");
        EndpointConfig endpoint = new EndpointConfig(
            new EndpointRule(true, 2.4f, 0.0f),
            new EndpointRule(true, 1.2f, 0.0f),
            new EndpointRule(false, 0.0f, 20.0f));
        OnlineRecognizerConfig cfg = new OnlineRecognizerConfig(
            new FeatureConfig(SAMPLE_RATE, 80, 0.0f),
            mc,
            new OnlineLMConfig(),
            new OnlineCtcFstDecoderConfig(),
            new HomophoneReplacerConfig(),
            endpoint,
            true,
            "modified_beam_search",
            4,
            "", 0.0f, "", "", 0.0f);
        onlineRecognizer = new OnlineRecognizer(null, cfg);
      }
    }
  }

  /** 整段音频过流式模型（文件转写的 zipformer 通道）。 */
  private String recognizeWithOnline(float[] samples) {
    synchronized (recognizerLock) {
      OnlineStream stream = onlineRecognizer.createStream("");
      StringBuilder out = new StringBuilder();
      double probSum = 0;
      int probN = 0;
      int chunk = SAMPLE_RATE / 10; // 100ms
      for (int off = 0; off < samples.length; off += chunk) {
        int len = Math.min(chunk, samples.length - off);
        float[] part = new float[len];
        System.arraycopy(samples, off, part, 0, len);
        stream.acceptWaveform(part, SAMPLE_RATE);
        while (onlineRecognizer.isReady(stream)) onlineRecognizer.decode(stream);
        if (onlineRecognizer.isEndpoint(stream)) {
          OnlineRecognizerResult r = onlineRecognizer.getResult(stream);
          probSum += meanYsProb(r);
          probN++;
          out.append(r.getText());
          onlineRecognizer.reset(stream);
        }
      }
      stream.inputFinished();
      while (onlineRecognizer.isReady(stream)) onlineRecognizer.decode(stream);
      OnlineRecognizerResult r = onlineRecognizer.getResult(stream);
      probSum += meanYsProb(r);
      probN++;
      out.append(r.getText());
      stream.release();
      lastConfidence = probN > 0 ? clamp01((float) (probSum / probN)) : 0.0f;
      return out.toString().trim();
    }
  }

  private static float meanYsProb(OnlineRecognizerResult r) {
    float[] ys = r.getYsProbs();
    if (ys == null || ys.length == 0) return 0.0f;
    double sum = 0;
    for (float v : ys) sum += v;
    return (float) (sum / ys.length);
  }

  private static float clamp01(float v) {
    return Math.max(0.0f, Math.min(1.0f, v));
  }

  // ── 实时采集线程 ────────────────────────────────────────────────────

  @SuppressLint("MissingPermission")
  private void startCapture() throws Exception {
    int minBuf = AudioRecord.getMinBufferSize(SAMPLE_RATE, AudioFormat.CHANNEL_IN_MONO,
        AudioFormat.ENCODING_PCM_16BIT);
    audioRecord = new AudioRecord(MediaRecorder.AudioSource.VOICE_RECOGNITION,
        SAMPLE_RATE, AudioFormat.CHANNEL_IN_MONO, AudioFormat.ENCODING_PCM_16BIT,
        Math.max(minBuf, SAMPLE_RATE));
    if (audioRecord.getState() != AudioRecord.STATE_INITIALIZED) {
      int state = audioRecord.getState();
      audioRecord.release();
      audioRecord = null;
      throw new IllegalStateException("AudioRecord init failed, state=" + state);
    }
    sessionFinals.setLength(0);
    synchronized (recognizerLock) {
      captureStream = onlineRecognizer.createStream("");
    }
    listening.set(true);
    audioRecord.startRecording();

    final int chunkShorts = SAMPLE_RATE / 10; // 100ms
    captureThread = new Thread(() -> {
      short[] buf = new short[chunkShorts];
      long utteranceStartMs = 0;
      long lastPartialMs = 0;
      while (listening.get()) {
        int n = audioRecord.read(buf, 0, buf.length);
        if (n <= 0) continue;
        float[] samples = new float[n];
        for (int i = 0; i < n; i++) samples[i] = buf[i] / 32768.0f;
        String currentText;
        boolean endpoint;
        synchronized (recognizerLock) {
          if (captureStream == null || onlineRecognizer == null) break;
          captureStream.acceptWaveform(samples, SAMPLE_RATE);
          while (onlineRecognizer.isReady(captureStream)) onlineRecognizer.decode(captureStream);
          currentText = safeText(captureStream);
          endpoint = onlineRecognizer.isEndpoint(captureStream);
          if (endpoint) {
            if (!currentText.isEmpty()) {
              sessionFinals.append(currentText);
              emitPartial(currentText, true, utteranceStartMs, System.currentTimeMillis());
            }
            onlineRecognizer.reset(captureStream);
          }
        }
        long now = System.currentTimeMillis();
        if (!currentText.isEmpty() && utteranceStartMs == 0) utteranceStartMs = now;
        // 节流 200ms 的 interim 事件（灰字）；final 已在上面发过（黑字）。
        if (!endpoint && !currentText.isEmpty() && now - lastPartialMs >= 200) {
          lastPartialMs = now;
          emitPartial(currentText, false, utteranceStartMs, now);
        }
        if (endpoint) {
          utteranceStartMs = 0;
          lastPartialMs = 0;
        }
      }
    }, TAG + "-capture");
    captureThread.setPriority(Thread.NORM_PRIORITY + 1);
    captureThread.start();
  }

  /** 停止采集；返回整段文本（已确认句段 + inputFinished 后的残余尾段）。 */
  private String stopCapture() {
    listening.set(false);
    try {
      if (captureThread != null) captureThread.join(2000);
    } catch (InterruptedException ignored) {
      Thread.currentThread().interrupt();
    }
    captureThread = null;
    String tail = "";
    synchronized (recognizerLock) {
      if (captureStream != null) {
        captureStream.inputFinished();
        while (onlineRecognizer.isReady(captureStream)) onlineRecognizer.decode(captureStream);
        String t = safeText(captureStream);
        if (!t.isEmpty()) sessionFinals.append(t);
        captureStream.release();
        captureStream = null;
      }
      tail = sessionFinals.toString();
      sessionFinals.setLength(0);
    }
    if (audioRecord != null) {
      try {
        audioRecord.stop();
      } catch (IllegalStateException ignored) {
      }
      audioRecord.release();
      audioRecord = null;
    }
    return tail.trim();
  }

  private String safeText(OnlineStream stream) {
    OnlineRecognizerResult r = onlineRecognizer.getResult(stream);
    String t = r == null ? null : r.getText();
    return t == null ? "" : t;
  }

  private void emitPartial(String text, boolean isFinal, long startMs, long endMs) {
    JSObject data = new JSObject();
    data.put("text", text);
    data.put("isFinal", isFinal);
    data.put("startMs", startMs);
    data.put("endMs", endMs);
    notifyListeners("partialResult", data);
  }

  // ── 模型管理 ────────────────────────────────────────────────────────

  private static String modelName(PluginCall call) {
    String m = call.getString("model");
    if (m == null || m.isEmpty()) return MODEL_ZIPFORMER;
    if (m.equals(MODEL_ZIPFORMER) || m.equals(MODEL_SENSEVOICE)) return m;
    throw new IllegalArgumentException("unknown model: " + m + " (zipformer|sensevoice)");
  }

  private File modelsRoot() {
    return new File(getContext().getExternalFilesDir(null), "sherpa-models");
  }

  private File modelDir(String model) {
    String dir = MODEL_SENSEVOICE.equals(model) ? SENSEVOICE_DIR : ZIPFORMER_DIR;
    return new File(modelsRoot(), dir);
  }

  private boolean modelPresent(String model) {
    File dir = modelDir(model);
    if (MODEL_SENSEVOICE.equals(model)) {
      return new File(dir, SENSEVOICE_MODEL).isFile()
          && new File(dir, "tokens.txt").isFile();
    }
    return new File(dir, ZIPFORMER_ENCODER).isFile()
        && new File(dir, ZIPFORMER_DECODER).isFile()
        && new File(dir, ZIPFORMER_JOINER).isFile()
        && new File(dir, "tokens.txt").isFile();
  }

  /** 确保模型在位；缺则下载 tar.bz2 并解包（zip-slip 防护）。进度走 downloadProgress 事件。 */
  private void ensureModel(String model) throws IOException {
    if (modelPresent(model)) return;
    String url = MODEL_SENSEVOICE.equals(model) ? SENSEVOICE_URL : ZIPFORMER_URL;
    String topDir = MODEL_SENSEVOICE.equals(model) ? SENSEVOICE_DIR : ZIPFORMER_DIR;
    File root = modelsRoot();
    if (!root.exists() && !root.mkdirs()) throw new IOException("cannot mkdir " + root);
    File tarFile = new File(root, topDir + ".tar.bz2.part");
    download(url, tarFile, model);
    File finalTar = new File(root, topDir + ".tar.bz2");
    if (!tarFile.renameTo(finalTar)) throw new IOException("rename failed: " + finalTar);
    extractTarBz2(finalTar, root, topDir);
    if (!finalTar.delete()) finalTar.deleteOnExit();
    if (!modelPresent(model)) {
      throw new IOException("model extracted but files missing: " + modelDir(model));
    }
  }

  private void download(String urlStr, File target, String model) throws IOException {
    HttpURLConnection conn = (HttpURLConnection) new URL(urlStr).openConnection();
    conn.setInstanceFollowRedirects(true);
    conn.setConnectTimeout(15_000);
    conn.setReadTimeout(60_000);
    conn.setRequestProperty("User-Agent", "openpocket-sherpa/1.0");
    int code = conn.getResponseCode();
    if (code < 200 || code >= 300) {
      conn.disconnect();
      throw new IOException("model download HTTP " + code);
    }
    long total = conn.getContentLengthLong();
    try (InputStream in = new BufferedInputStream(conn.getInputStream());
         FileOutputStream out = new FileOutputStream(target)) {
      byte[] buf = new byte[1 << 16];
      long received = 0;
      long lastNotify = 0;
      int n;
      while ((n = in.read(buf)) > 0) {
        out.write(buf, 0, n);
        received += n;
        if (received - lastNotify >= (1 << 20)) { // 每 1MB 通知一次
          lastNotify = received;
          emitProgress(model, received, total);
        }
      }
      emitProgress(model, received, total);
    } finally {
      conn.disconnect();
    }
  }

  private void emitProgress(String model, long received, long total) {
    JSObject data = new JSObject();
    data.put("model", model);
    data.put("receivedBytes", received);
    data.put("totalBytes", total);
    notifyListeners("downloadProgress", data);
  }

  /** 解 tar.bz2：拒绝一切归一化后落在模型目录之外的条目（zip-slip 防护）。
   *  static 包级可见——instrumented test 直接驱动真实实现（含攻击样例）。 */
  static void extractTarBz2(File tar, File destRoot, String topDir) throws IOException {
    File allowedParent = destRoot.getCanonicalFile();
    try (TarArchiveInputStream tin = new TarArchiveInputStream(
        new BZip2CompressorInputStream(new BufferedInputStream(new FileInputStream(tar))))) {
      TarArchiveEntry entry;
      while ((entry = tin.getNextTarEntry()) != null) {
        if (!tin.canReadEntryData(entry)) continue;
        File out = new File(destRoot, entry.getName());
        if (!out.getCanonicalPath().startsWith(allowedParent.getPath() + File.separator)) {
          throw new IOException("tar entry escapes model root: " + entry.getName());
        }
        if (entry.isDirectory()) {
          out.mkdirs();
          continue;
        }
        File parent = out.getParentFile();
        if (parent != null && !parent.exists() && !parent.mkdirs()) {
          throw new IOException("cannot mkdir " + parent);
        }
        try (FileOutputStream fout = new FileOutputStream(out)) {
          byte[] buf = new byte[1 << 16];
          int n;
          while ((n = tin.read(buf)) > 0) fout.write(buf, 0, n);
        }
      }
    }
  }

  // ── WAV 解码（16k 单声道 PCM16 专用，错误信息可指导转码） ───────────

  private static final class WavFormatException extends IOException {
    WavFormatException(String msg) { super(msg); }
  }

  static float[] readWav16kMono(File f) throws IOException {
    byte[] all;
    try {
      all = Files.readAllBytes(f.toPath());
    } catch (IOException e) {
      throw new WavFormatException("cannot read audio file: " + e.getMessage());
    }
    if (all.length < 44 || !"RIFF".equals(new String(all, 0, 4, StandardCharsets.US_ASCII))) {
      throw new WavFormatException("not a WAV file (need 16k mono PCM16 wav; webm/mp3 请先转码)");
    }
    int channels = 1, sampleRate = 16000, bits = 16;
    boolean pcm = false;
    byte[] data = null;
    int pos = 12;
    while (pos + 8 <= all.length) {
      String id = new String(all, pos, 4, StandardCharsets.US_ASCII);
      int size = u32le(all, pos + 4);
      if (id.equals("fmt ")) {
        int format = u16le(all, pos + 8);
        channels = u16le(all, pos + 10);
        sampleRate = u32le(all, pos + 12);
        bits = u16le(all, pos + 22);
        pcm = format == 1;
      } else if (id.equals("data")) {
        int avail = Math.min(size, all.length - pos - 8);
        data = new byte[avail];
        System.arraycopy(all, pos + 8, data, 0, avail);
      }
      if (size < 0) break;
      pos += 8 + size + (size % 2);
    }
    if (!pcm) throw new WavFormatException("WAV is not PCM (compressed wav unsupported)");
    if (bits != 16) throw new WavFormatException("WAV bits=" + bits + ", need 16");
    if (channels != 1) throw new WavFormatException("WAV channels=" + channels + ", need mono");
    if (sampleRate != SAMPLE_RATE) {
      throw new WavFormatException(String.format(Locale.US,
          "WAV sampleRate=%d, need %d", sampleRate, SAMPLE_RATE));
    }
    if (data == null || data.length == 0) throw new WavFormatException("WAV has no audio data");
    int n = data.length / 2;
    float[] samples = new float[n];
    for (int i = 0; i < n; i++) {
      short v = (short) u16le(data, 2 * i);
      samples[i] = v / 32768.0f;
    }
    return samples;
  }

  private static int u16le(byte[] b, int off) {
    return (b[off] & 0xFF) | ((b[off + 1] & 0xFF) << 8);
  }

  private static int u32le(byte[] b, int off) {
    return (b[off] & 0xFF) | ((b[off + 1] & 0xFF) << 8)
        | ((b[off + 2] & 0xFF) << 16) | ((b[off + 3] & 0xFF) << 24);
  }

  // ── 杂项 ────────────────────────────────────────────────────────────

  private void runInBackground(Runnable r) {
    getBridge().execute(r);
  }

  private static JSObject errCode(String code) {
    JSObject o = new JSObject();
    o.put("code", code);
    return o;
  }

  private static JSObject toJsError(Exception e) {
    return errCode(e instanceof IOException ? "io" : "engine");
  }
}
