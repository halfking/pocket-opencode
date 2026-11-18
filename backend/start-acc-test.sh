#!/bin/bash
set -e
export POCKET_DEV_AUTH=true
export POCKET_AUTH_LEGACY_ONLY=true
export POCKET_JWT_SECRET=test-secret-key-for-phase7-validation
export POCKET_HTTP_PORT=8088
export POCKET_DB_PATH=./data/pocket.sqlite
export POCKET_MEMORA_BASE_URL=http://192.168.31.211:8091
export POCKET_MEMORA_JWT_SECRET=#POCKET_MEMORA_API_KEY=
export POCKET_MEMORA_TENANT_ID=default
export POCKET_MEMORA_PROJECT_ID=default
export POCKET_MEMORA_USER_ID=pocketd
export POCKET_MEMORA_V2_JWT_ISSUER=pocketd
export POCKET_MEMORA_V2_JWT_AUDIENCE=memora-api
export POCKET_MEMORA_V2_JWT_SCOPE="notebook.read notebook.write"
export POCKET_ACC_RUNTIME_URL=http://127.0.0.1:4101
export POCKET_ACC_CONTROL_SECRET=YbbJOnB0U8q1yQQTnsdZNxAHuEZm7jxdH1ztIuNO
export POCKET_AUTH_USER=admin
export POCKET_AUTH_PASS="Veritrans&9527"
exec ./pocketd
