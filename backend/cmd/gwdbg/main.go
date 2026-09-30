// 直查 PG 的 llm_gateway_configs，验证 POST 是否真的落库
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	dsn := os.Getenv("POCKET_POSTGRES_DSN")
	if dsn == "" {
		dsn = "postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable"
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	rows, err := pool.Query(ctx, `
		SELECT workspace_id, base_url, models::text, format, is_active, created_at, updated_at,
		       left(coalesce(api_key_encrypted,''), 12) AS key_prefix
		FROM opencode_pocket.llm_gateway_configs
		ORDER BY workspace_id, created_at DESC`)
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
	urows, err := pool.Query(ctx, `
		SELECT user_id, workspace_id, namespace, id, payload::text, updated_at
		FROM opencode_pocket.user_settings
		WHERE namespace = 'llm_gateway'
		ORDER BY updated_at DESC`)
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
