package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"habitus-backend/internal/config"
	"habitus-backend/internal/repository"
	"habitus-backend/internal/service"
)

// partnerApp собирает приложение с включённым B2B-контуром. Сервисы пустые:
// тест проверяет проводку маршрутов и публичность документации, а не работу
// поиска.
func partnerApp(t *testing.T, enabled bool) *fiber.App {
	t.Helper()
	return New(config.Settings{
		PartnerAPIEnabled: enabled,
		CORSAllowedOrigin: "http://localhost:3000",
		PublicBaseURL:     "https://api.example.test",
	}, Services{
		Partners:      service.NewPartnerService(nil, nil),
		PartnerSearch: &service.PartnerSearchService{},
		PartnerIdem:   (*repository.PartnerRepo)(nil),
	})
}

func get(t *testing.T, app *fiber.App, path string) (*http.Response, string) {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil), -1)
	if err != nil {
		t.Fatalf("app.Test(%s) error = %v", path, err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp, string(body)
}

// Спека и страница документации доступны без ключа намеренно: разработчик
// должен уметь прочитать контракт до того, как получит доступ.
func TestOpenAPIAndDocsAreServedWithoutKey(t *testing.T) {
	app := partnerApp(t, true)

	resp, body := get(t, app, "/partner/v1/openapi.json")
	if resp.StatusCode != 200 {
		t.Fatalf("openapi.json = %d; want 200", resp.StatusCode)
	}
	var spec map[string]any
	if err := json.Unmarshal([]byte(body), &spec); err != nil {
		t.Fatalf("openapi.json не разбирается: %v", err)
	}
	if spec["openapi"] != "3.1.0" {
		t.Fatalf("openapi = %v", spec["openapi"])
	}

	resp, body = get(t, app, "/docs")
	if resp.StatusCode != 200 {
		t.Fatalf("/docs = %d; want 200", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("Content-Type = %q", resp.Header.Get("Content-Type"))
	}
	if !strings.Contains(body, "Habitus Partner API") {
		t.Fatal("страница документации отдалась без своего содержимого")
	}
}

func TestPartnerRoutesRequireKey(t *testing.T) {
	resp, body := get(t, partnerApp(t, true), "/partner/v1/me")

	if resp.StatusCode != 401 {
		t.Fatalf("/me без ключа = %d; want 401", resp.StatusCode)
	}
	var envelope struct {
		Error struct {
			Type    string `json:"type"`
			Code    string `json:"code"`
			DocURL  string `json:"doc_url"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("конверт ошибки не разбирается: %v", err)
	}
	if envelope.Error.Code != "api_key_missing" || envelope.Error.Type != "authentication_error" {
		t.Fatalf("конверт = %+v", envelope.Error)
	}
	// doc_url собирается из PUBLIC_BASE_URL — иначе ссылка в ошибке ведёт в
	// localhost разработчика, а не в документацию.
	if envelope.Error.DocURL != "https://api.example.test/docs#errors" {
		t.Fatalf("doc_url = %q", envelope.Error.DocURL)
	}
}

// Выключенный контур не должен отвечать «ключ не подошёл»: снаружи не видно
// даже, что он существует.
func TestDisabledPartnerAPIRegistersNoRoutes(t *testing.T) {
	app := partnerApp(t, false)

	for _, path := range []string{"/partner/v1/me", "/partner/v1/openapi.json", "/partner/v1/status"} {
		if resp, _ := get(t, app, path); resp.StatusCode != 404 {
			t.Errorf("%s = %d; при выключенном контуре ожидался 404", path, resp.StatusCode)
		}
	}
}

// Проводка без партнёрских сервисов — обычное состояние тестов, собирающих
// Services{} напрямую: приложение обязано подняться, а не упасть на nil.
func TestUnconfiguredPartnerAPIDoesNotBreakApp(t *testing.T) {
	app := New(config.Settings{PartnerAPIEnabled: true,
		CORSAllowedOrigin: "http://localhost:3000"}, Services{})

	if resp, _ := get(t, app, "/health"); resp.StatusCode != 200 {
		t.Fatalf("/health = %d; основной контур должен работать", resp.StatusCode)
	}
	if resp, _ := get(t, app, "/partner/v1/me"); resp.StatusCode != 404 {
		t.Fatalf("/partner/v1/me = %d; несконфигурированный контур не регистрируется", resp.StatusCode)
	}
}

// Несуществующий путь под /partner/v1 обязан отвечать тем же конвертом, что
// и остальные ошибки: клиент разбирает ответ одним кодом, а не двумя.
//
// Проверка ключа стоит раньше маршрутизации намеренно: аноним не должен по
// разнице 401/404 выяснять, какие ручки у нас есть.
func TestUnknownPartnerEndpointUsesPartnerEnvelope(t *testing.T) {
	resp, body := get(t, partnerApp(t, true), "/partner/v1/nope")

	if resp.StatusCode != 401 {
		t.Fatalf("status = %d; без ключа отказ должен быть 401, а не 404", resp.StatusCode)
	}
	var envelope struct {
		Error struct {
			Type string `json:"type"`
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("конверт не разбирается: %v", err)
	}
	if envelope.Error.Code != "api_key_missing" || envelope.Error.Type != "authentication_error" {
		t.Fatalf("конверт = %+v", envelope.Error)
	}
}
