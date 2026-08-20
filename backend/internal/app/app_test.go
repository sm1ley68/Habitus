package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"habitus-backend/internal/config"
)

// На превышении BodyLimit fiber.App.Test возвращает ОШИБКУ клиента, а не ответ
// 413: сервер обрывает чтение тела ещё до маршрутизации. Для теста важен сам
// факт отказа.
type fiberAppUnderTest struct{ app *fiber.App }

func testApp(bodyLimit int) *fiberAppUnderTest {
	cfg := config.Settings{BodyLimitBytes: bodyLimit, CORSAllowedOrigin: "http://localhost:3000"}
	return &fiberAppUnderTest{app: New(cfg, Services{})}
}

func (f *fiberAppUnderTest) post(body string) (*http.Response, error) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return f.app.Test(req)
}

func TestBodyLimitRejectsOversizedPayload(t *testing.T) {
	// Без явного BodyLimit фреймворк принимает до 4 МБ на запрос — публичному
	// шлюзу столько не нужно ни на одном роуте.
	_, err := testApp(1024).post(strings.Repeat("x", 4096))

	if err == nil {
		t.Fatal("тело сверх лимита должно быть отвергнуто")
	}
	if !strings.Contains(err.Error(), "limit") {
		t.Fatalf("err = %v; ожидался отказ по размеру тела", err)
	}
}

func TestBodyLimitAllowsNormalPayload(t *testing.T) {
	// Тот же роут с обычным телом до лимита не доходит — запрос принят и
	// доехал до слоя обработки.
	_, err := testApp(1 << 20).post(`{"email":"a@b.c","password":"12345678"}`)

	if err != nil && strings.Contains(err.Error(), "limit") {
		t.Fatalf("обычное тело упёрлось в лимит: %v", err)
	}
}

func TestBodyLimitFallsBackWhenMisconfigured(t *testing.T) {
	// BodyLimit <= 0 в fiber означает «без ограничений» — кривая переменная
	// окружения не должна молча снимать защиту.
	_, err := testApp(0).post(strings.Repeat("x", 2<<20))

	if err == nil {
		t.Fatal("при нулевой настройке должен действовать дефолтный лимит")
	}
}

func TestHealthIsServedWithoutAuth(t *testing.T) {
	resp, err := testApp(1 << 20).app.Test(httptest.NewRequest(http.MethodGet, "/health", nil))
	if err != nil {
		t.Fatalf("Test() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200", resp.StatusCode)
	}
}

// GET /metrics — как /health, без авторизации (Task 8).
func TestMetricsIsServedWithoutAuth(t *testing.T) {
	resp, err := testApp(1 << 20).app.Test(httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if err != nil {
		t.Fatalf("Test() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200", resp.StatusCode)
	}
}

// Появление /metrics и рейт-лимита не ослабило остальной API: защищённый
// роут по-прежнему требует сессию.
func TestProtectedRouteStillRequiresAuth(t *testing.T) {
	resp, err := testApp(1 << 20).app.Test(httptest.NewRequest(http.MethodGet, "/api/v1/me", nil))
	if err != nil {
		t.Fatalf("Test() error = %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401", resp.StatusCode)
	}
}
