// 直查 PG 的 llm_gateway_configs，验证 POST 是否真的落库
package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/halfking/pocket-opencode/backend/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
)

// resolvePGTarget 返回要查询的 PG 目标，口径**完全复用 config**（§692）。
//
// ⚠ 此前本文件自己写了一遍，且与后端有两处不一致：
//
//	① 写死了 `postgres://postgres@127.0.0.1:5432/postgres` 作为兜底 DSN——
//	   后端从不用它（`Config.PostgresDSN` 默认空；生产环境 `Validate()` 直接
//	   拒绝空 DSN，开发环境则根本不连 PG）。
//	② 只认 `POCKET_POSTGRES_DSN`，不认后端明确支持的 `DATABASE_URL`。
//
// 两者合起来的后果：在用 `DATABASE_URL` 部署的实例上，本探针会连到**另一个库**，
// 然后对「POST 到底落没落库」报出**假阴性**——而这正是这个探针存在的目的。
// 现在缺配置就报错退出（与兄弟探针 invoiceprobe / spamdryprobe 同一形状），
// 不再自己发明一个「看起来能连」的默认值。
func resolvePGTarget() (dsn, schema string, err error) {
	dsn, schema = config.PGTargetFromEnv()
	if strings.TrimSpace(dsn) == "" {
		return "", "", fmt.Errorf("未配置 PG 连接：请设置 POCKET_POSTGRES_DSN 或 DATABASE_URL（与后端同一个来源）")
	}
	return dsn, schema, nil
}

func main() {
	dsn, schema, err := resolvePGTarget()
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	rows, err := pool.Query(ctx, fmt.Sprintf(`
		SELECT workspace_id, base_url, models::text, format, is_active, created_at, updated_at,
		       left(coalesce(api_key_encrypted,''), 12) AS key_prefix
		FROM %s.llm_gateway_configs
		ORDER BY workspace_id, created_at DESC`, schema))
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()
	fmt.Printf("%-18s %-30s %-8s %-12s %-20s %s\n", "WORKSPACE", "BASE_URL", "ACTIVE", "KEY_PREFIX", "CREATED_AT", "MODELS(n)")
	for rows.Next() {
		var ws, base, models, format, kp string
		var active bool
		var created, updated any
		if err := rows.Scan(&ws, &base, &models, &format, &active, &created, &updated, &kp); err != nil {
			log.Fatal(err)
		}
		n := 0
		fmt.Sscanf(models, "", &n)
		fmt.Printf("%-18s %-30s %-8v %-12s %-20v models=%d fmt=%s\n", ws, base, active, kp, created, len(models), format)
	}
	if err := rows.Err(); err != nil {
		log.Fatal(err)
	}

	// user settings：effectiveGatewayState 会用它覆盖工作区快照，
	// 之前的 /api/user-settings?namespace=... 明显忽略了过滤条件，必须直查表。
	fmt.Println("\n=== user_settings（llm_gateway）===")
	urows, err := pool.Query(ctx, fmt.Sprintf(`
		SELECT user_id, workspace_id, namespace, id, payload::text, updated_at
		FROM %s.user_settings
		WHERE namespace = 'llm_gateway'
		ORDER BY updated_at DESC`, schema))
	if err != nil {
		fmt.Println("查询失败: " + err.Error())
		return
	}
	defer urows.Close()
	n := 0
	for urows.Next() {
		var uid, ws, ns, id, payload string
		var updated any
		if err := urows.Scan(&uid, &ws, &ns, &id, &payload, &updated); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("user=%s ws=%s id=%s updated=%v\n  payload=%s\n", uid, ws, id, updated, payload)
		n++
	}
	if n == 0 {
		fmt.Println("(无 llm_gateway 用户设置)")
	}
}
