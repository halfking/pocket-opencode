# Phase 2 外部服务配置指南

本文档说明如何配置Phase 2依赖的外部服务（pyannote和kxmemory）。

---

## 1. Pyannote说话人分离服务配置

### 前置条件
- **HuggingFace Token**: `<REDACTED_CREDENTIAL>` (readonly)
- **服务器**: 192.168.31.30
- **端口**: 8002 (推荐)

### 步骤A: 在192.168.31.30上部署pyannote服务

#### 1. SSH到服务器
```bash
ssh user@192.168.31.30
```

#### 2. 安装依赖
```bash
# 安装pyannote-audio
pip install pyannote-audio torch torchaudio

# 接受模型许可（必须）
# 访问 https://huggingface.co/pyannote/speaker-diarization
# 点击 "Agree and access repository"
```

#### 3. 创建服务脚本
创建 `/opt/pyannote-service/app.py`:

```python
#!/usr/bin/env python3
"""
Pyannote Speaker Diarization HTTP Service
用于OpenPocket Phase 2说话人分离功能
"""

from flask import Flask, request, jsonify
from pyannote.audio import Pipeline
import torch
import tempfile
import os

app = Flask(__name__)

# 初始化pyannote pipeline
HF_TOKEN = os.environ.get("HF_TOKEN", "<REDACTED_CREDENTIAL>")
pipeline = Pipeline.from_pretrained(
    "pyannote/speaker-diarization",
    use_auth_token=HF_TOKEN
)

# 使用GPU if available
if torch.cuda.is_available():
    pipeline.to(torch.device("cuda"))

@app.route('/health', methods=['GET'])
def health():
    return jsonify({"status": "ok", "model": "pyannote/speaker-diarization"})

@app.route('/diarize', methods=['POST'])
def diarize():
    if 'audio' not in request.files:
        return jsonify({"error": "audio file required"}), 400
    
    audio_file = request.files['audio']
    min_speakers = request.form.get('min_speakers', type=int)
    max_speakers = request.form.get('max_speakers', type=int)
    num_speakers = request.form.get('num_speakers', type=int)
    
    # 保存临时文件
    with tempfile.NamedTemporaryFile(delete=False, suffix='.wav') as tmp:
        audio_file.save(tmp.name)
        tmp_path = tmp.name
    
    try:
        # 执行说话人分离
        kwargs = {}
        if num_speakers:
            kwargs['num_speakers'] = num_speakers
        elif min_speakers or max_speakers:
            kwargs['min_speakers'] = min_speakers or 1
            kwargs['max_speakers'] = max_speakers or 10
        
        diarization = pipeline(tmp_path, **kwargs)
        
        # 转换结果格式
        segments = []
        speakers = set()
        for turn, _, speaker in diarization.itertracks(yield_label=True):
            segments.append({
                "speaker": speaker,
                "start": turn.start,
                "end": turn.end,
                "text": ""  # 文本由ASR填充
            })
            speakers.add(speaker)
        
        return jsonify({
            "segments": segments,
            "speaker_count": len(speakers),
            "duration_ms": int(diarization.get_timeline().duration() * 1000)
        })
    
    finally:
        os.unlink(tmp_path)

if __name__ == '__main__':
    app.run(host='0.0.0.0', port=8002)
```

#### 4. 启动服务
```bash
cd /opt/pyannote-service
export HF_TOKEN="<REDACTED_CREDENTIAL>"
python app.py
```

#### 5. 使用systemd守护进程（可选）
创建 `/etc/systemd/system/pyannote.service`:

```ini
[Unit]
Description=Pyannote Speaker Diarization Service
After=network.target

[Service]
Type=simple
User=pyannote
WorkingDirectory=/opt/pyannote-service
Environment="HF_TOKEN=<REDACTED_CREDENTIAL>"
ExecStart=/usr/bin/python3 /opt/pyannote-service/app.py
Restart=always

[Install]
WantedBy=multi-user.target
```

启动服务：
```bash
sudo systemctl enable pyannote
sudo systemctl start pyannote
sudo systemctl status pyannote
```

### 步骤B: 配置pocketd连接pyannote服务

修改pocketd启动配置，添加环境变量：

```bash
export POCKET_DIARIZATION_URL="http://192.168.31.30:8002"
```

或者在代码中配置（需要修改server.go初始化部分）：

```go
// 在server.go的NewServer中添加
if diarizationURL := cfg.DiarizationURL; diarizationURL != "" {
    s.diarizationClient = diarization.NewClient(diarization.Config{
        BaseURL: diarizationURL,
        Timeout: 5 * time.Minute,
    })
}
```

### 验证
```bash
# 1. 测试健康检查
curl http://192.168.31.30:8002/health

# 2. 测试API端点
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:8088/api/diarize/health

# 3. 测试完整流程
curl -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -F "audio=@test.wav" \
  "http://localhost:8088/api/meetings/$MEETING_ID/diarize"
```

---

## 2. KxMemory服务配置

### 前置条件
- **服务器**: 192.168.31.211
- **推测端口**: 8080, 9000, 或其他（需确认）

### 步骤A: 确认kxmemory服务

SSH到211服务器确认服务状态：

