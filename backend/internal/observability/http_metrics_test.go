package observability

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

// Учёт запросов держится на порядке вызовов внутри fiber: middleware видит
// ошибку, возвращённую из Next(), а итоговый статус ошибочного ответа
// проставляет ErrorHandler уже после. Тест закрепляет фактическое поведение
// цепочки, а не наши представления о ней.
func metricsApp(m *Metrics) *fiber.App {
	app := fiber.New(fiber.Config{
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			// упрощённый аналог middleware.ErrorHandler: статус ставит он,
			// он же и считает — так же, как в проде
			m.IncHTTPRequest(c.Route().Path, "500")
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "boom"})
		},
	})
	app.Use(HTTPRequestsMiddleware(m))
	app.Get("/ok", func(c *fiber.Ctx) error { return c.SendString("ok") })
	app.Get("/boom", func(c *fiber.Ctx) error { return errors.New("boom") })
	app.Get("/metrics", MetricsHandler(m))
	app.Get("/health", func(c *fiber.Ctx) error { return c.SendString("ok") })
	return app
}

func countSeries(t *testing.T, m *Metrics, needle string) int {
	t.Helper()
	n := 0
	for _, line := range strings.Split(m.Render(), "\n") {
		if strings.HasPrefix(line, needle) {
			n++
		}
	}
	return n
}

func TestHTTPRequestsCountedOncePerResponse(t *testing.T) {
	m := NewMetrics()
	app := metricsApp(m)

	for _, path := range []string{"/ok", "/boom"} {
		if _, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil)); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}

	out := m.Render()
	if !strings.Contains(out, `habitus_http_requests_total{route="/ok",status="200"} 1`) {
		t.Fatalf("успешный ответ должен считаться ровно один раз:\n%s", out)
	}
	if !strings.Contains(out, `habitus_http_requests_total{route="/boom",status="500"} 1`) {
		t.Fatalf("ошибочный ответ должен считаться ровно один раз:\n%s", out)
	}
	if got := countSeries(t, m, "habitus_http_requests_total{"); got != 2 {
		t.Fatalf("серий = %d; want 2 — двойного учёта быть не должно:\n%s", got, out)
	}
}

func TestUnknownRouteDoesNotMultiplyLabels(t *testing.T) {
	// 404 по произвольным путям не должен плодить серии: иначе кардинальность
	// habitus_http_requests_total растёт от каждого сканера.
	m := NewMetrics()
	app := metricsApp(m)

	for _, path := range []string{"/nope/a", "/nope/b", "/nope/c"} {
		if _, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil)); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}

	if got := countSeries(t, m, "habitus_http_requests_total{"); got > 1 {
		t.Fatalf("серий = %d; 404 не должен плодить метки:\n%s", got, m.Render())
	}
}

func TestServiceRoutesAreNotCounted(t *testing.T) {
	// Скрейп и проба живости не должны доминировать в счётчике трафика API.
	m := NewMetrics()
	app := metricsApp(m)

	for _, path := range []string{"/metrics", "/health", "/metrics"} {
		if _, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil)); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}

	if out := m.Render(); strings.Contains(out, `route="/metrics"`) ||
		strings.Contains(out, `route="/health"`) {
		t.Fatalf("служебные роуты не должны попадать в счётчик:\n%s", out)
	}
}

func TestQuoteLabelEscapesNewline(t *testing.T) {
	// layer/stage приходят из JSON-ответа ML — сырой перевод строки в метке
	// ломает разбор всего ответа /metrics, а не одной серии.
	m := NewMetrics()
	m.IncMLDegraded("ml\nbroken")

	out := m.Render()
	if strings.Contains(out, "layer=\"ml\nbroken\"") {
		t.Fatalf("перевод строки в метке не экранирован:\n%q", out)
	}
	if !strings.Contains(out, `layer="ml\nbroken"`) {
		t.Fatalf("ожидалось экранирование \\n в метке:\n%q", out)
	}
}
