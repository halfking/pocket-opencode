#!/usr/bin/env python3
"""
本地真实 ASR 服务（免费 / 离线 / 无需任何 API Key）。

用途：把「录音转录」这条链路从「假上游」升级成**真引擎**验证。
此前 scripts/mock-asr-server.mjs 只能证明「音频到了上游、请求形状对、
返回被正确解析」；它用的是写死的假文本，**证明不了识别得准**。
本服务用 faster-whisper（本地 CTranslate2，CPU 即可，不依赖 PyTorch）
真正跑一遍识别。

暴露的是 OpenAI 兼容契约 /v1/audio/transcriptions —— 正是后端 STT 外部
通道要对接的形状，因此不需要改任何产品代码就能接上：

    POCKET_STT_ALLOW_PRIVATE=true      # 允许指向 127.0.0.1（本轮新加的开关）
    externalBaseURL = http://127.0.0.1:<port>/v1
    externalModel   = whisper-small
    externalApiKey  = 任意非空值（本服务不校验）

依赖只有 faster-whisper（自带 PyAV 解码，不需要系统 ffmpeg）。

用法：
    python local-asr-server.py --port 18900 --model small
    # 或用中文小模型（更快，中文更好）：--model base / --model small
"""
from __future__ import annotations

import argparse
import io
import json
import os
import sys
import time
import uuid
import wave
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

# faster-whisper / ctranslate2 在多线程下会偶发崩溃（已知问题），限制线程数
os.environ.setdefault("OMP_NUM_THREADS", "4")

MODEL = None
MODEL_NAME = "?"
IGNORE_PROMPT = False
DEVICE = "cpu"
COMPUTE_TYPE = "int8"
STATS = {"requests": 0, "chars": 0, "audio_bytes": 0, "seconds": 0.0, "last_text": ""}


def log(msg: str) -> None:
    print(f"[local-asr] {msg}", flush=True)


def get_model(name: str):
    """惰性加载。首次会下载模型权重（免费来源：HuggingFace）。"""
    global MODEL, MODEL_NAME, DEVICE, COMPUTE_TYPE
    if MODEL is not None and MODEL_NAME == name:
        return MODEL
    from faster_whisper import WhisperModel

    # 有 CUDA 就用 fp16，没有就用 int8 的 CPU 量化（小模型上 CPU int8 足够快）
    try:
        import torch  # noqa: F401

        if torch.cuda.is_available():
            DEVICE, COMPUTE_TYPE = "cuda", "float16"
        else:
            DEVICE, COMPUTE_TYPE = "cpu", "int8"
    except Exception:
        DEVICE, COMPUTE_TYPE = "cpu", "int8"

    log(f"loading model={name} device={DEVICE} compute={COMPUTE_TYPE} (首次会下载权重)")
    t0 = time.time()
    MODEL = WhisperModel(name, device=DEVICE, compute_type=COMPUTE_TYPE)
    MODEL_NAME = name
    log(f"model ready in {time.time() - t0:.1f}s")
    return MODEL


def decode_to_pcm16k(audio_bytes: bytes):
    """
    把任意输入解成 16kHz 单声道 float32。

    为什么不直接用 faster_whisper.decode_audio：
    它内部调 av.open(..., metadata_errors=...)，而 faster-whisper 1.2.1 与
    av 19.x 不兼容，实测直接抛
      TypeError: open() got an unexpected keyword argument 'metadata_errors'
    （2026-10-01 本机实测）。与其去钉一个 av 版本，不如 PCM WAV 走标准库
    wave —— 零依赖、行为确定，而且浏览器 MediaRecorder 与手机录音落盘下来
    经过重采样的也正是 PCM WAV。非 WAV 格式才回退到 av（并把错误如实报出）。
    """
    import numpy as np

    # 1) PCM WAV：标准库直接解
    try:
        with wave.open(io.BytesIO(audio_bytes), "rb") as w:
            n_ch = w.getnchannels()
            width = w.getsampwidth()
            rate = w.getframerate()
            frames = w.readframes(w.getnframes())
        if width != 2:
            raise ValueError(f"unsupported sample width {width}")
        data = np.frombuffer(frames, dtype=np.int16).astype(np.float32) / 32768.0
        if n_ch > 1:
            data = data.reshape(-1, n_ch).mean(axis=1)
        if rate != 16000:
            # 线性重采样。语料与手机录音都是 16k，这条只是兜底。
            n_out = int(round(len(data) * 16000.0 / rate))
            data = np.interp(
                np.linspace(0, len(data) - 1, n_out, dtype=np.float64),
                np.arange(len(data), dtype=np.float64),
                data,
            ).astype(np.float32)
        return data
    except (wave.Error, ValueError, EOFError):
        pass

    # 2) 其他容器（webm/opus/m4a…）：走 av
    from faster_whisper import decode_audio

    return decode_audio(io.BytesIO(audio_bytes), sampling_rate=16000)


