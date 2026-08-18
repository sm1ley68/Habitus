package db

import (
	"testing"
	"time"
)

func TestPoolConfigBoundsConnectionsAndRecyclesThem(t *testing.T) {
	// Дефолты pgxpool: MaxConns = число ядер машины, соединения живут вечно.
	// Под SSE соединение держится дольше обычного запроса, а вечные соединения
	// переживают рестарт Postgres и отдают битые хендлы.
	cfg, err := PoolConfig("postgresql://habitus:habitus@localhost:5544/habitus", 8)
	if err != nil {
		t.Fatalf("PoolConfig() error = %v", err)
	}

	if cfg.MaxConns != 8 {
		t.Fatalf("MaxConns = %d; want 8", cfg.MaxConns)
	}
	if cfg.MaxConnLifetime == 0 || cfg.MaxConnLifetime > time.Hour {
		t.Fatalf("MaxConnLifetime = %v; соединения должны переоткрываться", cfg.MaxConnLifetime)
	}
	if cfg.MaxConnIdleTime == 0 {
		t.Fatalf("MaxConnIdleTime = 0; простаивающие соединения должны закрываться")
	}
	if cfg.HealthCheckPeriod == 0 {
		t.Fatalf("HealthCheckPeriod = 0; битые соединения должны отсеиваться пулом")
	}
}

func TestPoolConfigIgnoresNonPositiveMaxConns(t *testing.T) {
	// MaxConns <= 0 роняет pgxpool.NewWithConfig — на кривой переменной
	// окружения шлюз должен подняться на дефолте, а не упасть.
	cfg, err := PoolConfig("postgresql://habitus:habitus@localhost:5544/habitus", 0)
	if err != nil {
		t.Fatalf("PoolConfig() error = %v", err)
	}
	if cfg.MaxConns <= 0 {
		t.Fatalf("MaxConns = %d; want положительное значение", cfg.MaxConns)
	}
}

func TestPoolConfigRejectsBrokenDSN(t *testing.T) {
	if _, err := PoolConfig("://не-dsn", 8); err == nil {
		t.Fatal("PoolConfig() error = nil; битый DSN должен быть ошибкой")
	}
}
