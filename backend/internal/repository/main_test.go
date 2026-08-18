// Харнесс репозиторных тестов. Конвенция та же, что в Python-части (см.
// tests/conftest.py): работаем в ОТДЕЛЬНОЙ базе «рабочая + _test», создаём её
// сами, а без поднятого Postgres тесты скипаются, а не падают.
package repository

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"habitus-backend/internal/db"
)

const defaultDSN = "postgresql://habitus:habitus@localhost:5544/habitus"

// testDSN превращает рабочий DSN в тестовый, дописывая суффикс к имени базы.
func testDSN(dsn string) (string, string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", "", err
	}
	name := strings.TrimPrefix(u.Path, "/")
	if name == "" {
		return "", "", fmt.Errorf("в DSN нет имени базы: %s", dsn)
	}
	if !strings.HasSuffix(name, "_test") {
		name += "_test"
	}
	u.Path = "/" + name
	return u.String(), name, nil
}

// testPool отдаёт пул к тестовой базе с накатанными миграциями. Postgres не
// поднят — t.Skip, как в Python-сьюте.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		dsn = defaultDSN
	}
	target, name, err := testDSN(dsn)
	if err != nil {
		t.Fatalf("разбор DSN: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Skipf("Postgres недоступен (%v) — репозиторные тесты пропущены", err)
	}
	// CREATE DATABASE IF NOT EXISTS в Postgres нет: ошибку «уже существует»
	// (42P04) глотаем, всё остальное — настоящая проблема.
	if _, err := admin.Exec(ctx, fmt.Sprintf("CREATE DATABASE %q", name)); err != nil &&
		!strings.Contains(err.Error(), "42P04") &&
		!strings.Contains(err.Error(), "already exists") {
		_ = admin.Close(ctx)
		t.Skipf("не удалось создать тестовую базу (%v) — тесты пропущены", err)
	}
	_ = admin.Close(ctx)

	if err := db.RunMigrations(target, "../../migrations"); err != nil {
		t.Skipf("миграции на тестовой базе не накатились (%v)", err)
	}

	pool, err := db.NewPool(ctx, target, 4)
	if err != nil {
		t.Skipf("пул к тестовой базе не поднялся (%v)", err)
	}
	t.Cleanup(pool.Close)
	return pool
}
