package com.kaixuan.opencode.pocket.plugins;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.Service;
import android.content.Context;
import android.content.Intent;
import android.content.pm.ServiceInfo;
import android.media.AudioDeviceInfo;
import android.media.AudioFormat;
import android.media.AudioManager;
import android.media.AudioRecord;
import android.media.MediaRecorder;
import android.media.audiofx.AcousticEchoCanceler;
import android.media.audiofx.AutomaticGainControl;
import android.media.audiofx.NoiseSuppressor;
import android.os.Build;
import android.os.IBinder;
import androidx.core.app.NotificationCompat;
import com.kaixuan.opencode.pocket.R;
import java.io.ByteArrayOutputStream;
import java.nio.ByteBuffer;
import java.nio.ByteOrder;

/**
 * 前台麦克风服务：切后台/熄屏继续采音，约 8s 一片 WAV 回调 JS。
 * 多麦：setPreferredDevice + VOICE_RECOGNITION + AEC/NS/AGC。
 */
public class MeetingRecordService extends Service {
  public static final String ACTION_START = "com.kaixuan.opencode.pocket.MIC_START";
  public static final String ACTION_STOP = "com.kaixuan.opencode.pocket.MIC_STOP";
  public static final String EXTRA_MEETING = "meetingId";
  public static final String EXTRA_DEVICE = "deviceId";
  private static final String CHANNEL_ID = "meeting_record";
  private static final int NOTIF_ID = 42;
  private static final int SAMPLE_RATE = 16000;
  private static final int PART_MS = 8000;

  private volatile boolean running = false;
  private Thread worker;
  private AudioRecord recorder;
  private String meetingId = "";

  @Override
  public IBinder onBind(Intent intent) {
    return null;
  }

  @Override
  public int onStartCommand(Intent intent, int flags, int startId) {
    if (intent != null && ACTION_STOP.equals(intent.getAction())) {
      stopCapture();
      stopForeground(true);
      stopSelf();
      return START_NOT_STICKY;
    }
    meetingId = intent != null ? intent.getStringExtra(EXTRA_MEETING) : "";
    String deviceId = intent != null ? intent.getStringExtra(EXTRA_DEVICE) : null;
    startForegroundCompat();
    startCapture(deviceId);
    return START_STICKY;
  }

  private void startForegroundCompat() {
    NotificationManager nm = (NotificationManager) getSystemService(Context.NOTIFICATION_SERVICE);
    if (Build.VERSION.SDK_INT >= 26 && nm != null) {
      NotificationChannel ch = new NotificationChannel(CHANNEL_ID, "会话录音", NotificationManager.IMPORTANCE_LOW);
      ch.setDescription("正在录音");
      nm.createNotificationChannel(ch);
    }
    Notification notif = new NotificationCompat.Builder(this, CHANNEL_ID)
        .setContentTitle("正在录音")
        .setContentText("会话实时记录进行中")
        .setSmallIcon(R.mipmap.ic_launcher)
        .setOngoing(true)
        .build();
    if (Build.VERSION.SDK_INT >= 29) {
      startForeground(NOTIF_ID, notif, ServiceInfo.FOREGROUND_SERVICE_TYPE_MICROPHONE);
    } else {
      startForeground(NOTIF_ID, notif);
    }
  }

  private void startCapture(String deviceId) {
    stopCapture();
    running = true;
    worker = new Thread(() -> loop(deviceId), "meeting-mic");
    worker.start();
  }

