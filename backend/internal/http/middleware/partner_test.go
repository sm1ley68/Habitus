package middleware

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/domain"
	"habitus-backend/internal/repository"
	"habitus-backend/internal/service"
)

type stubAuth struct {
	identity service.Identity
	err      error
	touched  int
}

func (s *stubAuth) Authenticate(context.Context, string) (service.Identity, error) {
	return s.identity, s.err
}
func (s *stubAuth) TouchKey(context.Context, uuid.UUID) { s.touched++ }

func identityWith(scopes ...string) service.Identity {
	return service.Identity{
		Partner: domain.Partner{ID: uuid.New(), Slug: "gorod", Status: domain.PartnerStatusActive},
		Key:     domain.APIKey{ID: uuid.New(), Prefix: "hab_live_abcd1234", Scopes: scopes},
	}
}

func ok(c *fiber.Ctx) error { return c.JSON(fiber.Map{"ok": true}) }

func do(t *testing.T, app *fiber.App, req *http.Request) (*http.Response, map[string]any) {
	t.Helper()
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	return resp, body
}

func TestPartnerAuthRequiresBearerHeader(t *testing.T) {
	app := fiber.New()
	app.Use(PartnerErrors("https://example.test/docs"))
	app.Get("/x", PartnerAuth(&stubAuth{identity: identityWith()}), ok)

	cases := map[string]struct{ header, code string }{
		"без заголовка": {"", "api_key_missing"},
		"чужая схема":   {"Basic abc", "invalid_api_key"},
		"голый ключ":    {"hab_live_a_b", "invalid_api_key"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/x", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			resp, body := do(t, app, req)
			if resp.StatusCode != 401 {
				t.Fatalf("status = %d; want 401", resp.StatusCode)
			}
			errObj := body["error"].(map[string]any)
			if errObj["code"] != tc.code {
				t.Fatalf("code = %v; want %s", errObj["code"], tc.code)
			}
			if errObj["type"] != "authentication_error" {
				t.Fatalf("type = %v; want authentication_error", errObj["type"])
			}
		})
	}
}

func TestPartnerAuthTouchesKeyOncePerMinute(t *testing.T) {
	auth := &stubAuth{identity: identityWith(service.ScopeSearchRead)}
	app := fiber.New()
	app.Get("/x", PartnerAuth(auth), ok)

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Header.Set("Authorization", "Bearer hab_live_abcd1234_secret")
		do(t, app, req)
	}
	// Отметка «ключом пользуются» — телеметрия, а не счётчик запросов:
	// UPDATE на каждый вызов API здесь не оправдан.
	if auth.touched != 1 {
		t.Fatalf("touched = %d; want 1", auth.touched)
	}
}

func TestRequireScopeNamesMissingRight(t *testing.T) {
	app := fiber.New()
	app.Use(PartnerErrors("https://example.test/docs"))
	app.Get("/x", PartnerAuth(&stubAuth{identity: identityWith(service.ScopeGeoRead)}),
		RequireScope(service.ScopeLeadsRead), ok)

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer hab_live_abcd1234_secret")
	resp, body := do(t, app, req)

	if resp.StatusCode != 403 {
		t.Fatalf("status = %d; want 403", resp.StatusCode)
	}
	errObj := body["error"].(map[string]any)
	// Без имени права разработчику остаётся угадывать, чего не хватило.
	if !strings.Contains(errObj["message"].(string), service.ScopeLeadsRead) {
		t.Fatalf("message = %v; в сообщении должно быть имя недостающего права", errObj["message"])
	}
}

func TestRateLimitHeadersAppearOnEverySuccess(t *testing.T) {
	quotas := NewPartnerQuotas(3, 10)
	app := fiber.New()
	app.Use(PartnerErrors("https://example.test/docs"))
	app.Get("/x", PartnerAuth(&stubAuth{identity: identityWith()}), quotas.PartnerRateLimit(), ok)

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer hab_live_abcd1234_secret")
	resp, _ := do(t, app, req)

	if resp.Header.Get("RateLimit-Limit") != "3" {
		t.Fatalf("RateLimit-Limit = %q; want 3", resp.Header.Get("RateLimit-Limit"))
	}
	// Клиент должен уметь притормозить сам, а не узнавать о лимите в момент,
	// когда в него уже упёрся.
	if resp.Header.Get("RateLimit-Remaining") != "2" {
		t.Fatalf("RateLimit-Remaining = %q; want 2", resp.Header.Get("RateLimit-Remaining"))
	}
	if _, err := strconv.Atoi(resp.Header.Get("RateLimit-Reset")); err != nil {
		t.Fatalf("RateLimit-Reset = %q; ожидались секунды", resp.Header.Get("RateLimit-Reset"))
	}
	if resp.Header.Get("X-RateLimit-Limit") != "3" {
		t.Fatal("дубль X-RateLimit-* нужен готовым клиентам")
	}
}

