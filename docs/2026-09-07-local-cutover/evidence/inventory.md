# inventory 2026-09-07T23:27:09+08:00

## docker (name image ports status)
llm-gateway-local-8782	kx-llm-gateway-local:2.5.3.2053	127.0.0.1:8782->8782/tcp	Up 5 hours
llm-gateway-local-8781	07172d4c56bb	127.0.0.1:8781->8781/tcp	Up 5 hours
llm-gateway-pg	kx-citus-pg17:offline-arm64	127.0.0.1:5432->5432/tcp	Up 5 hours (healthy)
ai-session-manager-asm-1	ai-session-manager:latest	8782/tcp	Up 10 hours
nbjl-app	nbjl-app:minimal-deploy	127.0.0.1:18081->8080/tcp	Up 12 hours (healthy)
nb-mac-01	netbirdio/netbird:0.78.1		Up 13 hours
ai-native-llm-mock	python:3.12-alpine		Up 13 hours (unhealthy)
ai-native-prompt-optimizer	ai-native/prompt-optimizer:dev	0.0.0.0:18090->8090/tcp, [::]:18090->8090/tcp	Up 13 hours (healthy)
ai-native-llm-gateway	ai-native/llm-gateway:dev	0.0.0.0:8080->8080/tcp, [::]:8080->8080/tcp	Up 13 hours (healthy)
ai-native-acc	ai-native/acc:dev	0.0.0.0:3000->3000/tcp, [::]:3000->3000/tcp	Up 13 hours (healthy)
ai-native-agent-companion	ai-native/agent-companion:dev	0.0.0.0:28082->8082/tcp, [::]:28082->8082/tcp	Up 13 hours
ai-native-redis	redis:7-alpine	0.0.0.0:16380->6379/tcp, [::]:16380->6379/tcp	Up 13 hours (healthy)
ai-native-qdrant	qdrant/qdrant:latest	0.0.0.0:16333->6333/tcp, [::]:16333->6333/tcp	Up 13 hours
agent-companion-local	agent-companion-local:dev	0.0.0.0:28080->8080/tcp, [::]:28080->8080/tcp	Up 13 hours
nbjl-mysql	mysql:8.4	127.0.0.1:13306->3306/tcp	Up 13 hours
pms-nacos	nacos/nacos-server:v2.2.3	0.0.0.0:8848->8848/tcp, [::]:8848->8848/tcp, 0.0.0.0:9848->9848/tcp, [::]:9848->9848/tcp	Up 5 hours
nbjl-redis	redis:7-alpine	6379/tcp	Up 13 hours
redclaw-local-stack-redclaw-admin-1	redclaw-admin:local	0.0.0.0:27093->8093/tcp, [::]:27093->8093/tcp, 0.0.0.0:29193->9090/tcp, [::]:29193->9090/tcp	Up 13 hours
redclaw-local-stack-redclaw-gateway-1	redclaw-gateway:local	0.0.0.0:27081->8080/tcp, [::]:27081->8080/tcp, 0.0.0.0:29181->9090/tcp, [::]:29181->9090/tcp	Up 13 hours
acc-blue	acc:1.1.0.32	4100/tcp	Up 13 hours (healthy)
redclaw-local-stack-redclaw-worker-1	redclaw-worker:local		Up 13 hours
redclaw-local-stack-redclaw-facade-1	redclaw-facade:local	0.0.0.0:27001->17000/tcp, [::]:27001->17000/tcp	Up 13 hours
redclaw-local-stack-redclaw-dal-1	redclaw-dal:local	0.0.0.0:27080->8080/tcp, [::]:27080->8080/tcp, 0.0.0.0:29180->9090/tcp, [::]:29180->9090/tcp	Up 13 hours (healthy)
redclaw-local-stack-redclaw-authagent-1	395dd5548866	0.0.0.0:27092->8092/tcp, [::]:27092->8092/tcp, 0.0.0.0:29192->9090/tcp, [::]:29192->9090/tcp	Up 13 hours
redclaw-local-stack-redclaw-casdoor-1	casbin/casdoor:v2.63.0	0.0.0.0:28000->8000/tcp, [::]:28000->8000/tcp	Up 13 hours
redclaw-local-stack-redclaw-api-1	12d5d01199ff	0.0.0.0:27000->8080/tcp, [::]:27000->8080/tcp, 0.0.0.0:29100->9090/tcp, [::]:29100->9090/tcp	Up 13 hours
redclaw-local-stack-redclaw-orchestrator-1	267fa5ebcf60	0.0.0.0:27090->8090/tcp, [::]:27090->8090/tcp, 0.0.0.0:29190->9090/tcp, [::]:29190->9090/tcp	Up 13 hours
vibecoding-gitea	gitea/gitea:1.21	0.0.0.0:2222->22/tcp, [::]:2222->22/tcp, 0.0.0.0:3030->3000/tcp, [::]:3030->3000/tcp	Up 13 hours (healthy)
redclaw-llm-router-local-llm-router-sidecar-1	registry.kxpms.cn/redclaw/llm-router	127.0.0.1:18000->8000/tcp	Up 13 hours (healthy)
redclaw-local-stack-redclaw-agentcontainer-1	f97db63c976b	0.0.0.0:27091->8091/tcp, [::]:27091->8091/tcp, 0.0.0.0:29191->9090/tcp, [::]:29191->9090/tcp	Up 13 hours
redclaw-local-stack-redclaw-casdoor-db-1	postgres:16-alpine	5432/tcp	Up 13 hours (healthy)
kxmemory-llm-mock	python:3.12-alpine	0.0.0.0:18082->18082/tcp, [::]:18082->18082/tcp	Up 13 hours
memora-stack-qdrant	kx-qdrant:v1-arm64	0.0.0.0:26333->6333/tcp, [::]:26333->6333/tcp, 0.0.0.0:26334->6334/tcp, [::]:26334->6334/tcp	Up 13 hours (healthy)
memora-stack-minio	kx-minio:v1-arm64-fixed	0.0.0.0:29000->9000/tcp, [::]:29000->9000/tcp, 0.0.0.0:29001->9001/tcp, [::]:29001->9001/tcp	Up 13 hours (healthy)
k3d-llm-gateway-observe-serverlb	ghcr.io/k3d-io/k3d-proxy:5.9.0	127.0.0.1:6550->6443/tcp	Up 13 hours
k3d-llm-gateway-observe-agent-0	rancher/k3s:v1.35.5-k3s1		Up 13 hours
k3d-llm-gateway-observe-server-0	rancher/k3s:v1.35.5-k3s1		Up 13 hours

