#!/usr/bin/env python3
"""
pyannote-audio 说话人分离服务
用于openpocket后端的说话人识别功能

依赖: pip install pyannote.audio flask whisperx torch
"""
import os
import sys
from flask import Flask, request, jsonify
from pyannote.audio import Pipeline
import torch
import tempfile
import time

app = Flask(__name__)

# 全局pipeline（启动时加载）
diarization_pipeline = None

def init_pipeline():
    """初始化pyannote pipeline"""
    global diarization_pipeline
    
    # 从环境变量获取HuggingFace token
    hf_token = os.getenv('HF_TOKEN', '')
    if not hf_token:
        print("WARNING: HF_TOKEN not set, using public models only", file=sys.stderr)
    
    try:
        # 加载说话人分离pipeline（使用最新的3.1版本）
        # pyannote 3.4.0+ 使用 token 参数替代 use_auth_token
        diarization_pipeline = Pipeline.from_pretrained(
            "pyannote/speaker-diarization-3.1",
            token=hf_token if hf_token else None
        )
        
        # 如果有GPU则使用GPU
        device = torch.device("cuda" if torch.cuda.is_available() else "cpu")
        diarization_pipeline.to(device)
        
        print(f"Diarization pipeline loaded on {device}", file=sys.stderr)
        return True
    except Exception as e:
        print(f"Failed to load pipeline: {e}", file=sys.stderr)
        return False

@app.route('/health', methods=['GET'])
def health():
    """健康检查"""
    if diarization_pipeline is None:
        return jsonify({"status": "unhealthy", "error": "pipeline not loaded"}), 503
    return jsonify({"status": "ok", "device": str(diarization_pipeline.device)})

@app.route('/diarize', methods=['POST'])
def diarize():
    """
    说话人分离API
    
    输入: multipart/form-data
      - audio: 音频文件（WAV/MP3等）
      - num_speakers: 预期说话人数（可选）
      - min_speakers: 最少说话人数（可选，默认1）
      - max_speakers: 最多说话人数（可选，默认不限）
    
    输出: JSON
      {
        "segments": [
          {"speaker": "SPEAKER_00", "start": 0.5, "end": 3.2},
          ...
        ],
        "speaker_count": 2,
        "duration_ms": 5000
      }
    """
    if diarization_pipeline is None:
        return jsonify({"error": "pipeline not initialized"}), 503
    
    if 'audio' not in request.files:
        return jsonify({"error": "no audio file provided"}), 400
    
    audio_file = request.files['audio']
    
    # 解析参数
    num_speakers = request.form.get('num_speakers', type=int)
    min_speakers = request.form.get('min_speakers', type=int, default=1)
    max_speakers = request.form.get('max_speakers', type=int)
    
    # 保存到临时文件
    with tempfile.NamedTemporaryFile(suffix='.wav', delete=False) as tmp:
        tmp_path = tmp.name
        audio_file.save(tmp_path)
    
    try:
        t0 = time.time()
        
        # 执行说话人分离
        kwargs = {}
        if num_speakers:
            kwargs['num_speakers'] = num_speakers
        else:
            kwargs['min_speakers'] = min_speakers
            if max_speakers:
                kwargs['max_speakers'] = max_speakers
        
        diarization = diarization_pipeline(tmp_path, **kwargs)
        
        # 转换为segments格式
        segments = []
        speakers = set()
        for turn, _, speaker in diarization.itertracks(yield_label=True):
            segments.append({
                "speaker": speaker,
                "start": round(turn.start, 3),
                "end": round(turn.end, 3)
            })
            speakers.add(speaker)
        
        duration_ms = int((time.time() - t0) * 1000)
        
        return jsonify({
            "segments": segments,
            "speaker_count": len(speakers),
            "duration_ms": duration_ms
        })
        
    except Exception as e:
        return jsonify({"error": str(e)}), 500
    finally:
        # 清理临时文件
        if os.path.exists(tmp_path):
            os.unlink(tmp_path)

@app.route('/diarize_with_transcript', methods=['POST'])
def diarize_with_transcript():
    """
    说话人分离 + ASR对齐
    
    输入: multipart/form-data
      - audio: 音频文件
      - transcript: 已有转写文本（可选，如果有则只做对齐）
      - model: Whisper模型（默认large-v2）
      - align: 是否进行词级对齐（默认true）
    
    输出: JSON
      {
        "segments": [
          {"speaker": "SPEAKER_00", "text": "你好", "start": 0.5, "end": 1.2},
          ...
        ],
        "speaker_count": 2,
        "duration_ms": 8000
      }
    """
    # 这个功能需要WhisperX，暂时返回not implemented
    return jsonify({"error": "whisperx integration pending"}), 501

if __name__ == '__main__':
    print("Initializing pyannote-audio service...")
    if not init_pipeline():
        print("Failed to initialize, exiting")
        sys.exit(1)
    
    port = int(os.getenv('PORT', 8002))
    print(f"Starting server on port {port}")
    app.run(host='0.0.0.0', port=port, threaded=True)
