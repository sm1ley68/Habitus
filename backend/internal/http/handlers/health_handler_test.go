package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"habitus-backend/internal/service"
)

func newHealthApp(probes map[string]service.Probe) *fiber.App {
	app := fiber.New()
	h := NewHealthHandler(service.NewReadinessService(time.Second, probes))
	app.Get("/health", h.Live)
	app.Get("/health/ready", h.Ready)
	return app
}

// Liveness намеренно не зависит от БД и ML: его задача — сказать, что процесс
// жив, а не что он полезен. Иначе моргнувший Postgres уронил бы контейнер.
func TestLiveIsIndependentOfDependencies(t *testing.T) {
	app := newHealthApp(map[string]service.Probe{
		"db": func(context.Context) error { return errors.New("dead") },
	})

	resp, err := app.Test(httptest.NewRequest("GET", "/health", nil))
	if err != nil {
		t.Fatalf("запрос: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("статус = %d, ожидался 200", resp.StatusCode)
	}
}

func TestReadyReturns200WhenDependenciesAlive(t *testing.T) {
	app := newHealthApp(map[string]service.Probe{
		"db": func(context.Context) error { return nil },
		"ml": func(context.Context) error { return nil },
	})

	resp, err := app.Test(httptest.NewRequest("GET", "/health/ready", nil))
	if err != nil {
		t.Fatalf("запрос: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("статус = %d, ожидался 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("разбор тела: %v (%s)", err, body)
	}
	if got["status"] != "ready" {
		t.Fatalf("status = %v, ожидалось ready", got["status"])
	}
}

func TestReadyReturns503AndNamesTheDeadDependency(t *testing.T) {
	app := newHealthApp(map[string]service.Probe{
		"db": func(context.Context) error { return nil },
		"ml": func(context.Context) error { return errors.New("connection refused") },
	})

	resp, err := app.Test(httptest.NewRequest("GET", "/health/ready", nil))
	if err != nil {
		t.Fatalf("запрос: %v", err)
	}
	if resp.StatusCode != fiber.StatusServiceUnavailable {
		t.Fatalf("статус = %d, ожидался 503", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var got struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("разбор тела: %v (%s)", err, body)
	}
	if got.Status != "degraded" {
		t.Fatalf("status = %q, ожидалось degraded", got.Status)
	}
	if got.Checks["ml"] == "ok" || got.Checks["ml"] == "" {
		t.Fatalf("checks[ml] = %q, ожидалась причина отказа", got.Checks["ml"])
	}
}