func TestRateLimitRefusesOverBudgetWithRetryAfter(t *testing.T) {
	quotas := NewPartnerQuotas(1, 10)
	identity := identityWith()
	app := fiber.New()
	app.Use(PartnerErrors("https://example.test/docs"))
	app.Get("/x", PartnerAuth(&stubAuth{identity: identity}), quotas.PartnerRateLimit(), ok)

	call := func() (*http.Response, map[string]any) {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Header.Set("Authorization", "Bearer hab_live_abcd1234_secret")
		return do(t, app, req)
	}
	if resp, _ := call(); resp.StatusCode != 200 {
		t.Fatalf("первый запрос = %d; want 200", resp.StatusCode)
	}
	resp, body := call()
	if resp.StatusCode != 429 {
		t.Fatalf("второй запрос = %d; want 429", resp.StatusCode)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Fatal("429 без Retry-After: клиенту нечем отмерить паузу")
	}
	if body["error"].(map[string]any)["type"] != "rate_limit_error" {
		t.Fatalf("type = %v; want rate_limit_error", body["error"])
	}
}

func TestLLMQuotaIsCountedSeparately(t *testing.T) {
	// Тысяча дешёвых чтений — норма, тысяча вызовов модели — счёт за месяц.
	quotas := NewPartnerQuotas(100, 1)
	identity := identityWith()
	app := fiber.New()
	app.Use(PartnerErrors("https://example.test/docs"))
	app.Get("/cheap", PartnerAuth(&stubAuth{identity: identity}), quotas.PartnerRateLimit(), ok)
	app.Post("/costly", PartnerAuth(&stubAuth{identity: identity}), quotas.PartnerRateLimit(),
		quotas.PartnerLLMLimit(), ok)

	call := func(method, path string) int {
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Authorization", "Bearer hab_live_abcd1234_secret")
		resp, _ := do(t, app, req)
		return resp.StatusCode
	}
	if got := call(http.MethodPost, "/costly"); got != 200 {
		t.Fatalf("первый дорогой запрос = %d; want 200", got)
	}
	if got := call(http.MethodPost, "/costly"); got != 429 {
		t.Fatalf("второй дорогой запрос = %d; want 429", got)
	}
	if got := call(http.MethodGet, "/cheap"); got != 200 {
		t.Fatalf("дешёвый запрос = %d; исчерпанная квота модели не должна его трогать", got)
	}
}

// fakeIdem — хранилище идемпотентности в памяти.
type fakeIdem struct {
	saved map[string]domain.IdempotencyRecord
}

func newFakeIdem() *fakeIdem { return &fakeIdem{saved: map[string]domain.IdempotencyRecord{}} }

func (f *fakeIdem) GetIdempotent(_ context.Context, partnerID uuid.UUID, key, hash string) (domain.IdempotencyRecord, error) {
	rec, found := f.saved[partnerID.String()+"|"+key]
	if !found {
		return domain.IdempotencyRecord{}, repository.ErrNotFound
	}
	if rec.RequestHash != hash {
		return domain.IdempotencyRecord{}, repository.ErrIdempotencyMismatch
	}
	return rec, nil
}

func (f *fakeIdem) SaveIdempotent(_ context.Context, rec domain.IdempotencyRecord) error {
	key := rec.PartnerID.String() + "|" + rec.Key
	if _, exists := f.saved[key]; !exists {
		f.saved[key] = rec
	}
	return nil
}

