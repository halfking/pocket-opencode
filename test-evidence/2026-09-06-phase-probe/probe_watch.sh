#!/bin/zsh
# 串行守候探测 v2：每轮 glm-5.2 →（隔 4s）→ auto 双发，单连接串行小间隔规避 429。
# 背景：preferredModels=[] 时 auto 只解析出 catalog 首个 abab5.5-chat 即席最终候选，
# 单探 auto 会在「仅 glm 恢复」相位假阴性。任一发 200 即退出码 0；无恢复 40 分钟退出码 3。
cd "$(dirname "$0")"
TOKEN=$(cat .token)
probe() {
  curl -s -m 45 -o /tmp/probe-watch-last.json -w "%{http_code}" \
    -X POST http://127.0.0.1:8088/api/llm/chat \
    -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
    -d "{\"model\":\"$1\",\"messages\":[{\"role\":\"user\",\"content\":\"reply with one word pong\"}]}"
}
for i in $(seq 1 8); do
  S=$(date "+%H:%M:%S")
  C1=$(probe glm-5.2); B1=$(head -c 120 /tmp/probe-watch-last.json | tr '\n' ' ')
  sleep 4
  C2=$(probe auto);   B2=$(head -c 120 /tmp/probe-watch-last.json | tr '\n' ' ')
  echo "[$S] try#$i glm=$C1 auto=$C2 | $B1 | $B2" | tee -a probe-watch.txt
  if [ "$C1" = "200" ] || [ "$C2" = "200" ]; then
    echo "[$S] UPSTREAM RECOVERED (glm=$C1 auto=$C2)" | tee -a probe-watch.txt
    exit 0
  fi
  [ "$i" -lt 8 ] && sleep 266
done
echo "[$(date "+%H:%M:%S")] still-dead after watch window" | tee -a probe-watch.txt
exit 3
