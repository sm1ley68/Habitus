package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Границы пула. Дефолты pgxpool (MaxConns = число ядер, соединения живут
// вечно) для публичного шлюза не годятся: под SSE соединение держится дольше
// обычного запроса, а вечные соединения переживают рестарт Postgres и потом
// отдают битые хендлы.
const (
	defaultMaxConns   = 20
	maxConnLifetime   = 30 * time.Minute
	maxConnIdleTime   = 5 * time.Minute
	healthCheckPeriod = 1 * time.Minute
)

// PoolConfig собирает конфигурацию пула. maxConns <= 0 — не ошибка, а
// невнятная переменная окружения: берём дефолт, потому что pgxpool на
// неположительном значении отказывается стартовать вовсе.
func PoolConfig(dsn string, maxConns int) (*pgxpool.Config, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	if maxConns <= 0 {
		maxConns = defaultMaxConns
	}
	cfg.MaxConns = int32(maxConns)
	cfg.MaxConnLifetime = maxConnLifetime
	cfg.MaxConnIdleTime = maxConnIdleTime
	cfg.HealthCheckPeriod = healthCheckPeriod
	return cfg, nil
}

func NewPool(ctx context.Context, dsn string, maxConns int) (*pgxpool.Pool, error) {
	cfg, err := PoolConfig(dsn, maxConns)
	if err != nil {
		return nil, err
	}
	return pgxpool.NewWithConfig(ctx, cfg)
}
