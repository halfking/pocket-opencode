#!/bin/zsh
# 串行守候探测 v3：每轮 glm-5.2 →(4s)→ auto →(4s)→ gpt-5.6-terra 三发，
# 单连接串行小间隔规避 429。背景：09-06 13:48~13:55 terra 短恢复窗后
# 14:03 转灭，上游处高漂移相；glm 仍为主判据，terra 为次判据。
cd "$(dirname "$0")"
TOKEN=$(cat .token)
probe() {
  curl -s -m 45 -o /tmp/probe-watch-last.json -w "%{http_code}" \
    -X POST http://127.0.0.1:8088/api/llm/chat \
    -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
    -d "{\"model\":\"$1\",\"messages\":[{\"role\":\"user\",\"content\":\"reply with one word pong\"}]}"
}
for i in $(seq 1 4); do
  S=$(date "+%H:%M:%S")
  C1=$(probe glm-5.2); B1=$(head -c 100 /tmp/probe-watch-last.json | tr '\n' ' ')
  sleep 4
  C2=$(probe auto);    B2=$(head -c 100 /tmp/probe-watch-last.json | tr '\n' ' ')
  sleep 4
  C3=$(probe gpt-5.6-terra); B3=$(head -c 100 /tmp/probe-watch-last.json | tr '\n' ' ')
  echo "[$S] try#$i glm=$C1 auto=$C2 terra=$C3 | $B1 | $B3" | tee -a probe-watch.txt
  if [ "$C1" = "200" ] || [ "$C3" = "200" ]; then
    echo "[$S] UPSTREAM RECOVERED (glm=$C1 terra=$C3)" | tee -a probe-watch.txt
    exit 0
  fi
  [ "$i" -lt 8 ] && sleep 258
done
echo "[$(date "+%H:%M:%S")] still-dead after watch window" | tee -a probe-watch.txt
exit 3
