import com.k2fsa.sherpa.onnx.EndpointConfig;
import com.k2fsa.sherpa.onnx.EndpointRule;
import com.k2fsa.sherpa.onnx.FeatureConfig;
import com.k2fsa.sherpa.onnx.HomophoneReplacerConfig;
import com.k2fsa.sherpa.onnx.OfflineRecognizer;
import com.k2fsa.sherpa.onnx.OfflineRecognizerConfig;
import com.k2fsa.sherpa.onnx.OfflineModelConfig;
import com.k2fsa.sherpa.onnx.OfflineSenseVoiceModelConfig;
import com.k2fsa.sherpa.onnx.OnlineCtcFstDecoderConfig;
import com.k2fsa.sherpa.onnx.OnlineLMConfig;
import com.k2fsa.sherpa.onnx.OnlineModelConfig;
import com.k2fsa.sherpa.onnx.OnlineParaformerModelConfig;
import com.k2fsa.sherpa.onnx.OnlineZipformer2CtcModelConfig;
import com.k2fsa.sherpa.onnx.OnlineNeMoCtcModelConfig;
import com.k2fsa.sherpa.onnx.OnlineToneCtcModelConfig;
import com.k2fsa.sherpa.onnx.OnlineRecognizer;
import com.k2fsa.sherpa.onnx.OnlineRecognizerConfig;
import com.k2fsa.sherpa.onnx.OnlineRecognizerResult;
import com.k2fsa.sherpa.onnx.OnlineStream;
import com.k2fsa.sherpa.onnx.OnlineTransducerModelConfig;
import com.k2fsa.sherpa.onnx.QnnConfig;

import javax.sound.sampled.AudioFormat;
import javax.sound.sampled.AudioInputStream;
import javax.sound.sampled.AudioSystem;
import java.io.File;
import java.nio.ByteBuffer;
import java.nio.ByteOrder;
import java.nio.file.Files;
import java.util.ArrayList;
import java.util.List;

/**
 * 端上实时转写可行性探针（2026-10-06 ASR 多模型轮）。
 *
 * 与 SherpaPlugin 的实时路径逐参同构（100ms 块 / 16k / 三端点规则
 * [2.4s trailing, 1.2s utterance, 20s max] / modified_beam_search beam=4 /
 * int8 zipformer small 双语 / SenseVoice int8 离线），在桌面 arm64 JVM 上量：
 *
 *   1) RTF：块尽可能快地喂入，识别耗时 / 音频时长（算力预算判据）；
 *   2) 实时仿真：按真实墙钟每 100ms 喂一块，量「出字及时性」——
 *      TTFP（语音开始到第一条非空 partial）、partial 条数、
 *      尾延迟（最后一块喂入到端点判停+final 文本）；
 *   3) SenseVoice 离线整段延迟与文本（本地高精档对照）；
 *   4) 粗粒度内存（识别器构造后 used heap）。
 *
 * 参考真值写在 REF_ZH（TTS 合成样本的原文），输出字级相似率。
 * 用法：java LatencyProbe <zipformerDir> <sensevoiceDir> <wav16k>
 */
public class LatencyProbe {

  static final int SAMPLE_RATE = 16000;
  // 与 SherpaPlugin.recognizeWithOnline / startListening 同款文本
  static final String REF_ZH =
      "各位好，今天我们评审实时转写网关的第二阶段方案，重点是多供应商接入、精细化转写和实时摘要三个模块。";