  private void loop(String deviceId) {
    AudioManager am = (AudioManager) getSystemService(Context.AUDIO_SERVICE);
    AudioDeviceInfo preferred = am != null ? AudioDeviceRank.find(am, deviceId) : null;
    int[] layouts = new int[] { AudioFormat.CHANNEL_IN_STEREO, AudioFormat.CHANNEL_IN_MONO };
    AudioRecord rec = null;
    int channels = 1;
    for (int layout : layouts) {
      channels = layout == AudioFormat.CHANNEL_IN_STEREO ? 2 : 1;
      int min = AudioRecord.getMinBufferSize(SAMPLE_RATE, layout, AudioFormat.ENCODING_PCM_16BIT);
      if (min <= 0) continue;
      try {
        rec = new AudioRecord(MediaRecorder.AudioSource.VOICE_RECOGNITION,
            SAMPLE_RATE, layout, AudioFormat.ENCODING_PCM_16BIT, min * 4);
        if (rec.getState() != AudioRecord.STATE_INITIALIZED) {
          rec.release();
          rec = null;
          continue;
        }
        if (preferred != null && Build.VERSION.SDK_INT >= 23) {
          rec.setPreferredDevice(preferred);
        }
        attachFx(rec.getAudioSessionId());
        break;
      } catch (Exception e) {
        if (rec != null) rec.release();
        rec = null;
      }
    }
    if (rec == null) {
      BackgroundMicPlugin.emitError("无法打开麦克风");
      return;
    }
    recorder = rec;
    rec.startRecording();
    int frame = SAMPLE_RATE / 10 * channels;
    short[] buf = new short[frame];
    ByteArrayOutputStream pcm = new ByteArrayOutputStream();
    long captureStart = System.currentTimeMillis();
    long partStartRel = 0;
    int seq = 0;
    try {
      while (running) {
        int n = rec.read(buf, 0, buf.length);
        if (n <= 0) continue;
        short[] out = louderMono(buf, n, channels);
        for (short s : out) {
          pcm.write(s & 0xff);
          pcm.write((s >> 8) & 0xff);
        }
        long nowRel = System.currentTimeMillis() - captureStart;
        if (nowRel - partStartRel >= PART_MS && pcm.size() > 0) {
          seq++;
          byte[] wav = pcmToWav(pcm.toByteArray(), SAMPLE_RATE, 1);
          BackgroundMicPlugin.emitPart(seq, wav, partStartRel, nowRel);
          pcm.reset();
          partStartRel = nowRel;
        }
      }
      if (pcm.size() > 0) {
        seq++;
        long nowRel = System.currentTimeMillis() - captureStart;
        byte[] wav = pcmToWav(pcm.toByteArray(), SAMPLE_RATE, 1);
        BackgroundMicPlugin.emitPart(seq, wav, partStartRel, nowRel);
      }
    } catch (Exception e) {
      BackgroundMicPlugin.emitError(e.getMessage() != null ? e.getMessage() : "录音中断");
    } finally {
      try { rec.stop(); } catch (Exception ignored) {}
      rec.release();
      recorder = null;
    }
  }

  private static void attachFx(int session) {
    try {
      if (AcousticEchoCanceler.isAvailable()) AcousticEchoCanceler.create(session);
      if (NoiseSuppressor.isAvailable()) NoiseSuppressor.create(session);
      if (AutomaticGainControl.isAvailable()) AutomaticGainControl.create(session);
    } catch (Exception ignored) {}
  }

  private static short[] louderMono(short[] buf, int n, int channels) {
    if (channels == 1) {
      short[] copy = new short[n];
      System.arraycopy(buf, 0, copy, 0, n);
      return copy;
    }
    long eL = 0, eR = 0;
    int frames = n / 2;
    for (int i = 0; i < frames; i++) {
      int l = buf[i * 2];
      int r = buf[i * 2 + 1];
      eL += (long) l * l;
      eR += (long) r * r;
    }
    boolean right = eR > eL * 1.08;
    short[] mono = new short[frames];
    for (int i = 0; i < frames; i++) {
      mono[i] = buf[i * 2 + (right ? 1 : 0)];
    }
    return mono;
  }

  private static byte[] pcmToWav(byte[] pcm, int sampleRate, int channels) {
    int dataLen = pcm.length;
    ByteBuffer b = ByteBuffer.allocate(44 + dataLen).order(ByteOrder.LITTLE_ENDIAN);
    b.put("RIFF".getBytes());
    b.putInt(36 + dataLen);
    b.put("WAVE".getBytes());
    b.put("fmt ".getBytes());
    b.putInt(16);
    b.putShort((short) 1);
    b.putShort((short) channels);
    b.putInt(sampleRate);
    b.putInt(sampleRate * channels * 2);
    b.putShort((short) (channels * 2));
    b.putShort((short) 16);
    b.put("data".getBytes());
    b.putInt(dataLen);
    b.put(pcm);
    return b.array();
  }

  private void stopCapture() {
    running = false;
    if (worker != null) {
      try { worker.join(1500); } catch (InterruptedException ignored) {}
      worker = null;
    }
  }

  @Override
  public void onDestroy() {
    stopCapture();
    super.onDestroy();
  }
}
