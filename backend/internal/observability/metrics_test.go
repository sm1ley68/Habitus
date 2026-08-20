package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

// /metrics отдаёт валидный текст Prometheus exposition и содержит все пять
// зарегистрированных семейств счётчиков из брифа Task 8.
func TestMetricsRenderContainsAllRegisteredFamilies(t *testing.T) {
	m := NewMetrics()
	m.IncHTTPRequest("/api/v1/chats", "200")
	m.ObserveMLCall("search", 1.5)
	m.IncMLDegraded("vector")
	m.ObserveMLStage("retrieval", 0.42)
	m.IncRateLimited()

	text := m.Render()

	wantFamilies := []string{
		"habitus_http_requests_total",
		"habitus_ml_call_seconds",
		"habitus_ml_degraded_total",
		"habitus_ml_stage_seconds",
		"habitus_rate_limited_total",
	}
	for _, family := range wantFamilies {
		if !strings.Contains(text, "# TYPE "+family) {
			t.Errorf("в выводе нет # TYPE для %s:\n%s", family, text)
		}
	}

	wantLines := []string{
		`habitus_http_requests_total{route="/api/v1/chats",status="200"} 1`,
		`habitus_ml_call_seconds_sum{kind="search"} 1.5`,
		`habitus_ml_call_seconds_count{kind="search"} 1`,
		`habitus_ml_degraded_total{layer="vector"} 1`,
		`habitus_ml_stage_seconds_sum{stage="retrieval"} 0.42`,
		`habitus_ml_stage_seconds_count{stage="retrieval"} 1`,
		`habitus_rate_limited_total 1`,
	}
	for _, line := range wantLines {
		if !strings.Contains(text, line) {
			t.Errorf("в выводе нет строки %q:\n%s", line, text)
		}
	}
}

// Стадия, которой не было в timings ответа ML, не публикуется — метрика не
// придумывает нулевые замеры за отсутствующие стадии (глобальное ограничение
// задачи).
func TestMetricsRenderOmitsUnobservedStages(t *testing.T) {
	m := NewMetrics()
	m.ObserveMLStage("parse", 0.01)

	text := m.Render()

	if strings.Contains(text, `stage="rerank"`) {
		t.Fatalf("стадия rerank не замерялась — её не должно быть в выводе:\n%s", text)
	}
	if !strings.Contains(text, `stage="parse"`) {
		t.Fatalf("замеренная стадия parse отсутствует в выводе:\n%s", text)
	}
}

// Счётчики потокобезопасны: конкурентная запись из middleware/сервисов не
// должна гоняться — проверяется через go test -race.
func TestMetricsConcurrentWritesAreRaceFree(t *testing.T) {
	m := NewMetrics()
	done := make(chan struct{})
	for i := 0; i < 20; i++ {
		go func(n int) {
			m.IncHTTPRequest("/probe", "200")
			m.ObserveMLCall("search", 0.1)
			m.IncMLDegraded("vector")
			m.ObserveMLStage("parse", 0.01)
			m.IncRateLimited()
			done <- struct{}{}
		}(i)
	}
	for i := 0; i < 20; i++ {
		<-done
	}
	_ = m.Render()
}

// GET /metrics доступен без авторизации, а остальной API продолжает требовать
// её (роут регистрируется рядом с /health в router.go).
func TestMetricsHandlerServedWithoutAuth(t *testing.T) {
	m := NewMetrics()
	m.IncRateLimited()

	app := fiber.New()
	app.Get("/metrics", MetricsHandler(m))

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if err != nil {
		t.Fatalf("Test() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("Content-Type = %q; want text/plain", ct)
	}

	body := make([]byte, 4096)
	n, _ := resp.Body.Read(body)
	text := string(body[:n])
	if !strings.Contains(text, "habitus_rate_limited_total 1") {
		t.Fatalf("тело /metrics не содержит зарегистрированный счётчик:\n%s", text)
	}
}