  public static void main(String[] args) throws Exception {
    File zfDir = new File(args[0]);
    File svDir = new File(args[1]);
    File wav = new File(args[2]);
    float[] samples = readWav16kMono(wav);
    double audioSec = samples.length / (double) SAMPLE_RATE;
    System.out.printf("{\"audio_sec\": %.2f}%n", audioSec);

    Runtime rt = Runtime.getRuntime();
    long heapBefore = usedHeap(rt);

    // ── 流式 zipformer（插件实时档同构配置）─────────────────────────
    OnlineRecognizer online = buildOnline(zfDir);
    long heapAfterInit = usedHeap(rt);

    // 1) RTF 模式
    long t0 = System.nanoTime();
    String rtfText = streamThrough(online, samples, null);
    long rtfNs = System.nanoTime() - t0;
    double rtf = (rtfNs / 1e9) / audioSec;
    System.out.printf("{\"mode\":\"rtf\", \"rtf\": %.4f, \"proc_ms\": %d, \"sim\": %.3f}%n",
        rtf, rtfNs / 1_000_000, sim(rtfText, REF_ZH));

    // 2) 实时仿真（100ms 墙钟节拍）
    List<long[]> partials = new ArrayList<>();
    long firstPartialMs = -1, endToEndFinalMs = -1;
    long simStart = System.currentTimeMillis();
    StringBuilder finalText = new StringBuilder();
    int chunk = SAMPLE_RATE / 10;
    OnlineStream stream = online.createStream("");
    int fedChunks = 0;
    int lastSpeechChunk = 0;
    for (int off = 0; off < samples.length; off += chunk) {
      int len = Math.min(chunk, samples.length - off);
      float[] part = new float[len];
      System.arraycopy(samples, off, part, 0, len);
      // 粗 VAD：能量门限判定「这段在说话」，用于 TTFP 的语音起点锚定
      double rms = rms(part);
      if (rms > 200) lastSpeechChunk = fedChunks;
      stream.acceptWaveform(part, SAMPLE_RATE);
      while (online.isReady(stream)) online.decode(stream);
      OnlineRecognizerResult r = online.getResult(stream);
      if (r.getText() != null && !r.getText().isEmpty()) {
        long now = System.currentTimeMillis() - simStart;
        if (firstPartialMs < 0) firstPartialMs = now;
        partials.add(new long[]{now, r.getText().length()});
      }
      if (online.isEndpoint(stream)) {
        OnlineRecognizerResult fr = online.getResult(stream);
        finalText.append(fr.getText());
        online.reset(stream);
      }
      fedChunks++;
      // 墙钟节拍：每块 100ms（实时流语义）
      long target = simStart + (long) ((fedChunks + 1) * 100);
      long sleep = target - System.currentTimeMillis();
      if (sleep > 0) Thread.sleep(sleep);
    }
    long lastFedAt = System.currentTimeMillis();
    stream.inputFinished();
    while (online.isReady(stream)) online.decode(stream);
    OnlineRecognizerResult tail = online.getResult(stream);
    finalText.append(tail.getText());
    endToEndFinalMs = System.currentTimeMillis() - lastFedAt;
    double simTotal = (System.currentTimeMillis() - simStart) / 1000.0;
    System.out.printf("{\"mode\":\"realtime_sim\", \"ttfp_ms\": %d, \"partials\": %d, \"tail_final_ms\": %d, \"sim_total_sec\": %.2f, \"text\": \"%s\", \"sim\": %.3f}%n",
        firstPartialMs, partials.size(), endToEndFinalMs, simTotal, jsonEsc(finalText.toString()), sim(finalText.toString(), REF_ZH));

    // ── SenseVoice 离线（本地高精档对照）───────────────────────────
    OfflineRecognizer offline = buildOffline(svDir);
    long heapAfterOffline = usedHeap(rt);
    long o0 = System.nanoTime();
    com.k2fsa.sherpa.onnx.OfflineStream os = offline.createStream();
    os.acceptWaveform(samples, SAMPLE_RATE);
    offline.decode(os);
    com.k2fsa.sherpa.onnx.OfflineRecognizerResult ores = offline.getResult(os);
    String oText = ores != null && ores.getText() != null ? ores.getText() : "";
    long oNs = System.nanoTime() - o0;
    System.out.printf("{\"mode\":\"sensevoice_offline\", \"latency_ms\": %d, \"text\": \"%s\", \"sim\": %.3f}%n",
        oNs / 1_000_000, jsonEsc(oText), sim(oText, REF_ZH));

    System.out.printf("{\"heap_mb\": {\"before\": %d, \"after_online_init\": %d, \"after_offline_init\": %d}}%n",
        heapBefore >> 20, heapAfterInit >> 20, heapAfterOffline >> 20);
  }

  static OnlineRecognizer buildOnline(File dir) {
    OnlineTransducerModelConfig transducer = new OnlineTransducerModelConfig(
        new File(dir, "encoder-epoch-99-avg-1.int8.onnx").getAbsolutePath(),
        new File(dir, "decoder-epoch-99-avg-1.onnx").getAbsolutePath(),
        new File(dir, "joiner-epoch-99-avg-1.int8.onnx").getAbsolutePath(),
        new QnnConfig("", "", ""));
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
    return new OnlineRecognizer(null, cfg);
  }

  static OfflineRecognizer buildOffline(File dir) {
    OfflineModelConfig mc = new OfflineModelConfig();
    mc.setSenseVoice(new OfflineSenseVoiceModelConfig(
        new File(dir, "model.int8.onnx").getAbsolutePath(),
        "", true, new QnnConfig("", "", "")));
    mc.setTokens(new File(dir, "tokens.txt").getAbsolutePath());
    mc.setNumThreads(2);
    mc.setDebug(false);
    mc.setProvider("cpu");
    mc.setModelType("sense_voice");
    OfflineRecognizerConfig cfg = new OfflineRecognizerConfig();
    cfg.setFeatConfig(new FeatureConfig(SAMPLE_RATE, 80, 0.0f));
    cfg.setModelConfig(mc);
    return new OfflineRecognizer(null, cfg);
  }

