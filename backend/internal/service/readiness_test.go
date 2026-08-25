package service

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestReadinessAllProbesOK(t *testing.T) {
	svc := NewReadinessService(time.Second, map[string]Probe{
		"db": func(context.Context) error { return nil },
		"ml": func(context.Context) error { return nil },
	})

	ok, checks := svc.Check(context.Background())

	if !ok {
		t.Fatalf("ok = false, ожидалось true при живых пробах: %v", checks)
	}
	if checks["db"] != "ok" || checks["ml"] != "ok" {
		t.Fatalf("checks = %v, ожидалось ok у обеих проб", checks)
	}
}

// Упавшая проба обязана назвать причину: readiness без причины отказа —
// та же заглушка, только с кодом 503.
func TestReadinessReportsFailingProbeByName(t *testing.T) {
	svc := NewReadinessService(time.Second, map[string]Probe{
		"db": func(context.Context) error { return nil },
		"ml": func(context.Context) error { return errors.New("connection refused") },
	})

	ok, checks := svc.Check(context.Background())

	if ok {
		t.Fatal("ok = true, ожидалось false при упавшей пробе")
	}
	if checks["db"] != "ok" {
		t.Fatalf("checks[db] = %q, ожидалось ok", checks["db"])
	}
	if checks["ml"] == "ok" || checks["ml"] == "" {
		t.Fatalf("checks[ml] = %q, ожидалась причина отказа", checks["ml"])
	}
}

// Зависшая проба не должна вешать сам readiness: таймаут — это тоже ответ.
func TestReadinessTimesOutSlowProbe(t *testing.T) {
	svc := NewReadinessService(50*time.Millisecond, map[string]Probe{
		"ml": func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
	})

	started := time.Now()
	ok, checks := svc.Check(context.Background())

	if ok {
		t.Fatal("ok = true, ожидалось false по таймауту")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Check занял %v — таймаут не сработал", elapsed)
	}
	if checks["ml"] == "ok" {
		t.Fatalf("checks[ml] = ok, ожидался отказ по таймауту")
	}
}
