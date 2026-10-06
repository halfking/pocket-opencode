package com.kaixuan.opencode.pocket.plugins;

import android.content.Context;
import android.util.Log;

import androidx.test.ext.junit.runners.AndroidJUnit4;
import androidx.test.platform.app.InstrumentationRegistry;

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
import org.apache.commons.compress.archivers.tar.TarArchiveOutputStream;
import org.apache.commons.compress.compressors.bzip2.BZip2CompressorInputStream;
import org.apache.commons.compress.compressors.bzip2.BZip2CompressorOutputStream;
import org.junit.BeforeClass;
import org.junit.Test;
import org.junit.runner.RunWith;

import java.io.BufferedInputStream;
import java.io.BufferedOutputStream;
import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;

import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertNotNull;
import static org.junit.Assert.assertTrue;
import static org.junit.Assert.fail;

/**
 * sherpa-onnx 端上（模拟器 arm64）引擎验证——2026-10-07 端上 E2E 轮。
 *
 * 验证的是「桌面上证不了」的部分：
 *   1. libsherpa-onnx-jni.so 在设备 ABI 上真实加载并解码；
 *   2. 双引擎配方在端上产出中文（流式 zipformer / 离线 SenseVoice）；
 *   3. SherpaPlugin.readWav16kMono 的格式闸（非 WAV/错误采样率拒绝）；
 *   4. extractTarBz2 的 zip-slip 防护（真实攻击样例必须被拒）。
 *
 * 模型与测试音频不进 git：跑测前由 adb push 到 /data/local/tmp/sherpa_test/
 * （models/ 与 wavs/），@BeforeClass 复制进应用外置私有目录——这也是插件
 * 运行时模型就位的同一目录，顺带验证 modelPresent 的文件布局假设。
 */
@RunWith(AndroidJUnit4.class)
public class SherpaOnnxInstrumentedTest {

  private static final String TAG = "SherpaE2E";

  private static final String ZIPFORMER_DIR =
      "sherpa-onnx-streaming-zipformer-small-bilingual-zh-en-2023-02-16-mobile";
  private static final String SENSEVOICE_DIR =
      "sherpa-onnx-sense-voice-zh-en-ja-ko-yue-int8-2024-07-17";

  private static File modelsRoot;
  private static File wavsRoot;

  @BeforeClass
  public static void copyFixturesIntoPlace() throws IOException {
    Context ctx = InstrumentationRegistry.getInstrumentation().getTargetContext();
    File externalFiles = ctx.getExternalFilesDir(null);
    assertNotNull(externalFiles);
    modelsRoot = new File(externalFiles, "sherpa-models");
    File inputRoot = new File("/data/local/tmp/sherpa_test");
    assertTrue("缺测试夹具：先 adb push 模型与音频到 " + inputRoot, inputRoot.isDirectory());
    // 幂等：已就位则不重复拷（SenseVoice ≈227MB）。
    if (!new File(modelsRoot, ZIPFORMER_DIR + "/tokens.txt").isFile()) {
      copyDir(new File(inputRoot, "models/" + ZIPFORMER_DIR),
          new File(modelsRoot, ZIPFORMER_DIR));
    }
    if (!new File(modelsRoot, SENSEVOICE_DIR + "/tokens.txt").isFile()) {
      copyDir(new File(inputRoot, "models/" + SENSEVOICE_DIR),
          new File(modelsRoot, SENSEVOICE_DIR));
    }
    wavsRoot = new File(inputRoot, "wavs");
    assertTrue(new File(wavsRoot, "zipformer_zh.wav").isFile());
    assertTrue(new File(wavsRoot, "sensevoice_zh.wav").isFile());
    Log.i(TAG, "fixtures ready: " + modelsRoot);
  }