func idempotencyApp(store IdempotencyStore, calls *int) *fiber.App {
	identity := identityWith()
	app := fiber.New()
	app.Use(PartnerErrors("https://example.test/docs"))
	app.Post("/things", PartnerAuth(&stubAuth{identity: identity}), Idempotency(store),
		func(c *fiber.Ctx) error {
			*calls++
			return c.Status(201).JSON(fiber.Map{"id": "thing-" + strconv.Itoa(*calls)})
		})
	return app
}

func postThing(t *testing.T, app *fiber.App, key, body string) (*http.Response, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/things", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer hab_live_abcd1234_secret")
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	return do(t, app, req)
}

func TestIdempotentRepeatReplaysFirstResponse(t *testing.T) {
	calls := 0
	app := idempotencyApp(newFakeIdem(), &calls)

	first, firstBody := postThing(t, app, "k-1", `{"a":1}`)
	second, secondBody := postThing(t, app, "k-1", `{"a":1}`)

	if calls != 1 {
		t.Fatalf("обработчик вызван %d раза; повтор не должен создавать второй объект", calls)
	}
	if second.StatusCode != first.StatusCode {
		t.Fatalf("status = %d; want %d", second.StatusCode, first.StatusCode)
	}
	if secondBody["id"] != firstBody["id"] {
		t.Fatalf("id = %v; повтор обязан вернуть первый ответ (%v)", secondBody["id"], firstBody["id"])
	}
	if second.Header.Get("Idempotent-Replay") != "true" {
		t.Fatal("повтор не помечен заголовком Idempotent-Replay")
	}
}

func TestIdempotencyKeyReusedWithOtherBodyConflicts(t *testing.T) {
	calls := 0
	app := idempotencyApp(newFakeIdem(), &calls)

	postThing(t, app, "k-1", `{"a":1}`)
	resp, body := postThing(t, app, "k-1", `{"a":2}`)

	if resp.StatusCode != 409 {
		t.Fatalf("status = %d; want 409", resp.StatusCode)
	}
	if body["error"].(map[string]any)["code"] != "idempotency_key_reuse" {
		t.Fatalf("code = %v; want idempotency_key_reuse", body["error"])
	}
	if calls != 1 {
		t.Fatalf("обработчик вызван %d раза; при конфликте ключа операции быть не должно", calls)
	}
}

func TestIdempotencyDoesNotFreezeFailures(t *testing.T) {
	// Запомненный отказ запер бы клиента в ошибке навсегда.
	store := newFakeIdem()
	calls := 0
	identity := identityWith()
	app := fiber.New()
	app.Use(PartnerErrors("https://example.test/docs"))
	app.Post("/things", PartnerAuth(&stubAuth{identity: identity}), Idempotency(store),
		func(c *fiber.Ctx) error {
			calls++
			if calls == 1 {
				return apperr.Validation("город не указан").WithParam("city")
			}
			return c.Status(201).JSON(fiber.Map{"id": "thing"})
		})

	if resp, _ := postThing(t, app, "k-1", `{}`); resp.StatusCode != 400 {
		t.Fatalf("первый ответ = %d; want 400", resp.StatusCode)
	}
	resp, body := postThing(t, app, "k-1", `{}`)
	if resp.StatusCode != 201 {
		t.Fatalf("повтор после ошибки = %d; want 201", resp.StatusCode)
	}
	if body["id"] != "thing" {
		t.Fatalf("body = %v; повтор должен выполнить операцию", body)
	}
}

func TestIdempotencyIgnoredWithoutKey(t *testing.T) {
	calls := 0
	app := idempotencyApp(newFakeIdem(), &calls)

	postThing(t, app, "", `{"a":1}`)
	postThing(t, app, "", `{"a":1}`)

	// Ключ необязателен: навязывать его значит ломать простые интеграции.
	if calls != 2 {
		t.Fatalf("обработчик вызван %d раз(а); без ключа повтор — обычная новая операция", calls)
	}
}