  /** RTF 模式的整段过流式（无墙钟节拍，端点判停逻辑同插件）。 */
  static String streamThrough(OnlineRecognizer online, float[] samples, List<long[]> sink) {
    OnlineStream stream = online.createStream("");
    StringBuilder out = new StringBuilder();
    int chunk = SAMPLE_RATE / 10;
    for (int off = 0; off < samples.length; off += chunk) {
      int len = Math.min(chunk, samples.length - off);
      float[] part = new float[len];
      System.arraycopy(samples, off, part, 0, len);
      stream.acceptWaveform(part, SAMPLE_RATE);
      while (online.isReady(stream)) online.decode(stream);
      if (online.isEndpoint(stream)) {
        out.append(online.getResult(stream).getText());
        online.reset(stream);
      }
    }
    stream.inputFinished();
    while (online.isReady(stream)) online.decode(stream);
    out.append(online.getResult(stream).getText());
    return out.toString();
  }

  static float[] readWav16kMono(File f) throws Exception {
    byte[] all = Files.readAllBytes(f.toPath());
    // 标记扫描：afconvert/ffmpeg 会写 LIST 等附加块且对齐垫不一致，
    // 逐块遍历容易踩垫片；直接搜 "fmt " / "data" 标记更皮实（只用于测试探针）。
    int fmtOff = indexOf(all, "fmt ".getBytes("US-ASCII"), 12);
    int dataOff = indexOf(all, "data".getBytes("US-ASCII"), 12);
    if (fmtOff < 0 || dataOff < 0) throw new IllegalArgumentException("missing fmt/data chunk in " + f);
    // 注意：ByteBuffer.wrap(array, offset, len) 的 position 语义是**数组
    // 绝对位置**（初值=offset），不是切片相对位置——用顺序读取避免踩坑。
    ByteBuffer fmt = ByteBuffer.wrap(all, fmtOff + 8, 16).order(ByteOrder.LITTLE_ENDIAN);
    fmt.getShort();            // audio format (1=PCM)
    short channels = fmt.getShort();
    int rate = fmt.getInt();
    fmt.getInt();              // byte rate
    fmt.getShort();            // block align
    short bits = fmt.getShort();
    int dataLen = ByteBuffer.wrap(all, dataOff + 4, 4).order(ByteOrder.LITTLE_ENDIAN).getInt();
    dataOff += 8;
    if (dataLen <= 0 || dataOff + dataLen > all.length) dataLen = all.length - dataOff;
    if (rate != SAMPLE_RATE || bits != 16) throw new IllegalArgumentException(
        "expect 16k/16bit mono, got " + rate + "Hz/" + bits + "bit/" + channels + "ch"
        + " — 先 afconvert 转码");
    ByteBuffer bb = ByteBuffer.wrap(all, dataOff, dataLen).order(ByteOrder.LITTLE_ENDIAN);
    float[] out = new float[dataLen / 2 / channels];
    for (int i = 0; i < out.length; i++) {
      int acc = 0;
      for (int c = 0; c < channels; c++) acc += bb.getShort();
      out[i] = (acc / (float) channels) / 32768f;
    }
    return out;
  }

  static int indexOf(byte[] hay, byte[] needle, int from) {
    outer:
    for (int i = from; i <= hay.length - needle.length; i++) {
      for (int j = 0; j < needle.length; j++) if (hay[i + j] != needle[j]) continue outer;
      return i;
    }
    return -1;
  }

  static double rms(float[] a) {
    double s = 0;
    for (float v : a) s += v * v;
    return Math.sqrt(s / a.length) * 32768;
  }

  /** 字级相似率（标点/空白归一后 LCS），与 verify-gateway-audio-multi.mjs 同口径。 */
  static double sim(String a, String b) {
    String x = a == null ? "" : a.replaceAll("[\\s\\p{P}\\p{S}]+", "");
    String y = b.replaceAll("[\\s\\p{P}\\p{S}]+", "");
    if (x.isEmpty() || y.isEmpty()) return 0;
    int m = x.length(), n = y.length();
    int[] prev = new int[n + 1], cur = new int[n + 1];
    for (int i = 1; i <= m; i++) {
      for (int j = 1; j <= n; j++) {
        cur[j] = x.charAt(i - 1) == y.charAt(j - 1)
            ? prev[j - 1] + 1 : Math.max(prev[j], cur[j - 1]);
      }
      int[] t = prev; prev = cur; cur = t;
    }
    return prev[n] / (double) Math.max(m, n);
  }

  static String jsonEsc(String s) {
    return s == null ? "" : s.replace("\\", "\\\\").replace("\"", "\\\"");
  }

  static long usedHeap(Runtime rt) {
    rt.gc();
    return rt.totalMemory() - rt.freeMemory();
  }
}
