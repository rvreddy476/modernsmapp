// Command m7dbsetup creates a fresh, explicitly named Module 7 proof database.
// It never drops or overwrites a database and accepts only the m7*_codex_*
// namespace, making accidental use against application data fail safe.
package main

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
)

var safeName = regexp.MustCompile(`^m7[a-z0-9_]*_codex_[a-z0-9_]+$`)

func main() {
	adminDSN := os.Getenv("M7_POSTGRES_ADMIN_DSN")
	target := os.Getenv("M7_DATABASE_NAME")
	if adminDSN == "" || !safeName.MatchString(target) {
		fmt.Fprintln(os.Stderr, "M7_POSTGRES_ADMIN_DSN and a safe m7*_codex_* M7_DATABASE_NAME are required")
		os.Exit(2)
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		panic(err)
	}
	defer conn.Close(ctx)
	var exists bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)`, target).Scan(&exists); err != nil {
		panic(err)
	}
	if exists {
		fmt.Fprintln(os.Stderr, "refusing to overwrite existing database", target)
		os.Exit(3)
	}
	quoted := `"` + strings.ReplaceAll(target, `"`, `""`) + `"`
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		panic(err)
	}
	fmt.Println("created", target)
}