func TestPartnerErrorEnvelopeCarriesRequestIDAndDocURL(t *testing.T) {
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		c.Set(RequestIDHeader, "req-42")
		return c.Next()
	})
	app.Use(PartnerErrors("https://example.test/docs"))
	app.Get("/x", func(*fiber.Ctx) error {
		return apperr.Validation("bbox содержит не число").WithParam("bbox")
	})

	resp, body := do(t, app, httptest.NewRequest(http.MethodGet, "/x", nil))
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d; want 400", resp.StatusCode)
	}
	errObj := body["error"].(map[string]any)
	for field, want := range map[string]any{
		"type": "invalid_request_error", "code": "validation_error",
		"param": "bbox", "request_id": "req-42",
		"doc_url": "https://example.test/docs#errors",
	} {
		if errObj[field] != want {
			t.Errorf("%s = %v; want %v", field, errObj[field], want)
		}
	}
	// Пустые cause/hint в конверт не попадают: «причина известна и она
	// пустая» — неправда, причины просто нет.
	if _, present := errObj["cause"]; present {
		t.Error("пустой cause не должен попадать в ответ")
	}
}

func TestQuotaLimiterReleasesWindowOverTime(t *testing.T) {
	limiter := NewQuotaLimiter(time.Minute)
	now := time.Now()
	limiter.now = func() time.Time { return now }
	key := uuid.New()

	if q := limiter.Allow(key, 1); !q.Allowed {
		t.Fatal("первый запрос должен пройти")
	}
	if q := limiter.Allow(key, 1); q.Allowed {
		t.Fatal("второй запрос в том же окне должен быть отвергнут")
	}
	now = now.Add(61 * time.Second)
	if q := limiter.Allow(key, 1); !q.Allowed {
		t.Fatal("после сдвига окна место должно освободиться")
	}
	// Опустевшее окно удаляется целиком: иначе карта растёт по числу
	// когда-либо заходивших партнёров.
	now = now.Add(61 * time.Second)
	limiter.mu.Lock()
	limiter.sweepLocked(now)
	size := len(limiter.hits)
	limiter.mu.Unlock()
	if size != 0 {
		t.Fatalf("после чистки в карте осталось %d окон", size)
	}
}

// Отказ самого роутера тоже обязан ехать в партнёрском конверте: клиент
// разбирает ответ одним кодом, а не двумя.
func TestPartnerErrorsWrapsRouterFailures(t *testing.T) {
	app := fiber.New()
	app.Use(PartnerErrors("https://example.test/docs"))
	app.Get("/known", ok)

	resp, body := do(t, app, httptest.NewRequest(http.MethodGet, "/unknown", nil))
	if resp.StatusCode != 404 {
		t.Fatalf("status = %d; want 404", resp.StatusCode)
	}
	errObj := body["error"].(map[string]any)
	// "internal_error" здесь врал бы: на нашей стороне ничего не сломалось.
	if errObj["code"] != "unknown_endpoint" || errObj["type"] != "invalid_request_error" {
		t.Fatalf("конверт = %v", errObj)
	}
}

// Ключ, выпущенный на список адресов, не должен работать откуда попало —
// иначе утёкший ключ живёт до тех пор, пока кто-то не заметит счёт.
func TestPartnerAuthEnforcesIPAllowlist(t *testing.T) {
	identity := identityWith(service.ScopeSearchRead)
	identity.Key.AllowedIPs = []string{"203.0.113.0/24"}

	app := fiber.New()
	app.Use(PartnerErrors("https://example.test/docs"))
	app.Get("/x", PartnerAuth(&stubAuth{identity: identity}), ok)

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer hab_live_abcd1234_secret")
	// fiber.App.Test подставляет адрес 0.0.0.0 — он вне списка.
	resp, body := do(t, app, req)

	if resp.StatusCode != 403 {
		t.Fatalf("status = %d; want 403", resp.StatusCode)
	}
	errObj := body["error"].(map[string]any)
	// Отдельный код, а не общий 403: партнёру нужно понять, что чинить —
	// права или сеть.
	if errObj["code"] != "ip_not_allowed" {
		t.Fatalf("code = %v; want ip_not_allowed", errObj["code"])
	}
}

func TestPartnerAuthAllowsAnyAddressWhenListIsEmpty(t *testing.T) {
	app := fiber.New()
	app.Get("/x", PartnerAuth(&stubAuth{identity: identityWith(service.ScopeSearchRead)}), ok)

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer hab_live_abcd1234_secret")
	resp, _ := do(t, app, req)

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d; пустой список означает «откуда угодно»", resp.StatusCode)
	}
}
