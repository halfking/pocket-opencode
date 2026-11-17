package main

import (
	"context"
	"fmt"
	"os"

	"github.com/halfking/pocket-opencode/backend/internal/auth"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	email := os.Getenv("TEST_REGISTER_EMAIL")
	dsn := os.Getenv("POCKET_POSTGRES_DSN")
	schema := os.Getenv("POCKET_PG_SCHEMA")
	if dsn == "" {
		panic("POCKET_POSTGRES_DSN is required")
	}
	if schema != "" {
		dsn += "&search_path=" + schema
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		panic(err)
	}
	defer pool.Close()
	store, err := auth.NewCodeStore(pool)
	if err != nil {
		panic(err)
	}
	code, ttl, err := store.Generate(context.Background(), email, auth.PurposeRegister, "127.0.0.1")
	if err != nil {
		panic(err)
	}
	fmt.Printf("%s %d\n", code, ttl)
}
