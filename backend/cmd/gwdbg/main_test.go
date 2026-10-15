package main

// ⚠ §692 判据：`cmd/gwdbg` 的查询目标必须与后端**同一口径**。
//
// 缺陷原文：本探针自己写了一遍 DSN/schema 解析，且 (①) 多了一个后端从不使用的
// 写死默认 DSN、(②) 不认后端明确支持的 `DATABASE_URL` ⇒ 在用 DATABASE_URL
// 部署的实例上会去连**另一个库**，对「POST 到底落没落库」给出**假阴性**。

import (
	"strings"
	"testing"
)

func TestResolvePGTarget_FollowsBackendPrecedence(t *testing.T) {
	cases := []struct {
		name        string
		dsnEnv      string
		databaseURL string
		schemaEnv   string
		wantDSN     string
		wantSchema  string
		wantErr     bool
	}{
		{
			name:    "两者都未设置 ⇒ 报错，绝不发明默认 DSN",
			wantErr: true,
		},
		{
			name:        "只给 DATABASE_URL（后端支持的部署方式）⇒ 必须认",
			databaseURL: "postgres://u@h:5432/app", wantDSN: "postgres://u@h:5432/app",
			wantSchema: "opencode_pocket",
		},
		{
			name:   "两者都给 ⇒ POCKET_POSTGRES_DSN 优先（与 getFirstEnv 同序）",
			dsnEnv: "postgres://u@h:5432/primary", databaseURL: "postgres://u@h:5432/fallback",
			wantDSN: "postgres://u@h:5432/primary", wantSchema: "opencode_pocket",
		},
		{
			name:   "schema 跟随 POCKET_PG_SCHEMA（隔离后端场景）",
			dsnEnv: "postgres://u@h:5432/app", schemaEnv: "opencode_pocket_verify",
			wantDSN: "postgres://u@h:5432/app", wantSchema: "opencode_pocket_verify",
		},
		{
			name:   "纯空白 DSN 等同未设置（不得当成有效目标去连）",
			dsnEnv: "   ", wantErr: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, k := range []string{"POCKET_POSTGRES_DSN", "DATABASE_URL", "POCKET_PG_SCHEMA"} {
				t.Setenv(k, "")
			}
			if c.dsnEnv != "" {
				t.Setenv("POCKET_POSTGRES_DSN", c.dsnEnv)
			}
			if c.databaseURL != "" {
				t.Setenv("DATABASE_URL", c.databaseURL)
			}
			if c.schemaEnv != "" {
				t.Setenv("POCKET_PG_SCHEMA", c.schemaEnv)
			}

			dsn, schema, err := resolvePGTarget()
			if c.wantErr {
				if err == nil {
					t.Fatalf("期望报错却拿到了 dsn=%q —— 探针又在自己发明默认目标", dsn)
				}
				// 契约：没有目标就没有 schema 一说，两值都返回零值，
				// 调用方 log.Fatal 退出，不允许拿半个目标去连。
				if dsn != "" || schema != "" {
					t.Errorf("报错时 dsn/schema 都应为零值，实际 %q/%q", dsn, schema)
				}
			} else if err != nil {
				t.Fatalf("期望成功却报错：%v", err)
			} else if dsn != c.wantDSN {
				t.Errorf("dsn = %q，期望 %q", dsn, c.wantDSN)
			}
			if schema != c.wantSchema {
				t.Errorf("schema = %q，期望 %q", schema, c.wantSchema)
			}
			// 负控：写死的本机默认 DSN 一旦被重新引入，这里转红。
			if strings.Contains(dsn, "127.0.0.1:5432") {
				t.Errorf("不得回落到写死的本机 DSN：%q", dsn)
			}
		})
	}
}
