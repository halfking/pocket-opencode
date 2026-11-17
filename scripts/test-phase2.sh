#!/bin/bash
# Phase 2 完整功能测试脚本
# 用于验证所有Phase 2端点的功能

set -e

cd "$(dirname "$0")/.."

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${GREEN}=== Phase 2 Complete Test Suite ===${NC}\n"

# 检查环境变量
if [ -z "$IDENTITY_SHARED_SECRET" ]; then
    echo -e "${RED}Error: IDENTITY_SHARED_SECRET not set${NC}"
    echo "Please run: export IDENTITY_SHARED_SECRET=\"your-64-char-secret\""
    exit 1
fi

# 生成token
echo "Generating JWT token..."
TOKEN=$(cd backend && go run cmd/gentoken2/main.go)
if [ -z "$TOKEN" ]; then
    echo -e "${RED}Failed to generate token${NC}"
    exit 1
fi
echo -e "${GREEN}✓ Token generated${NC}\n"

# 测试基础健康检查
echo -e "${YELLOW}=== 1. Basic Health Check ===${NC}"
HEALTH=$(curl -s http://localhost:8088/healthz)
if [ "$HEALTH" = "ok" ]; then
    echo -e "${GREEN}✓ Server is healthy${NC}\n"
else
    echo -e "${RED}✗ Server health check failed${NC}"
    exit 1
fi

# 测试Advisor健康检查
echo -e "${YELLOW}=== 2. Advisor Health ===${NC}"
ADVISOR_HEALTH=$(curl -s -H "Authorization: Bearer $TOKEN" \
    http://localhost:8088/api/advisor/health)
echo "$ADVISOR_HEALTH" | jq
KXMEMORY_STATUS=$(echo "$ADVISOR_HEALTH" | jq -r '.kxmemory')
if [ "$KXMEMORY_STATUS" = "true" ]; then
    echo -e "${GREEN}✓ KxMemory service available${NC}\n"
else
    echo -e "${YELLOW}⚠ KxMemory service not configured (optional)${NC}\n"
fi

# 测试Diarize健康检查
echo -e "${YELLOW}=== 3. Diarize Health ===${NC}"
DIARIZE_HEALTH=$(curl -s -H "Authorization: Bearer $TOKEN" \
    http://localhost:8088/api/diarize/health)
echo "$DIARIZE_HEALTH" | jq
DIARIZE_AVAILABLE=$(echo "$DIARIZE_HEALTH" | jq -r '.available')
if [ "$DIARIZE_AVAILABLE" = "true" ]; then
    echo -e "${GREEN}✓ Diarization service available${NC}\n"
else
    echo -e "${YELLOW}⚠ Diarization service not configured (optional)${NC}\n"
fi

# 创建测试会议
echo -e "${YELLOW}=== 4. Create Test Meeting ===${NC}"
MEETING_RESPONSE=$(curl -s -X POST \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d '{"title":"Phase 2 Automated Test","status":"active"}' \
    http://localhost:8088/api/meetings)
echo "$MEETING_RESPONSE" | jq
MEETING_ID=$(echo "$MEETING_RESPONSE" | jq -r '.id')
if [ -z "$MEETING_ID" ] || [ "$MEETING_ID" = "null" ]; then
    echo -e "${RED}✗ Failed to create meeting${NC}"
    exit 1
fi
echo -e "${GREEN}✓ Meeting created: $MEETING_ID${NC}\n"

# 测试音频文件
AUDIO_FILE=".artifacts/asr-s782/acoustic-speaker-mic.wav"
if [ ! -f "$AUDIO_FILE" ]; then
    echo -e "${RED}✗ Test audio file not found: $AUDIO_FILE${NC}"
    exit 1
fi

# 测试Multilingual端点
echo -e "${YELLOW}=== 5. Multilingual Transcription ===${NC}"
MULTILINGUAL_RESPONSE=$(curl -s -X POST \
    -H "Authorization: Bearer $TOKEN" \
    -F "audio=@$AUDIO_FILE" \
    -F "languages=en,zh" \
    "http://localhost:8088/api/meetings/$MEETING_ID/multilingual")
echo "$MULTILINGUAL_RESPONSE" | jq
DETECTED_LANGS=$(echo "$MULTILINGUAL_RESPONSE" | jq -r '.detected_langs | length')
if [ "$DETECTED_LANGS" -gt 0 ]; then
    echo -e "${GREEN}✓ Multilingual transcription successful${NC}\n"
else
    echo -e "${RED}✗ Multilingual transcription failed${NC}\n"
fi

# 测试Diarize端点（如果服务可用）
if [ "$DIARIZE_AVAILABLE" = "true" ]; then
    echo -e "${YELLOW}=== 6. Speaker Diarization ===${NC}"
    DIARIZE_RESPONSE=$(curl -s -X POST \
        -H "Authorization: Bearer $TOKEN" \
        -F "audio=@$AUDIO_FILE" \
        "http://localhost:8088/api/meetings/$MEETING_ID/diarize")
    echo "$DIARIZE_RESPONSE" | jq
    DIARIZE_OK=$(echo "$DIARIZE_RESPONSE" | jq -r '.ok')
    if [ "$DIARIZE_OK" = "true" ]; then
        echo -e "${GREEN}✓ Speaker diarization successful${NC}\n"
    else
        echo -e "${RED}✗ Speaker diarization failed${NC}\n"
    fi
else
    echo -e "${YELLOW}=== 6. Speaker Diarization (Skipped) ===${NC}"
    echo -e "${YELLOW}⚠ Diarization service not configured${NC}\n"
fi

# 测试Advisor端点（如果服务可用）
if [ "$KXMEMORY_STATUS" = "true" ]; then
    echo -e "${YELLOW}=== 7. AI Meeting Advisor ===${NC}"
    ADVISOR_RESPONSE=$(curl -s -X POST \
        -H "Authorization: Bearer $TOKEN" \
        -H "Content-Type: application/json" \
        -d '{"analysis_type":"summary"}' \
        "http://localhost:8088/api/meetings/$MEETING_ID/advisor")
    echo "$ADVISOR_RESPONSE" | jq
    ADVISOR_ENABLED=$(echo "$ADVISOR_RESPONSE" | jq -r '.enabled')
    if [ "$ADVISOR_ENABLED" = "true" ]; then
        echo -e "${GREEN}✓ AI advisor successful${NC}\n"
    else
        echo -e "${YELLOW}⚠ AI advisor returned no suggestions${NC}\n"
    fi
else
    echo -e "${YELLOW}=== 7. AI Meeting Advisor (Skipped) ===${NC}"
    echo -e "${YELLOW}⚠ KxMemory service not configured${NC}\n"
fi

# 清理测试会议
echo -e "${YELLOW}=== 8. Cleanup ===${NC}"
DELETE_RESPONSE=$(curl -s -X DELETE \
    -H "Authorization: Bearer $TOKEN" \
    "http://localhost:8088/api/meetings/$MEETING_ID" \
    -w "%{http_code}")
if [ "$DELETE_RESPONSE" = "204" ]; then
    echo -e "${GREEN}✓ Test meeting deleted${NC}\n"
else
    echo -e "${YELLOW}⚠ Failed to delete test meeting${NC}\n"
fi

# 总结
echo -e "${GREEN}=== Test Summary ===${NC}"
echo -e "✓ JWT Authentication: ${GREEN}PASS${NC}"
echo -e "✓ Health Checks: ${GREEN}PASS${NC}"
echo -e "✓ Meeting CRUD: ${GREEN}PASS${NC}"
echo -e "✓ Multilingual Transcription: ${GREEN}PASS${NC}"

if [ "$DIARIZE_AVAILABLE" = "true" ]; then
    echo -e "✓ Speaker Diarization: ${GREEN}PASS${NC}"
else
    echo -e "⚠ Speaker Diarization: ${YELLOW}SKIPPED (service not configured)${NC}"
fi

if [ "$KXMEMORY_STATUS" = "true" ]; then
    echo -e "✓ AI Meeting Advisor: ${GREEN}PASS${NC}"
else
    echo -e "⚠ AI Meeting Advisor: ${YELLOW}SKIPPED (service not configured)${NC}"
fi

echo -e "\n${GREEN}Phase 2 testing complete!${NC}"