def transcribe(audio_bytes: bytes, language: str | None, upstream_prompt: str | None = None,
               use_builtin_bias: bool = True, delay_ms: int = 0) -> tuple[str, float]:
    """跑真识别。返回 (文本, 音频秒数)。

    use_builtin_bias=False 时**连内置兜底也一起关掉** —— 只丢上游 prompt
    而留着内置的，负控就不成立了（2026-10-01 实测踩过：B 组仍然返回简体，
    因为内置 prompt 还在兜着，对照完全无效）。

    delay_ms 用于**把单次转写人为拉长**。它存在的唯一理由是复现一个真实故障：
    http.Server 的 WriteTimeout=30s 会让「服务端耗时超过 30 秒」的请求
    **一个字节的响应都发不出去**（客户端只看到 EOF / Empty reply）。
    本机 CPU 推理够快，79 秒会议录音只要 28 秒，天然撞不到那条边界，
    所以必须能人为把请求推到 30 秒以上才能验证那条修复。
    """
    if delay_ms:
        time.sleep(delay_ms / 1000.0)
    t0 = time.time()
    pcm = decode_to_pcm16k(audio_bytes)
    dur = len(pcm) / 16000.0

    bias = None
    if use_builtin_bias:
        bias = upstream_prompt or "以下是普通话的句子，请用简体中文输出。"

    model = get_model(MODEL_NAME)
    segments, info = model.transcribe(
        pcm,
        language=(language or None),
        task="transcribe",
        beam_size=1,          # CPU 上求速度；要更高准确度可调 5
        vad_filter=True,      # 过滤静音，避免幻觉出「字幕bySubtitle」之类的东西
        condition_on_previous_text=False,
        # 强制简体输出。Whisper 系列在中文上默认吐**繁体**（2026-10-01 实测：
        # 简体的「帮我记一下明天要买牛奶和面包」被识别成繁体的
        # 「幫我記一下明天要買牛奶和麵包」，用字完全正确、只是字形不对）。
        # 这不是本服务的 bug，而是所有 whisper 系模型的共同行为 ——
        # 设置页预置的外部候选里就有 openai/whisper-large-v3-turbo，
        # 所以这是**产品侧要处理的真实问题**，不是这里可以绕过的小事。
        # OpenAI 官方给出的解法就是给一个普通话 initial_prompt 做偏置。
        # use_builtin_bias=False 时整条偏置关掉，用于负控对照。
        initial_prompt=bias,
    )
    text = "".join(s.text for s in segments).strip()
    log(
        f"transcribed {dur:.2f}s audio in {time.time() - t0:.2f}s "
        f"(rtf={(time.time() - t0) / dur:.2f}) -> {text[:80]}"
    )
    return text, dur


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, fmt, *args):  # 静音默认的逐行访问日志
        pass

    def _json(self, code: int, payload: dict):
        body = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        self.send_response(code)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Access-Control-Allow-Origin", "*")
        self.end_headers()
        self.wfile.write(body)

    def do_OPTIONS(self):
        self._json(204, {})

    def do_GET(self):
        if self.path.rstrip("/").endswith("/models"):
            self._json(200, {
                "object": "list",
                "data": [{"id": MODEL_NAME, "object": "model", "owned_by": "local-faster-whisper"}],
            })
            return
        self._json(200, {"service": "local-asr", "model": MODEL_NAME, "device": DEVICE})

    def do_POST(self):
        if "transcriptions" not in self.path:
            self._json(404, {"error": "not found"})
            return
        length = int(self.headers.get("Content-Length", "0"))
        raw = self.rfile.read(length)
        ctype = self.headers.get("Content-Type", "")
        language = None
        audio = None

        if ctype.startswith("multipart/form-data"):
            audio, language, prompt = self._parse_multipart(raw, ctype)
        else:
            # JSON 形态：{"audio": "<base64>"}
            try:
                j = json.loads(raw.decode("utf-8"))
                import base64

                audio = base64.b64decode(j.get("audio", ""))
                language = j.get("language")
                prompt = j.get("prompt")
            except Exception as e:
                self._json(400, {"error": f"cannot parse body: {e}"})
                return

        if not audio:
            self._json(400, {"error": "no audio field"})
            return
        # 上游 prompt 作为 initial_prompt 透传给 faster-whisper ——
        # 这与真实 OpenAI 兼容服务的语义一致。
        # 但 --no-bias 会把**所有** prompt（上游带来的与内置的）都丢掉，
        # 用来模拟「上游完全不吃 prompt」的情形，作为简体偏置那条修复的
        # **负控对照**：那时应当回落到繁体。
        if IGNORE_PROMPT:
            if prompt:
                log("已收到上游 prompt，但 --no-bias 生效，故意忽略（负控）")
            prompt = None

        try:
            text, dur = transcribe(audio, language, prompt or None, use_builtin_bias=not IGNORE_PROMPT, delay_ms=DELAY_MS)
        except Exception as e:  # 真实引擎会抛真错，不要吞
            log(f"ERROR {type(e).__name__}: {e}")
            self._json(500, {"error": {"message": f"{type(e).__name__}: {e}"}})
            return

        STATS["requests"] += 1
        STATS["chars"] += len(text)
        STATS["audio_bytes"] += len(audio)
        STATS["seconds"] += dur
        STATS["last_text"] = text
        self._json(200, {
            "text": text,
            "language": language or "zh",
            "duration": dur,
            "model": MODEL_NAME,
            "id": f"local-{uuid.uuid4().hex[:12]}",
        })

    @staticmethod
    def _parse_multipart(raw: bytes, ctype: str):
        import re

        m = re.search(r'boundary="?([^";]+)"?', ctype)
        if not m:
            return None, None
        boundary = ("--" + m.group(1)).encode()
        audio = None
        language = None
        prompt = None
        for part in raw.split(boundary):
            if b"\r\n\r\n" not in part:
                continue
            head, body = part.split(b"\r\n\r\n", 1)
            body = body.rstrip(b"\r\n-")
            name = re.search(rb'name="([^"]+)"', head)
            if not name:
                continue
            name = name.group(1)
            if name == b"file" or name == b"audio":
                audio = body
            elif name == b"language":
                language = body.decode("utf-8", "ignore").strip()
            elif name == b"prompt":
                prompt = body.decode("utf-8", "ignore").strip()
        return audio, language, prompt


def main() -> int:
    global MODEL_NAME, IGNORE_PROMPT, DELAY_MS
    ap = argparse.ArgumentParser()
    ap.add_argument("--port", type=int, default=18900)
    ap.add_argument("--host", default="127.0.0.1")
    ap.add_argument("--delay-ms", type=int, default=0,
                    help="每段人为延迟多少毫秒，用来把总耗时推过 http.Server 的 30s WriteTimeout")
    ap.add_argument("--no-bias", action="store_true",
                    help="忽略所有 prompt（含上游带来的），用作简体偏置修复的负控对照")
    ap.add_argument("--model", default="small",
                    help="faster-whisper 模型名：tiny/base/small/medium/large-v3")
    args = ap.parse_args()
    MODEL_NAME = args.model
    IGNORE_PROMPT = args.no_bias
    DELAY_MS = args.delay_ms

    srv = ThreadingHTTPServer((args.host, args.port), Handler)
    log(f"listening on http://{args.host}:{args.port} (model={args.model})")
    log("OpenAI 兼容端点：POST /v1/audio/transcriptions  （multipart: file + language）")
    try:
        srv.serve_forever()
    except KeyboardInterrupt:
        log(f"bye  stats={STATS}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