  /** 流式 zipformer：100ms 块喂入 + 端点检测，与插件实时路径同一形态。 */
  @Test
  public void streamingZipformerDecodesOnDevice() throws Exception {
    File dir = new File(modelsRoot, ZIPFORMER_DIR);
    OnlineTransducerModelConfig transducer = new OnlineTransducerModelConfig(
        new File(dir, "encoder-epoch-99-avg-1.int8.onnx").getAbsolutePath(),
        new File(dir, "decoder-epoch-99-avg-1.onnx").getAbsolutePath(),
        new File(dir, "joiner-epoch-99-avg-1.int8.onnx").getAbsolutePath(),
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
    OnlineRecognizerConfig cfg = new OnlineRecognizerConfig(
        new FeatureConfig(16000, 80, 0.0f),
        mc,
        new OnlineLMConfig(),
        new OnlineCtcFstDecoderConfig(),
        new HomophoneReplacerConfig(),
        new EndpointConfig(
            new EndpointRule(true, 2.4f, 0.0f),
            new EndpointRule(true, 1.2f, 0.0f),
            new EndpointRule(false, 0.0f, 20.0f)),
        true, "modified_beam_search", 4, "", 0.0f, "", "", 0.0f);

    float[] samples = SherpaPlugin.readWav16kMono(new File(wavsRoot, "zipformer_zh.wav"));
    long t0 = System.currentTimeMillis();
    OnlineRecognizer rec = new OnlineRecognizer(null, cfg);
    OnlineStream stream = rec.createStream("");
    StringBuilder out = new StringBuilder();
    int chunk = 1600;
    for (int off = 0; off < samples.length; off += chunk) {
      int len = Math.min(chunk, samples.length - off);
      float[] part = new float[len];
      System.arraycopy(samples, off, part, 0, len);
      stream.acceptWaveform(part, 16000);
      while (rec.isReady(stream)) rec.decode(stream);
      if (rec.isEndpoint(stream)) {
        out.append(textOf(rec, stream));
        rec.reset(stream);
      }
    }
    stream.inputFinished();
    while (rec.isReady(stream)) rec.decode(stream);
    out.append(textOf(rec, stream));
    stream.release();
    rec.release();
    double wall = (System.currentTimeMillis() - t0) / 1000.0;
    String text = out.toString().trim();
    Log.i(TAG, "[zipformer-on-device] text=" + text
        + " wall=" + String.format(java.util.Locale.US, "%.2fs", wall)
        + " rtf=" + String.format(java.util.Locale.US, "%.2f", wall / (samples.length / 16000.0)));
    assertTrue("流式解码应产出文本", text.length() >= 4);
    assertTrue("流式解码应含中文", containsHan(text));
  }

  /** 离线 SenseVoice：整段一次解码，即插件 transcribe 的本地高精档。 */
  @Test
  public void senseVoiceDecodesOnDevice() throws Exception {
    File dir = new File(modelsRoot, SENSEVOICE_DIR);
    OfflineModelConfig mc = new OfflineModelConfig();
    mc.setSenseVoice(new OfflineSenseVoiceModelConfig(
        new File(dir, "model.int8.onnx").getAbsolutePath(),
        "zh", true, new QnnConfig("", "", "")));
    mc.setTokens(new File(dir, "tokens.txt").getAbsolutePath());
    mc.setNumThreads(2);
    mc.setDebug(false);
    mc.setProvider("cpu");
    mc.setModelType("sense_voice");
    OfflineRecognizerConfig cfg = new OfflineRecognizerConfig();
    cfg.setFeatConfig(new FeatureConfig(16000, 80, 0.0f));
    cfg.setModelConfig(mc);

    float[] samples = SherpaPlugin.readWav16kMono(new File(wavsRoot, "sensevoice_zh.wav"));
    long t0 = System.currentTimeMillis();
    OfflineRecognizer rec = new OfflineRecognizer(null, cfg);
    OfflineStream stream = rec.createStream();
    stream.acceptWaveform(samples, 16000);
    rec.decode(stream);
    String text = rec.getResult(stream).getText();
    stream.release();
    rec.release();
    double wall = (System.currentTimeMillis() - t0) / 1000.0;
    Log.i(TAG, "[sensevoice-on-device] text=" + text
        + " wall=" + String.format(java.util.Locale.US, "%.2fs", wall));
    assertTrue("SenseVoice 解码应产出文本", text != null && text.trim().length() >= 3);
    assertTrue("SenseVoice 解码应含中文", containsHan(text));
  }

  /** WAV 格式闸：合法 16k 单声道通过；非 WAV、44.1k 采样率拒绝。 */
  @Test
  public void readWav16kMonoValidates() throws Exception {
    File good = new File(wavsRoot, "zipformer_zh.wav");
    float[] samples = SherpaPlugin.readWav16kMono(good);
    assertTrue("16k 单声道应可读", samples.length > 16000);

    File fake = new File(modelsRoot.getParentFile(), "fake.wav");
    Files.write(fake.toPath(), "this is not a wav file at all........".getBytes(StandardCharsets.US_ASCII));
    try {
      SherpaPlugin.readWav16kMono(fake);
      fail("非 WAV 应被拒绝");
    } catch (IOException expected) {
      assertTrue(expected.getMessage().contains("not a WAV"));
    }

    File bad44k = new File(modelsRoot.getParentFile(), "bad44k.wav");
    Files.write(bad44k.toPath(), minimalWav(44100, 16));
    try {
      SherpaPlugin.readWav16kMono(bad44k);
      fail("44100Hz 应被拒绝");
    } catch (IOException expected) {
      assertTrue(expected.getMessage().contains("44100"));
    }
    fake.delete();
    bad44k.delete();
  }

  /** zip-slip 防护：良性包正常解出；`../` 逃逸条目必须 IOException。 */
  @Test
  public void extractTarBz2GuardsZipSlip() throws Exception {
    File work = new File(modelsRoot.getParentFile(), "tar_test");
    work.mkdirs();

    // 良性：top/sub/hello.txt
    File benign = new File(work, "benign.tar.bz2");
    writeTar(benign, "top/sub/hello.txt", "hi".getBytes(StandardCharsets.UTF_8));
    File dest1 = new File(work, "dest1");
    dest1.mkdirs();
    SherpaPlugin.extractTarBz2(benign, dest1, "top");
    File hello = new File(dest1, "top/sub/hello.txt");
    assertTrue("良性包应解出文件", hello.isFile()
        && "hi".equals(new String(Files.readAllBytes(hello.toPath()), StandardCharsets.UTF_8)));

    // 攻击：../evil.txt 试图逃出模型根
    File evil = new File(work, "evil.tar.bz2");
    writeTar(evil, "../evil.txt", "pwned".getBytes(StandardCharsets.UTF_8));
    File dest2 = new File(work, "dest2");
    dest2.mkdirs();
    try {
      SherpaPlugin.extractTarBz2(evil, dest2, "top");
      fail("zip-slip 条目应被拒绝");
    } catch (IOException expected) {
      assertTrue(expected.getMessage().contains("escapes"));
    }
    assertFalse("逃逸文件不得落盘", new File(work, "evil.txt").exists());
  }

  // ── helpers ─────────────────────────────────────────────────────────

  private static String textOf(OnlineRecognizer rec, OnlineStream stream) {
    OnlineRecognizerResult r = rec.getResult(stream);
    return r == null || r.getText() == null ? "" : r.getText();
  }

  private static boolean containsHan(String s) {
    if (s == null) return false;
    for (int i = 0; i < s.length(); i++) {
      char c = s.charAt(i);
      if (c >= 0x4E00 && c <= 0x9FFF) return true;
    }
    return false;
  }

  private static void copyDir(File from, File to) throws IOException {
    File[] children = from.listFiles();
    assertNotNull("缺目录 " + from, children);
    if (!to.exists() && !to.mkdirs()) throw new IOException("mkdirs " + to);
    for (File c : children) {
      File t = new File(to, c.getName());
      if (c.isDirectory()) copyDir(c, t);
      else {
        try (InputStream in = new BufferedInputStream(new FileInputStream(c));
             OutputStream out = new BufferedOutputStream(new FileOutputStream(t))) {
          byte[] buf = new byte[1 << 16];
          int n;
          while ((n = in.read(buf)) > 0) out.write(buf, 0, n);
        }
      }
    }
  }

  private static void writeTar(File target, String entryName, byte[] content) throws IOException {
    try (TarArchiveOutputStream tar = new TarArchiveOutputStream(
        new BZip2CompressorOutputStream(new BufferedOutputStream(new FileOutputStream(target))))) {
      TarArchiveEntry entry = new TarArchiveEntry(entryName);
      entry.setSize(content.length);
      tar.putArchiveEntry(entry);
      tar.write(content);
      tar.closeArchiveEntry();
      tar.finish();
    }
  }

  /** 44 字节标准头 + 少量 PCM 数据的最小 WAV。
   *  布局：RIFF(0-3) size(4-7) WAVE(8-11) fmt (12-15) fmtSize(16-19)
   *  format(20-21) channels(22-23) sampleRate(24-27) byteRate(28-31)
   *  blockAlign(32-33) bits(34-35) data(36-39) dataSize(40-43)。 */
  private static byte[] minimalWav(int sampleRate, int bits) {
    ByteArrayOutputStream bos = new ByteArrayOutputStream();
    byte[] payload = new byte[64];
    bos.writeBytes("RIFF".getBytes(StandardCharsets.US_ASCII));
    writeLe32(bos, 0);               // RIFF size 占位
    bos.writeBytes("WAVE".getBytes(StandardCharsets.US_ASCII));
    bos.writeBytes("fmt ".getBytes(StandardCharsets.US_ASCII));
    writeLe32(bos, 16);              // fmt chunk size
    writeLe16(bos, 1);               // PCM
    writeLe16(bos, 1);               // mono
    writeLe32(bos, sampleRate);
    writeLe32(bos, sampleRate * 2);  // byte rate
    writeLe16(bos, 2);               // block align
    writeLe16(bos, bits);
    bos.writeBytes("data".getBytes(StandardCharsets.US_ASCII));
    writeLe32(bos, payload.length);
    bos.writeBytes(payload);
    byte[] all = bos.toByteArray();
    writeLe32At(all, 4, all.length - 8); // RIFF size
    return all;
  }

  private static void writeLe16(ByteArrayOutputStream bos, int v) {
    bos.write(v & 0xFF);
    bos.write((v >> 8) & 0xFF);
  }

  private static void writeLe32(ByteArrayOutputStream bos, int v) {
    bos.write(v & 0xFF);
    bos.write((v >> 8) & 0xFF);
    bos.write((v >> 16) & 0xFF);
    bos.write((v >> 24) & 0xFF);
  }

  private static void writeLe32At(byte[] b, int off, int v) {
    b[off] = (byte) (v & 0xFF);
    b[off + 1] = (byte) ((v >> 8) & 0xFF);
    b[off + 2] = (byte) ((v >> 16) & 0xFF);
    b[off + 3] = (byte) ((v >> 24) & 0xFF);
  }
}