## docker exited acc/memora/pocket
nb-mac-01	Up 13 hours	
ai-native-acc	Up 13 hours (healthy)	0.0.0.0:3000->3000/tcp, [::]:3000->3000/tcp
ai-native-agent-companion	Up 13 hours	0.0.0.0:28082->8082/tcp, [::]:28082->8082/tcp
agent-companion-local	Up 13 hours	0.0.0.0:28080->8080/tcp, [::]:28080->8080/tcp
acc-blue	Up 13 hours (healthy)	4100/tcp
acc-nginx	Exited (0) 13 hours ago	
memora-redis	Exited (0) 13 hours ago	
kxmemory-llm-mock	Up 13 hours	0.0.0.0:18082->18082/tcp, [::]:18082->18082/tcp
memora-stack-qdrant	Up 13 hours (healthy)	0.0.0.0:26333->6333/tcp, [::]:26333->6333/tcp, 0.0.0.0:26334->6334/tcp, [::]:26334->6334/tcp
memora-stack-minio	Up 13 hours (healthy)	0.0.0.0:29000->9000/tcp, [::]:29000->9000/tcp, 0.0.0.0:29001->9001/tcp, [::]:29001->9001/tcp

## listen ports
4096: 4100: 4101: 4175: 8080: com.docke 2780
8081: node 98256
8088: 8090: pocketd-f 15693
8782: com.docke 2780
27001: com.docke 2780
27081: com.docke 2780
28080: com.docke 2780
28082: com.docke 2780

## netbird container
OS: linux/arm64
Daemon version: 0.78.1
CLI version: 0.78.1
Profile: default
Management: Disconnected, reason: rpc error: code = FailedPrecondition desc = failed connecting to Management Service : create connection: dial context: context deadline exceeded
Signal: Disconnected
Relays: 0/0 Available
Nameservers: 0/0 Available
FQDN: 
NetBird IP: N/A
Interface type: N/A
Wireguard port: N/A
Quantum resistance: false
Lazy connection: false
SSH Server: Disabled
Networks: -
Peers count: 0/0 Connected

## pocketd process
40305 /bin/zsh -c builtin
15693 /Users/xutaohuang/workspace/ai-native-tools/openpocket/backend/bin/pocketd-firstinstall  

## adb
List of devices attached
10AF6H1MLM003HF        device usb:1048576X product:PD2436 model:V2436A device:PD2436 transport_id:2


## pocket.itestu.cn
115.29.212.252
Connecting to 115.29.212.252
CONNECTION ESTABLISHED
Protocol version: TLSv1.3
Ciphersuite: TLS_AES_256_GCM_SHA384
Peer certificate: CN=kxpms.cn
Hash used: SHA256
Signature type: rsa_pss_rsae_sha256
Verification: OK
Peer Temp Key: X25519, 253 bits
DONE

## companion env (no secrets)
ACC_URL=http://acc-nginx:80
AC_HEARTBEAT_MS=30000
MEMORA_BASE_URL=http://kxmemory-go-local:8080
ACC_BASE_URL=http://acc-go-local:4101
AC_HOST_ID=host-local
AC_API_LISTEN=0.0.0.0:8080
AC_RUNTIME_ID=local-runtime-dev
AC_RUNTIME_SCAN_INTERVAL_MS=30000
AC_CWD_BASE=/data/workspace
AC_ALLOWLIST_PATH=/config/allowlist.json
AC_TENANT_ID=dev-tenant
LLM_GATEWAY_URL=http://llm-gateway-local-8782:8782
AC_COMMAND_RUN_ID=local-dev-run
AC_RUNTIME_SCAN_ENABLED=true

## networks
acp-network-local=172.30.0.2 shared-infra=172.18.0.2 
acc-blue shared-infra=172.18.0.7 
acc-nginx status=exited