```bash
ssh user@192.168.31.211

# 查找kxmemory进程
ps aux | grep kxmemory

# 查找监听端口
netstat -tlnp | grep LISTEN | grep -E "8080|9000|3000"

# 或使用docker
docker ps | grep kxmemory
```

### 步骤B: 配置pocketd连接kxmemory

根据backend/internal/config/config.go:110，需要设置：

```bash
export POCKET_KXMEMORY_BASE_URL="http://192.168.31.211:PORT"
```

其中PORT需要根据实际部署确定。

### 常见端口
- 8080: 标准HTTP
- 9000: FastAPI默认
- 3000: Node.js服务

### 验证
```bash
# 1. 测试kxmemory健康检查
curl http://192.168.31.211:PORT/health

# 2. 测试advisor端点
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:8088/api/advisor/health

# 预期：available: true, kxmemory: true
```

---

## 3. 完整配置示例

### pocketd启动脚本
```bash
#!/bin/bash
# start-pocketd.sh

cd /Users/xutaohuang/workspace/ai-native-tools/client/openpocket/backend

# 认证配置
export IDENTITY_SHARED_SECRET="92LKtn61FehqIs8dy2TxzEnTcO8CWbtkE26IE1+/60r416AaYizg+4ziLsXFeBpL"
export IDENTITY_ISSUER_ALLOWLIST="redclaw,memora,llm-gateway,pocket,acc"
export POCKET_AUTH_LEGACY_ONLY=true

# 数据库配置
export POCKET_POSTGRES_DSN="postgresql://[REDACTED:secret]@localhost:15432/openpocket_test?sslmode=disable"

# Phase 2外部服务
export POCKET_DIARIZATION_URL="http://192.168.31.30:8002"
export POCKET_KXMEMORY_BASE_URL="http://192.168.31.211:PORT"

# 启动服务
./pocketd
```

### 测试脚本
```bash
#!/bin/bash
# test-phase2.sh

# 生成token
export IDENTITY_SHARED_SECRET="92LKtn61FehqIs8dy2TxzEnTcO8CWbtkE26IE1+/60r416AaYizg+4ziLsXFeBpL"
TOKEN=$(go run backend/cmd/gentoken2/main.go)

echo "Testing Phase 2 endpoints..."

# 1. 健康检查
echo -e "\n=== Health Checks ==="
curl -s -H "Authorization: Bearer $TOKEN" http://localhost:8088/api/advisor/health | jq
curl -s -H "Authorization: Bearer $TOKEN" http://localhost:8088/api/diarize/health | jq

# 2. 创建测试会议
echo -e "\n=== Create Meeting ==="
RESPONSE=$(curl -s -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"title":"Phase 2 Test"}' \
  http://localhost:8088/api/meetings)
MEETING_ID=$(echo "$RESPONSE" | jq -r '.id')
echo "Meeting ID: $MEETING_ID"

# 3. 测试multilingual
echo -e "\n=== Multilingual Test ==="
curl -s -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -F "audio=@.artifacts/asr-s782/acoustic-speaker-mic.wav" \
  -F "languages=en,zh" \
  "http://localhost:8088/api/meetings/$MEETING_ID/multilingual" | jq

# 4. 测试diarize (需pyannote)
echo -e "\n=== Diarize Test ==="
curl -s -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -F "audio=@.artifacts/asr-s782/acoustic-speaker-mic.wav" \
  "http://localhost:8088/api/meetings/$MEETING_ID/diarize" | jq

# 5. 测试advisor (需kxmemory)
echo -e "\n=== Advisor Test ==="
curl -s -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"analysis_type":"summary"}' \
  "http://localhost:8088/api/meetings/$MEETING_ID/advisor" | jq

echo -e "\nDone!"
```

---

## 4. 故障排查

### Pyannote服务问题

**问题**: 健康检查失败
```bash
curl http://192.168.31.30:8002/health
# Connection refused
```

**解决**:
1. 检查服务是否运行：`ps aux | grep pyannote`
2. 检查端口监听：`netstat -tlnp | grep 8002`
3. 检查防火墙：`sudo ufw allow 8002`
4. 查看服务日志：`sudo journalctl -u pyannote -f`

**问题**: HuggingFace token无效
```bash
# pyannote报错：401 Unauthorized
```

**解决**:
1. 访问 https://huggingface.co/pyannote/speaker-diarization
2. 点击 "Agree and access repository"
3. 使用新token重启服务

### KxMemory服务问题

**问题**: advisor显示kxmemory:false
```json
{"available":false,"kxmemory":false}
```

**解决**:
1. 确认POCKET_KXMEMORY_BASE_URL已设置
2. 测试kxmemory连通性：`curl http://192.168.31.211:PORT/health`
3. 检查pocketd日志中的kxmemory连接错误

---

## 5. 下一步

完成上述配置后，Phase 2将达到**100%完成度**：

- ✅ JWT认证
- ✅ API端点验证
- ✅ Multilingual端到端测试
- ✅ Diarize功能（配置pyannote后）
- ✅ Advisor功能（配置kxmemory后）

---

**文档创建**: 2026-11-15  
**HuggingFace Token**: <REDACTED_CREDENTIAL> (readonly)  
**维护人**: 待指派
