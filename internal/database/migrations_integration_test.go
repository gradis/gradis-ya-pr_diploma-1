package database

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gradis/ya-pr_diploma-1/migrations"
	"github.com/jackc/pgx/v5"
)

func TestOrderTypesMigrationRoundTrip(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URI")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URI is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	schema := pgx.Identifier{fmt.Sprintf("migration_test_%d", time.Now().UnixNano())}.Sanitize()
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, _ = conn.Exec(cleanup, "ROLLBACK")
		if _, err := conn.Exec(cleanup, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	}()
	if _, err := conn.Exec(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	apply := func(name string) {
		t.Helper()
		sql, err := migrations.Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	apply("000001_init.up.sql")
	if _, err := conn.Exec(ctx, `INSERT INTO users (login, password_hash) VALUES ($1, 'hash')`, strings.Repeat("я", 128)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO orders (number,user_id,status,accrual_cents) VALUES ('123',1,'PROCESSED',500)`); err != nil {
		t.Fatal(err)
	}
	assertData := func(wantType string) {
		t.Helper()
		var status, typ string
		var amount int64
		if err := conn.QueryRow(ctx, `SELECT status::text, pg_typeof(status)::text, accrual_cents FROM orders WHERE number='123'`).Scan(&status, &typ, &amount); err != nil {
			t.Fatal(err)
		}
		if status != "PROCESSED" || typ != wantType || amount != 500 {
			t.Fatalf("data changed: %s %s %d", status, typ, amount)
		}
	}
	apply("000002_order_types.up.sql")
	assertData("order_status")
	for _, query := range []string{
		`INSERT INTO orders(number,user_id,status) VALUES ('456',1,'UNKNOWN')`,
		`INSERT INTO orders(number,user_id) VALUES ('abc',1)`,
		`INSERT INTO orders(number,user_id) VALUES (repeat('1',257),1)`,
		`INSERT INTO users(login,password_hash) VALUES (repeat('я',129),'hash')`,
	} {
		if _, err := conn.Exec(ctx, query); err == nil {
			t.Fatalf("invalid data accepted: %s", query)
		}
	}
	if _, err := conn.Exec(ctx, `INSERT INTO orders(number,user_id) VALUES ('456',1)`); err != nil {
		t.Fatal(err)
	}
	var defaultStatus string
	if err := conn.QueryRow(ctx, `SELECT status FROM orders WHERE number='456'`).Scan(&defaultStatus); err != nil || defaultStatus != "NEW" {
		t.Fatalf("default: %s, %v", defaultStatus, err)
	}
	var exists bool
	if err := conn.QueryRow(ctx, `SELECT to_regclass('orders_pending_idx') IS NOT NULL`).Scan(&exists); err != nil || !exists {
		t.Fatalf("pending index missing: %v", err)
	}
	apply("000002_order_types.down.sql")
	assertData("text")
	apply("000002_order_types.up.sql")
	assertData("order_status")
}
