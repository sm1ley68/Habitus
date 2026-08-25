# Замыкание конверсионного цикла MVP (бек) — план внедрения

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Довести Go-шлюз до состояния, в котором путь пользователя замыкается: гость ищет без регистрации, сохраняет объекты, оценивает выдачу, отправляет заявку продавцу, а продукт фиксирует всю воронку — плюс честный readiness вместо заглушки `/health`.

**Architecture:** Ничего не переписываем. Четыре независимые группы задач поверх существующей схемы «репозиторий → сервис → хендлер»: (A) readiness-проба, (B) гость как обычная строка `users` с флагом `is_guest`, (C) заявки в отдельной таблице `leads` с `source_url` в паспорте для витринных объектов, (D) избранное / фидбек / журнал продуктовых событий. Таблица `listings` остаётся Python-owned и только читается.

**Tech Stack:** Go 1.25, Fiber v2, pgx v5 + pgxpool, golang-migrate, Postgres 16 (PostGIS + pgvector), zerolog.

**Spec:** Этот файл. Первоисточник по контракту API — `frontend/Пайплайн фронт.md`; каждая задача, меняющая контракт, дописывает туда раздел. Диагноз, из которого вырос план, — разбор бэкенда от 2026-08-25 (разрыв Критической цепочки после «нашли», стена регистрации перед Aha Moment, отсутствие сигнала о качестве подбора).

## Global Constraints

- Работа идёт напрямую в `main`. Отдельные ветки не заводить.
- Формат коммитов — Conventional Commits **на русском**: `feat:`, `fix:`, `chore:`, `refactor:`, `test:`, `docs:`. Подписи и трейлеры (`Co-Authored-By` и любые другие) **не используются**.
- Координаты везде `[lng, lat]`, WGS84 (EPSG:4326). Без трансформаций.
- **Не выдумывать факты.** Нет данных — поле `null` или блок отсутствует. Синтетический ноль вместо отсутствующего значения запрещён.
- Таблицы `listings`, `poi`, `raw_listings`, `urban_evidence` — Python-owned. Go читает их и **никогда** не пишет и не мигрирует.
- Все ошибки наружу — через `apperr` (единый конверт `{"error":{code,message}}`), никогда не голый `fiber.Error`.
- Миграции Go-стороны нумеруются последовательно в `backend/migrations/`, каждая с парой `.up.sql` / `.down.sql`. Нумерация этого плана: `0011`–`0015`.
- Тесты: `cd backend && go test ./...`. Репозиторные тесты берут пул через `testPool(t)` (`backend/internal/repository/main_test.go`) и **скипаются**, а не падают, без поднятого Postgres.
- Комментарии в коде — на русском, объясняют *почему*, а не *что* (соответствие стилю окружающего кода).
- Каждая задача заканчивается зелёным `go test ./...` и коммитом.

## Карта файлов

| Файл | Ответственность | Задача |
|---|---|---|
| `backend/internal/service/readiness.go` | Прогон проб зависимостей, агрегат «готов / деградирован» | 1 |
| `backend/internal/http/handlers/health_handler.go` | `/health` (liveness) и `/health/ready` (readiness) | 1 |
| `backend/migrations/0011_guest_users.*.sql` | `users.is_guest`, ослабление NOT NULL на учётных данных | 2 |
| `backend/internal/repository/user_repo.go` | `CreateGuest`, `UpgradeGuest`, `DeleteStaleGuests` | 2 |
| `backend/internal/repository/session_repo.go` | `GetSession` — user_id + признак гостя одним запросом | 2 |
| `backend/internal/service/auth_service.go` | Гостевая сессия, апгрейд гостя в аккаунт | 3 |
| `backend/internal/service/guest_sweeper.go` | Чистка брошенных гостей | 3 |
| `backend/internal/http/middleware/auth.go` | Признак гостя в `Locals` | 4 |
| `backend/internal/http/middleware/registered.go` | `RequireRegistered` — гостю в кабинет продавца нельзя | 4 |
| `backend/migrations/0012_leads.*.sql` | Таблица заявок | 6 |
| `backend/internal/repository/lead_repo.go` | CRUD заявок | 6 |
| `backend/internal/service/lead_service.go` | Правила заявки: кому можно, куда уходит | 6 |
| `backend/internal/http/handlers/lead_handler.go` | `POST /objects/{id}/lead`, `GET /owner/leads` | 6, 7 |
| `backend/migrations/0013_favorites.*.sql` | Избранное | 8 |
| `backend/internal/repository/favorite_repo.go` | CRUD избранного | 8 |
| `backend/internal/service/favorite_service.go` | Сборка карточек избранного из витрины | 8 |
| `backend/migrations/0014_result_feedback.*.sql` | Оценки выдачи | 9 |
| `backend/internal/repository/feedback_repo.go` | Upsert оценки | 9 |
| `backend/migrations/0015_product_events.*.sql` | Журнал продуктовых событий | 10 |
| `backend/internal/repository/event_repo.go` | Запись события | 10 |
| `backend/internal/service/events.go` | Неблокирующий рекордер с буфером | 10 |
| `docs/notes/funnel-queries.md` | SQL воронки для чтения журнала | 11 |

**Группы независимы.** A (задача 1), B (2–4), C (5–7), D (8–11) не пересекаются по файлам, кроме `router.go`, `app.go`, `main.go` и `frontend/Пайплайн фронт.md`, которые дописываются аддитивно. Внутри группы порядок обязателен.

---

## Task 1: `/health/ready` вместо заглушки

`/health` сейчас возвращает `{"status":"ok"}` безусловно — оркестратор не отличит живой шлюз от шлюза с мёртвой БД. Разделяем роли: `/health` остаётся **liveness** (процесс жив, перезапускать не надо) и это его честная работа, а новый `/health/ready` — **readiness**: пингует Postgres и ML-сервис и отдаёт 503, когда зависимость лежит.

**Files:**
- Create: `backend/internal/service/readiness.go`
- Create: `backend/internal/service/readiness_test.go`
- Modify: `backend/internal/client/ml_client.go` (добавить `Health`)
- Modify: `backend/internal/http/handlers/health_handler.go`
- Create: `backend/internal/http/handlers/health_handler_test.go`
- Modify: `backend/internal/http/router.go:31-32`
- Modify: `backend/internal/app/app.go` (Services + wiring)
- Modify: `backend/cmd/api/main.go` (собрать пробы)
- Modify: `docker-compose.yml` (healthcheck бэкенда → `/health/ready`)
- Modify: `README.md` (раздел «Диагностика проблем»)

**Interfaces:**
- Consumes: `pgxpool.Pool.Ping(ctx) error`, `client.MLClient`
- Produces: `service.Probe = func(context.Context) error`; `service.NewReadinessService(timeout time.Duration, probes map[string]service.Probe) *service.ReadinessService`; `(*ReadinessService).Check(ctx) (ok bool, checks map[string]string)`; `handlers.NewHealthHandler(*service.ReadinessService) *handlers.HealthHandler` с методами `Live` и `Ready`; `(*client.MLClient).Health(ctx) error`

- [ ] **Step 1: Написать падающий тест на ReadinessService**

Создать `backend/internal/service/readiness_test.go`:

```go
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
```

- [ ] **Step 2: Прогнать тест и убедиться, что он падает**

Run: `cd backend && go test ./internal/service/ -run TestReadiness -v`
Expected: FAIL — `undefined: NewReadinessService`, `undefined: Probe`

- [ ] **Step 3: Реализовать ReadinessService**

Создать `backend/internal/service/readiness.go`:

```go
// readiness.go — проба готовности шлюза принимать трафик. Отдельно от
// liveness: процесс может быть жив и при этом бесполезен, если под ним нет
// Postgres или ML-сервиса. Оркестратор должен различать эти два состояния —
// liveness перезапускает контейнер, readiness лишь снимает его с балансировки.
package service

import (
	"context"
	"sync"
	"time"
)

// Probe — одна проверка зависимости. nil — зависимость отвечает.
type Probe func(context.Context) error

type ReadinessService struct {
	probes  map[string]Probe
	timeout time.Duration
}

// NewReadinessService. Неположительный таймаут означает «без собственного
// предела» — тогда границу задаёт только контекст запроса.
func NewReadinessService(timeout time.Duration, probes map[string]Probe) *ReadinessService {
	return &ReadinessService{probes: probes, timeout: timeout}
}

// Check прогоняет пробы ПАРАЛЛЕЛЬНО: последовательный прогон складывал бы
// таймауты, и при двух мёртвых зависимостях readiness сам отвечал бы дольше,
// чем его готов ждать оркестратор. Возвращает ok и карту «имя → ok либо текст
// причины»: 503 без причины — та же заглушка, только с другим кодом.
func (s *ReadinessService) Check(ctx context.Context) (bool, map[string]string) {
	if s.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.timeout)
		defer cancel()
	}

	checks := make(map[string]string, len(s.probes))
	var mu sync.Mutex
	var wg sync.WaitGroup

	for name, probe := range s.probes {
		wg.Add(1)
		go func(name string, probe Probe) {
			defer wg.Done()
			result := "ok"
			if err := probe(ctx); err != nil {
				result = err.Error()
			}
			mu.Lock()
			checks[name] = result
			mu.Unlock()
		}(name, probe)
	}
	wg.Wait()

	ok := true
	for _, result := range checks {
		if result != "ok" {
			ok = false
		}
	}
	return ok, checks
}
```

- [ ] **Step 4: Прогнать тест и убедиться, что он проходит**

Run: `cd backend && go test ./internal/service/ -run TestReadiness -v`
Expected: PASS (три теста)

- [ ] **Step 5: Написать падающий тест на MLClient.Health**

Дописать в `backend/internal/client/ml_client_test.go`:

```go
func TestMLClientHealthOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Errorf("путь = %q, ожидался /health", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	if err := NewMLClient(srv.URL, time.Second).Health(context.Background()); err != nil {
		t.Fatalf("Health вернул ошибку на живом сервисе: %v", err)
	}
}

func TestMLClientHealthFailsOnBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	if err := NewMLClient(srv.URL, time.Second).Health(context.Background()); err == nil {
		t.Fatal("Health = nil на 503, ожидалась ошибка")
	}
}
```

- [ ] **Step 6: Прогнать тест и убедиться, что он падает**

Run: `cd backend && go test ./internal/client/ -run TestMLClientHealth -v`
Expected: FAIL — `c.Health undefined`

- [ ] **Step 7: Реализовать MLClient.Health**

Добавить в `backend/internal/client/ml_client.go` рядом с `WarmUp` (около строки 416):

```go
// Health — дешёвый пинг ML-сервиса для readiness: GET /health, без тела.
// Намеренно НЕ переиспользует WarmUp — тот делает настоящий /search и стоит
// секунд, а проба готовности обязана укладываться в интервал healthcheck'а.
func (c *MLClient) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ml /health: %s", resp.Status)
	}
	return nil
}
```

- [ ] **Step 8: Прогнать тест и убедиться, что он проходит**

Run: `cd backend && go test ./internal/client/ -run TestMLClientHealth -v`
Expected: PASS

- [ ] **Step 9: Написать падающий тест на хендлеры**

Создать `backend/internal/http/handlers/health_handler_test.go`:

```go
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
```

- [ ] **Step 10: Прогнать тест и убедиться, что он падает**

Run: `cd backend && go test ./internal/http/handlers/ -run "TestLive|TestReady" -v`
Expected: FAIL — `undefined: NewHealthHandler`

- [ ] **Step 11: Переписать health_handler.go**

Заменить содержимое `backend/internal/http/handlers/health_handler.go` целиком:

```go
package handlers

import (
	"github.com/gofiber/fiber/v2"

	"habitus-backend/internal/service"
)

type HealthHandler struct {
	ready *service.ReadinessService
}

func NewHealthHandler(ready *service.ReadinessService) *HealthHandler {
	return &HealthHandler{ready: ready}
}

// Live — liveness. Зависимости здесь НЕ проверяются намеренно: сигнал
// «перезапусти меня» не должен зависеть от моргнувшего Postgres, иначе одна
// недоступная БД укладывает весь пул контейнеров шлюза.
func (h *HealthHandler) Live(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{"status": "ok"})
}

// Ready — readiness: шлюз бесполезен без Postgres и без ML-сервиса, поэтому
// при мёртвой зависимости отвечает 503 и называет, какая именно легла.
func (h *HealthHandler) Ready(c *fiber.Ctx) error {
	ok, checks := h.ready.Check(c.Context())
	if !ok {
		return c.Status(fiber.StatusServiceUnavailable).
			JSON(fiber.Map{"status": "degraded", "checks": checks})
	}
	return c.JSON(fiber.Map{"status": "ready", "checks": checks})
}
```

- [ ] **Step 12: Подключить хендлер в роутер**

В `backend/internal/http/router.go` добавить поле в `Handlers`:

```go
type Handlers struct {
	Health    *handlers.HealthHandler
	Auth      *handlers.AuthHandler
	Chat      *handlers.ChatHandler
	Stream    *handlers.StreamHandler
	Object    *handlers.ObjectHandler
	ObjectAsk *handlers.ObjectAskHandler
	Geo       *handlers.GeoHandler
	Results   *handlers.ResultsHandler
	Owner     *handlers.OwnerHandler
}
```

и заменить строку `app.Get("/health", handlers.Health)` на:

```go
	app.Get("/health", h.Health.Live)
	app.Get("/health/ready", h.Health.Ready)
```

- [ ] **Step 13: Прокинуть пробы через app.New**

В `backend/internal/app/app.go` добавить поле в `Services`:

```go
type Services struct {
	Ready     *service.ReadinessService
	Auth      *service.AuthService
	// ... остальные поля без изменений
```

и в вызове `httpapi.RegisterRoutes` добавить первым полем:

```go
		Health:    handlers.NewHealthHandler(svc.Ready),
```

Если `svc.Ready` равен nil (тесты собирают `app.Services{}` напрямую — так делает `app_test.go`), подставить пустой сервис, чтобы `/health` не паниковал:

```go
	ready := svc.Ready
	if ready == nil {
		ready = service.NewReadinessService(readyTimeout, nil)
	}
```

и рядом с остальными константами границ HTTP-слоя:

```go
	// readyTimeout — сколько ждём зависимости в readiness. Заметно меньше
	// интервала healthcheck'а в compose (10 с), чтобы проба не наслаивалась
	// сама на себя.
	readyTimeout = 3 * time.Second
```

Использовать `ready` вместо `svc.Ready` в `handlers.NewHealthHandler`.

- [ ] **Step 14: Собрать пробы в main.go**

В `backend/cmd/api/main.go` после создания `mlClient` и `pool`, перед сборкой `fiberApp`, добавить:

```go
	// Пробы readiness: обе зависимости, без которых шлюз бесполезен.
	readinessService := service.NewReadinessService(3*time.Second, map[string]service.Probe{
		"db": pool.Ping,
		"ml": mlClient.Health,
	})
```

и передать в `app.Services`:

```go
		Ready:     readinessService,
```

- [ ] **Step 15: Перевести healthcheck бэкенда в compose на readiness**

В `docker-compose.yml`, в блоке `backend.healthcheck`, заменить URL:

```yaml
    healthcheck:
      # /health/ready, а не /health: фронт зависит от бэкенда по
      # service_healthy, и пускать его к шлюзу, под которым нет ML, незачем —
      # он получит «ошибку ИИ» на первом же поиске.
      test: ["CMD", "wget", "-qO-", "http://localhost:8080/health/ready"]
      interval: 10s
      timeout: 5s
      retries: 5
```

- [ ] **Step 16: Дописать README**

В `README.md`, в раздел «Диагностика проблем», добавить:

````markdown
### Проверить, что шлюз действительно готов

```bash
# liveness — жив ли процесс (перезапускать или нет)
curl -s localhost:8080/health

# readiness — есть ли под ним Postgres и ML-сервис
curl -s localhost:8080/health/ready | jq
# {"status":"ready","checks":{"db":"ok","ml":"ok"}}
# при мёртвой зависимости — 503 и причина:
# {"status":"degraded","checks":{"db":"ok","ml":"ml /health: 503 Service Unavailable"}}
```
````

- [ ] **Step 17: Прогнать весь набор тестов**

Run: `cd backend && go test ./...`
Expected: PASS во всех пакетах

- [ ] **Step 18: Коммит**

```bash
cd /Users/yarik/PycharmProjects/Habitus
git add backend/internal/service/readiness.go backend/internal/service/readiness_test.go \
        backend/internal/client/ml_client.go backend/internal/client/ml_client_test.go \
        backend/internal/http/handlers/health_handler.go \
        backend/internal/http/handlers/health_handler_test.go \
        backend/internal/http/router.go backend/internal/app/app.go \
        backend/cmd/api/main.go docker-compose.yml README.md
git commit -m "feat: readiness проверяет Postgres и ML вместо безусловного ok"
```

---

## Task 2: гость на уровне БД

Гость — обычная строка `users` с флагом `is_guest`. Так все внешние ключи (`sessions`, `chats`, `owner_listings`, будущие `favorites`/`leads`) работают без единой правки, а регистрация гостя — это `UPDATE` той же строки: чаты, результаты и досье прилипают к аккаунту сами, без переноса данных.

**Files:**
- Create: `backend/migrations/0011_guest_users.up.sql`
- Create: `backend/migrations/0011_guest_users.down.sql`
- Modify: `backend/internal/domain/domain.go` (поле `IsGuest`)
- Modify: `backend/internal/repository/user_repo.go`
- Modify: `backend/internal/repository/session_repo.go`
- Create: `backend/internal/repository/user_guest_test.go`

**Interfaces:**
- Consumes: `testPool(t)` и `newTestUser(t, repo)` из `backend/internal/repository/main_test.go`
- Produces: `domain.User.IsGuest bool`; `(*UserRepo).CreateGuest(ctx) (domain.User, error)`; `(*UserRepo).UpgradeGuest(ctx, id uuid.UUID, email, passwordHash, name string) (domain.User, error)`; `(*UserRepo).DeleteStaleGuests(ctx, olderThan time.Duration) (int64, error)`; `(*SessionRepo).GetSession(ctx, tokenHash string) (uuid.UUID, bool, error)`

- [ ] **Step 1: Написать миграцию**

Создать `backend/migrations/0011_guest_users.up.sql`:

```sql
-- Гость — обычный пользователь без учётных данных. Отдельной таблицы нет
-- намеренно: так все FK (sessions, chats, owner_listings) продолжают
-- ссылаться на users без правок, а регистрация гостя становится UPDATE той
-- же строки — чаты и результаты поиска прилипают к аккаунту сами.
ALTER TABLE users ALTER COLUMN email DROP NOT NULL;
ALTER TABLE users ALTER COLUMN password_hash DROP NOT NULL;
ALTER TABLE users ADD COLUMN is_guest boolean NOT NULL DEFAULT false;

-- Учётные данные обязательны ровно для зарегистрированных. Без этого
-- ослабление NOT NULL выше открыло бы дорогу аккаунту без пароля.
ALTER TABLE users ADD CONSTRAINT users_credentials_ck
    CHECK (is_guest OR (email IS NOT NULL AND password_hash IS NOT NULL));

-- Свипер ищет брошенных гостей по возрасту; без индекса это seq scan по всей
-- таблице пользователей на каждом проходе.
CREATE INDEX users_stale_guests_ix ON users (created_at) WHERE is_guest;
```

Создать `backend/migrations/0011_guest_users.down.sql`:

```sql
-- Откат уносит гостей: строк без email в схеме с NOT NULL быть не может.
DROP INDEX IF EXISTS users_stale_guests_ix;
DELETE FROM users WHERE is_guest;
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_credentials_ck;
ALTER TABLE users DROP COLUMN is_guest;
ALTER TABLE users ALTER COLUMN password_hash SET NOT NULL;
ALTER TABLE users ALTER COLUMN email SET NOT NULL;
```

- [ ] **Step 2: Написать падающий тест на репозиторий**

Создать `backend/internal/repository/user_guest_test.go`:

```go
package repository

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCreateGuestHasNoCredentials(t *testing.T) {
	pool := testPool(t)
	repo := NewUserRepo(pool)

	guest, err := repo.CreateGuest(context.Background())
	if err != nil {
		t.Fatalf("CreateGuest: %v", err)
	}
	if !guest.IsGuest {
		t.Fatal("IsGuest = false у только что созданного гостя")
	}
	if guest.Email != "" {
		t.Fatalf("Email = %q, у гостя его быть не должно", guest.Email)
	}
	if guest.ID == uuid.Nil {
		t.Fatal("ID пустой")
	}
}

// Два гостя подряд — законный сценарий (два браузера). Уникальность email
// не должна им мешать: в Postgres UNIQUE пропускает сколько угодно NULL.
func TestCreateGuestTwiceSucceeds(t *testing.T) {
	pool := testPool(t)
	repo := NewUserRepo(pool)

	first, err := repo.CreateGuest(context.Background())
	if err != nil {
		t.Fatalf("первый гость: %v", err)
	}
	second, err := repo.CreateGuest(context.Background())
	if err != nil {
		t.Fatalf("второй гость: %v", err)
	}
	if first.ID == second.ID {
		t.Fatal("оба гостя получили один id")
	}
}

// Ключевое свойство схемы: апгрейд сохраняет id, поэтому всё, что гость успел
// сделать (чаты, результаты, избранное), остаётся при нём после регистрации.
func TestUpgradeGuestKeepsSameID(t *testing.T) {
	pool := testPool(t)
	repo := NewUserRepo(pool)
	ctx := context.Background()

	guest, err := repo.CreateGuest(ctx)
	if err != nil {
		t.Fatalf("CreateGuest: %v", err)
	}
	email := uuid.NewString() + "@example.test"

	upgraded, err := repo.UpgradeGuest(ctx, guest.ID, email, "hash", "Покупатель")
	if err != nil {
		t.Fatalf("UpgradeGuest: %v", err)
	}
	if upgraded.ID != guest.ID {
		t.Fatalf("id сменился: %s → %s", guest.ID, upgraded.ID)
	}
	if upgraded.IsGuest {
		t.Fatal("IsGuest = true после апгрейда")
	}
	if upgraded.Email != email {
		t.Fatalf("Email = %q, ожидался %q", upgraded.Email, email)
	}
}

func TestUpgradeGuestRejectsTakenEmail(t *testing.T) {
	pool := testPool(t)
	repo := NewUserRepo(pool)
	ctx := context.Background()

	email := uuid.NewString() + "@example.test"
	if _, err := repo.Create(ctx, email, "hash", "Занявший"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	guest, err := repo.CreateGuest(ctx)
	if err != nil {
		t.Fatalf("CreateGuest: %v", err)
	}

	_, err = repo.UpgradeGuest(ctx, guest.ID, email, "hash", "Гость")

	if err != ErrDuplicateEmail {
		t.Fatalf("err = %v, ожидался ErrDuplicateEmail", err)
	}
}

// Зарегистрированного апгрейдить нельзя: иначе чужой email перезаписал бы
// существующий аккаунт.
func TestUpgradeGuestRejectsRegisteredUser(t *testing.T) {
	pool := testPool(t)
	repo := NewUserRepo(pool)
	ctx := context.Background()

	user, err := repo.Create(ctx, uuid.NewString()+"@example.test", "hash", "Аккаунт")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = repo.UpgradeGuest(ctx, user.ID, uuid.NewString()+"@example.test", "hash", "Новый")

	if err != ErrNotFound {
		t.Fatalf("err = %v, ожидался ErrNotFound", err)
	}
}

// Свежего гостя чистка не трогает: он прямо сейчас ищет квартиру.
func TestDeleteStaleGuestsSparesFreshOnes(t *testing.T) {
	pool := testPool(t)
	repo := NewUserRepo(pool)
	ctx := context.Background()

	guest, err := repo.CreateGuest(ctx)
	if err != nil {
		t.Fatalf("CreateGuest: %v", err)
	}

	if _, err := repo.DeleteStaleGuests(ctx, 24*time.Hour); err != nil {
		t.Fatalf("DeleteStaleGuests: %v", err)
	}

	if _, err := repo.GetByID(ctx, guest.ID); err != nil {
		t.Fatalf("свежего гостя удалили: %v", err)
	}
}

// Нулевой возраст означает «всех гостей», зарегистрированных при этом не
// трогает — проверяем обе половины одним прогоном.
func TestDeleteStaleGuestsRemovesGuestsOnly(t *testing.T) {
	pool := testPool(t)
	repo := NewUserRepo(pool)
	ctx := context.Background()

	guest, err := repo.CreateGuest(ctx)
	if err != nil {
		t.Fatalf("CreateGuest: %v", err)
	}
	user, err := repo.Create(ctx, uuid.NewString()+"@example.test", "hash", "Аккаунт")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := repo.DeleteStaleGuests(ctx, 0); err != nil {
		t.Fatalf("DeleteStaleGuests: %v", err)
	}

	if _, err := repo.GetByID(ctx, guest.ID); err != ErrNotFound {
		t.Fatalf("гость уцелел: err = %v", err)
	}
	if _, err := repo.GetByID(ctx, user.ID); err != nil {
		t.Fatalf("удалили зарегистрированного: %v", err)
	}
}

func TestGetSessionReportsGuestFlag(t *testing.T) {
	pool := testPool(t)
	users := NewUserRepo(pool)
	sessions := NewSessionRepo(pool)
	ctx := context.Background()

	guest, err := users.CreateGuest(ctx)
	if err != nil {
		t.Fatalf("CreateGuest: %v", err)
	}
	token := uuid.NewString()
	if err := sessions.Create(ctx, token, guest.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("Create session: %v", err)
	}

	userID, isGuest, err := sessions.GetSession(ctx, token)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if userID != guest.ID {
		t.Fatalf("user_id = %s, ожидался %s", userID, guest.ID)
	}
	if !isGuest {
		t.Fatal("is_guest = false у сессии гостя")
	}
}

func TestGetSessionRejectsExpired(t *testing.T) {
	pool := testPool(t)
	users := NewUserRepo(pool)
	sessions := NewSessionRepo(pool)
	ctx := context.Background()

	userID := newTestUser(t, users)
	token := uuid.NewString()
	if err := sessions.Create(ctx, token, userID, time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("Create session: %v", err)
	}

	if _, _, err := sessions.GetSession(ctx, token); err != ErrNotFound {
		t.Fatalf("err = %v, ожидался ErrNotFound на протухшей сессии", err)
	}
}
```

- [ ] **Step 3: Прогнать тест и убедиться, что он падает**

Run: `cd backend && go test ./internal/repository/ -run "Guest|GetSession" -v`
Expected: FAIL — `repo.CreateGuest undefined`, `sessions.GetSession undefined`

- [ ] **Step 4: Добавить поле в domain.User**

В `backend/internal/domain/domain.go` в структуру `User` добавить после `Name`:

```go
	// IsGuest — аккаунт без учётных данных, заведённый ради первого поиска.
	// Апгрейд при регистрации сохраняет id, поэтому чаты и результаты гостя
	// остаются при нём.
	IsGuest bool
```

- [ ] **Step 5: Реализовать методы UserRepo**

В `backend/internal/repository/user_repo.go` заменить тела существующих SELECT'ов так, чтобы они читали новые колонки, и добавить три метода. Полный список правок:

Добавить в импорты `"time"`.

Во всех трёх существующих запросах (`Create`, `GetByEmail`, `GetByID`) заменить список колонок `id, email, password_hash, name, created_at, updated_at` на:

```sql
id, COALESCE(email, ''), COALESCE(password_hash, ''), COALESCE(name, ''), is_guest, created_at, updated_at
```

и соответственно в каждом `.Scan(...)` вставить `&u.IsGuest` между `&u.Name` и `&u.CreatedAt`.

Добавить в конец файла:

```go
// CreateGuest заводит пользователя без учётных данных — под первый поиск без
// регистрации. Имя ставим сразу, чтобы /me не отдавал пустую строку.
func (r *UserRepo) CreateGuest(ctx context.Context) (domain.User, error) {
	var u domain.User
	err := r.pool.QueryRow(ctx, `
		INSERT INTO users(name, is_guest) VALUES ('Гость', true)
		RETURNING id, COALESCE(email, ''), COALESCE(password_hash, ''),
		          COALESCE(name, ''), is_guest, created_at, updated_at`,
	).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &u.IsGuest, &u.CreatedAt, &u.UpdatedAt)
	return u, err
}

// UpgradeGuest превращает гостя в зарегистрированного, СОХРАНЯЯ id: всё, что
// он успел сделать до регистрации, остаётся при нём. Условие is_guest в WHERE
// защищает от перезаписи настоящего аккаунта — оттуда ErrNotFound.
func (r *UserRepo) UpgradeGuest(ctx context.Context, id uuid.UUID,
	email, passwordHash, name string) (domain.User, error) {
	if name == "" {
		name = "Гость"
	}
	var u domain.User
	err := r.pool.QueryRow(ctx, `
		UPDATE users
		SET email = $2, password_hash = $3, name = $4,
		    is_guest = false, updated_at = now()
		WHERE id = $1 AND is_guest
		RETURNING id, COALESCE(email, ''), COALESCE(password_hash, ''),
		          COALESCE(name, ''), is_guest, created_at, updated_at`,
		id, email, passwordHash, name,
	).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &u.IsGuest, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, ErrNotFound
	}
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return domain.User{}, ErrDuplicateEmail
		}
		return domain.User{}, err
	}
	return u, nil
}

// DeleteStaleGuests убирает брошенных гостей старше olderThan, у которых не
// осталось живой сессии. Без чистки таблица растёт на каждого посетителя, а
// вместе с ней — чаты и результаты по каскаду. Гость с живой сессией не
// трогается, даже если он старше срока: он прямо сейчас в продукте.
func (r *UserRepo) DeleteStaleGuests(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan)
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM users u
		WHERE u.is_guest
		  AND u.created_at < $1
		  AND NOT EXISTS (
		      SELECT 1 FROM sessions s
		      WHERE s.user_id = u.id AND s.expires_at > now())`, cutoff)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
```

- [ ] **Step 6: Добавить SessionRepo.GetSession**

В `backend/internal/repository/session_repo.go` добавить после `GetUserID`:

```go
// GetSession отдаёт владельца сессии вместе с признаком гостя — одним
// запросом, а не двумя: этот вызов стоит на каждом запросе к API, и лишний
// round-trip к БД тут заметен. ErrNotFound на отсутствующей или протухшей.
func (r *SessionRepo) GetSession(ctx context.Context, tokenHash string) (uuid.UUID, bool, error) {
	var userID uuid.UUID
	var isGuest bool
	err := r.pool.QueryRow(ctx, `
		SELECT s.user_id, u.is_guest
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > now()`,
		tokenHash,
	).Scan(&userID, &isGuest)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, ErrNotFound
	}
	return userID, isGuest, err
}
```

- [ ] **Step 7: Прогнать тесты репозитория**

Run: `cd backend && go test ./internal/repository/ -v`
Expected: PASS (или SKIP целиком, если Postgres не поднят — тогда поднять `docker compose up -d db` и прогнать снова; для этой задачи прогон против живой БД обязателен)

- [ ] **Step 8: Прогнать весь набор**

Run: `cd backend && go test ./...`
Expected: PASS

- [ ] **Step 9: Коммит**

```bash
cd /Users/yarik/PycharmProjects/Habitus
git add backend/migrations/0011_guest_users.up.sql backend/migrations/0011_guest_users.down.sql \
        backend/internal/domain/domain.go backend/internal/repository/user_repo.go \
        backend/internal/repository/session_repo.go \
        backend/internal/repository/user_guest_test.go
git commit -m "feat: гость — строка users без учётных данных, апгрейд сохраняет id"
```

---

## Task 3: гостевая сессия и апгрейд в AuthService

**Files:**
- Modify: `backend/internal/service/auth_service.go`
- Create: `backend/internal/service/guest_sweeper.go`
- Create: `backend/internal/service/guest_sweeper_test.go`
- Modify: `backend/internal/config/config.go` (`GuestRetentionDays`, `GuestSessionDays`)
- Modify: `backend/internal/config/config_test.go` (дефолты новых переменных)
- Modify: `.env.example`

**Interfaces:**
- Consumes: `(*repository.UserRepo).CreateGuest`, `.UpgradeGuest`, `.DeleteStaleGuests`; `(*repository.SessionRepo).GetSession` (Task 2)
- Produces: `(*AuthService).Guest(ctx) (domain.User, string, time.Time, error)`; `(*AuthService).UpgradeGuest(ctx, guestID uuid.UUID, email, password, name string) (domain.User, string, time.Time, error)`; `(*AuthService).AuthenticateSession(ctx, token string) (uuid.UUID, bool, error)`; `(*AuthService).SessionUser(ctx, token string) (domain.User, error)`; `service.StartGuestSweeper(ctx, cleaner StaleGuestCleaner, interval, retention time.Duration)`

- [ ] **Step 1: Написать падающий тест на свипер гостей**

Создать `backend/internal/service/guest_sweeper_test.go`:

```go
package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeGuestCleaner struct {
	mu       sync.Mutex
	calls    int
	lastAge  time.Duration
	err      error
	sweeped  chan struct{}
}

func (f *fakeGuestCleaner) DeleteStaleGuests(_ context.Context, olderThan time.Duration) (int64, error) {
	f.mu.Lock()
	f.calls++
	f.lastAge = olderThan
	f.mu.Unlock()
	select {
	case f.sweeped <- struct{}{}:
	default:
	}
	return 1, f.err
}

// Первый проход обязан случиться сразу: после рестарта мусор не должен ждать
// целый интервал — тот же приём, что у StartSessionSweeper.
func TestGuestSweeperRunsImmediately(t *testing.T) {
	cleaner := &fakeGuestCleaner{sweeped: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	StartGuestSweeper(ctx, cleaner, time.Hour, 30*24*time.Hour)

	select {
	case <-cleaner.sweeped:
	case <-time.After(2 * time.Second):
		t.Fatal("первый проход не случился")
	}
	cleaner.mu.Lock()
	defer cleaner.mu.Unlock()
	if cleaner.lastAge != 30*24*time.Hour {
		t.Fatalf("olderThan = %v, ожидалось 720h", cleaner.lastAge)
	}
}

// Моргнувшая БД не должна убивать периодическую задачу до конца жизни
// процесса — та же гарантия, что у sweepSessions.
func TestGuestSweeperSurvivesError(t *testing.T) {
	cleaner := &fakeGuestCleaner{err: errors.New("db down"), sweeped: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	StartGuestSweeper(ctx, cleaner, 20*time.Millisecond, time.Hour)

	deadline := time.After(2 * time.Second)
	for i := 0; i < 2; i++ {
		select {
		case <-cleaner.sweeped:
		case <-deadline:
			t.Fatalf("после ошибки свипер сделал только %d проход(ов)", i)
		}
	}
}

// Неположительный интервал — выключено, а не паника time.NewTicker.
func TestGuestSweeperDisabledOnNonPositiveInterval(t *testing.T) {
	cleaner := &fakeGuestCleaner{sweeped: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	StartGuestSweeper(ctx, cleaner, 0, time.Hour)

	select {
	case <-cleaner.sweeped:
		t.Fatal("свипер запустился при нулевом интервале")
	case <-time.After(100 * time.Millisecond):
	}
}
```

- [ ] **Step 2: Прогнать тест и убедиться, что он падает**

Run: `cd backend && go test ./internal/service/ -run TestGuestSweeper -v`
Expected: FAIL — `undefined: StartGuestSweeper`

- [ ] **Step 3: Реализовать свипер гостей**

Создать `backend/internal/service/guest_sweeper.go`:

```go
// guest_sweeper.go — чистка брошенных гостей. Гостевой аккаунт заводится на
// каждого посетителя без регистрации, и без чистки users растёт линейно по
// трафику, утаскивая за собой чаты и результаты по каскаду.
package service

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
)

// StaleGuestCleaner — то, что умеет вычистить брошенных гостей старше срока.
type StaleGuestCleaner interface {
	DeleteStaleGuests(ctx context.Context, olderThan time.Duration) (int64, error)
}

// StartGuestSweeper: первый проход сразу, дальше по тикеру, остановка вместе
// с контекстом. Неположительный интервал — выключено (time.NewTicker на таком
// паникует, а ронять процесс из-за кривой переменной окружения незачем).
func StartGuestSweeper(ctx context.Context, cleaner StaleGuestCleaner,
	interval, retention time.Duration) {
	if interval <= 0 {
		log.Warn().Dur("interval", interval).
			Msg("guest sweeper disabled: interval must be positive")
		return
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			sweepGuests(ctx, cleaner, retention)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func sweepGuests(ctx context.Context, cleaner StaleGuestCleaner, retention time.Duration) {
	removed, err := cleaner.DeleteStaleGuests(ctx, retention)
	if err != nil {
		log.Error().Err(err).Msg("stale guest sweep failed")
		return
	}
	if removed > 0 {
		log.Info().Int64("removed", removed).Msg("stale guests swept")
	}
}
```

- [ ] **Step 4: Прогнать тест и убедиться, что он проходит**

Run: `cd backend && go test ./internal/service/ -run TestGuestSweeper -v`
Expected: PASS

- [ ] **Step 5: Расширить AuthService**

В `backend/internal/service/auth_service.go`:

Заменить константу TTL на пару:

```go
const sessionTTL = 30 * 24 * time.Hour

// guestSessionTTL короче обычного: гостевая сессия — это «дай посмотреть»,
// а не долгий вход, и месяц жизни у неё держал бы мусорные строки в users.
const guestSessionTTL = 7 * 24 * time.Hour
```

Обобщить `createSession`, добавив длительность параметром:

```go
func (s *AuthService) createSession(ctx context.Context, userID uuid.UUID,
	ttl time.Duration) (token string, expiresAt time.Time, err error) {
	token, err = newOpaqueToken()
	if err != nil {
		return "", time.Time{}, err
	}
	expiresAt = time.Now().Add(ttl)
	if err = s.sessions.Create(ctx, hashToken(token), userID, expiresAt); err != nil {
		return "", time.Time{}, err
	}
	return token, expiresAt, nil
}
```

В `Register` и `Login` заменить вызовы на `s.createSession(ctx, u.ID, sessionTTL)`.

Добавить в конец файла:

```go
// Guest заводит анонимного пользователя и сессию под него — чтобы первый
// поиск случился без регистрации. Стена перед первым поиском стоит ровно
// там, где у продукта единственный шанс показать ценность.
func (s *AuthService) Guest(ctx context.Context) (domain.User, string, time.Time, error) {
	u, err := s.users.CreateGuest(ctx)
	if err != nil {
		return domain.User{}, "", time.Time{}, err
	}
	token, expiresAt, err := s.createSession(ctx, u.ID, guestSessionTTL)
	return u, token, expiresAt, err
}

// UpgradeGuest регистрирует гостя, не меняя его id: чаты, результаты поиска и
// избранное остаются при нём. Новая сессия выдаётся с обычным TTL, старая
// гостевая живёт до истечения — отзывать её незачем, она указывает на того же
// пользователя.
func (s *AuthService) UpgradeGuest(ctx context.Context, guestID uuid.UUID,
	email, password, name string) (domain.User, string, time.Time, error) {
	if len(password) < 8 {
		return domain.User{}, "", time.Time{}, apperr.Validation("пароль должен быть не короче 8 символов")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return domain.User{}, "", time.Time{}, err
	}
	u, err := s.users.UpgradeGuest(ctx, guestID, email, string(hash), name)
	switch {
	case errors.Is(err, repository.ErrDuplicateEmail):
		return domain.User{}, "", time.Time{}, apperr.Validation("email уже зарегистрирован")
	case errors.Is(err, repository.ErrNotFound):
		// Сессия принадлежит не гостю: регистрировать поверх живого аккаунта
		// нельзя, но и 500 это не — пусть выйдет и зарегистрируется заново.
		return domain.User{}, "", time.Time{}, apperr.Validation("вы уже вошли в аккаунт — выйдите, чтобы зарегистрировать новый")
	case err != nil:
		return domain.User{}, "", time.Time{}, err
	}
	token, expiresAt, err := s.createSession(ctx, u.ID, sessionTTL)
	return u, token, expiresAt, err
}

// AuthenticateSession — то же, что Authenticate, плюс признак гостя. Нужен
// middleware, чтобы ручки, закрытые для анонимов, отличали одного от другого
// без второго похода в БД.
func (s *AuthService) AuthenticateSession(ctx context.Context, token string) (uuid.UUID, bool, error) {
	userID, isGuest, err := s.sessions.GetSession(ctx, hashToken(token))
	if errors.Is(err, repository.ErrNotFound) {
		return uuid.Nil, false, apperr.Unauthorized()
	}
	return userID, isGuest, err
}

// SessionUser отдаёт пользователя по токену сессии. Нужен регистрации: она
// смотрит, не гость ли пришёл, ещё до того как хендлер решит — заводить
// нового пользователя или апгрейдить текущего.
func (s *AuthService) SessionUser(ctx context.Context, token string) (domain.User, error) {
	userID, _, err := s.AuthenticateSession(ctx, token)
	if err != nil {
		return domain.User{}, err
	}
	return s.users.GetByID(ctx, userID)
}
```

Оставить существующий `Authenticate` как есть — он больше не нужен middleware, но его удаление не входит в задачу; если после Task 4 на него не останется вызовов, удалить там.

- [ ] **Step 6: Добавить переменные конфигурации**

В `backend/internal/config/config.go` добавить в `Settings`:

```go
	// GuestRetentionDays — через сколько дней брошенный гость (без живой
	// сессии) вычищается вместе со своими чатами.
	GuestRetentionDays int
	// GuestSweepMinutes — как часто крутить чистку гостей.
	GuestSweepMinutes int
	// RateLimitLLMGuestPerHour — отдельный, более скупой потолок LLM-ручек для
	// гостя: гостевой аккаунт заводится в один запрос, поэтому общий лимит по
	// user_id его не сдерживает.
	RateLimitLLMGuestPerHour int
```

и в `Load()`:

```go
		GuestRetentionDays:       getenvInt("GUEST_RETENTION_DAYS", 30),
		GuestSweepMinutes:        getenvInt("GUEST_SWEEP_MINUTES", 720),
		RateLimitLLMGuestPerHour: getenvInt("RATE_LIMIT_LLM_GUEST_PER_HOUR", 5),
```

- [ ] **Step 7: Дописать тест конфигурации**

В `backend/internal/config/config_test.go` дописать:

```go
func TestLoadGuestDefaults(t *testing.T) {
	cfg := Load()

	if cfg.GuestRetentionDays != 30 {
		t.Fatalf("GuestRetentionDays = %d, ожидалось 30", cfg.GuestRetentionDays)
	}
	if cfg.GuestSweepMinutes != 720 {
		t.Fatalf("GuestSweepMinutes = %d, ожидалось 720", cfg.GuestSweepMinutes)
	}
	// Лимит гостя обязан быть строго меньше общего: иначе анонимный трафик
	// жжёт бюджет LLM наравне с зарегистрированным.
	if cfg.RateLimitLLMGuestPerHour >= cfg.RateLimitLLMPerHour {
		t.Fatalf("лимит гостя (%d) не меньше общего (%d)",
			cfg.RateLimitLLMGuestPerHour, cfg.RateLimitLLMPerHour)
	}
}
```

- [ ] **Step 8: Дописать .env.example**

Добавить в `.env.example`:

```bash
# Гость: первый поиск без регистрации. Брошенные гости (без живой сессии)
# вычищаются вместе с чатами через GUEST_RETENTION_DAYS дней.
GUEST_RETENTION_DAYS=30
GUEST_SWEEP_MINUTES=720
# Отдельный потолок LLM-ручек для гостя — строго меньше RATE_LIMIT_LLM_PER_HOUR.
RATE_LIMIT_LLM_GUEST_PER_HOUR=5
```

- [ ] **Step 9: Прогнать весь набор**

Run: `cd backend && go test ./...`
Expected: PASS

- [ ] **Step 10: Коммит**

```bash
cd /Users/yarik/PycharmProjects/Habitus
git add backend/internal/service/auth_service.go backend/internal/service/guest_sweeper.go \
        backend/internal/service/guest_sweeper_test.go backend/internal/config/config.go \
        backend/internal/config/config_test.go .env.example
git commit -m "feat: гостевая сессия, апгрейд в аккаунт и чистка брошенных гостей"
```

---

## Task 4: гость в HTTP — вход, апгрейд, границы

Гость получает сессию через `POST /auth/guest`, ищет, а на действиях, где анонимность бессмысленна (кабинет продавца, отправка заявки), упирается в честный 403 с предложением зарегистрироваться. Регистрация из-под гостевой сессии апгрейдит текущего пользователя, а не заводит нового.

**Files:**
- Modify: `backend/internal/http/middleware/auth.go`
- Create: `backend/internal/http/middleware/registered.go`
- Create: `backend/internal/http/middleware/registered_test.go`
- Modify: `backend/internal/http/middleware/ratelimit.go` (выбор лимита по типу аккаунта)
- Modify: `backend/internal/http/middleware/ratelimit_test.go`
- Modify: `backend/internal/http/handlers/auth_handler.go`
- Modify: `backend/internal/http/router.go`
- Modify: `backend/internal/app/app.go`
- Modify: `backend/cmd/api/main.go`
- Modify: `backend/internal/apperr/apperr.go`
- Modify: `frontend/Пайплайн фронт.md`

**Interfaces:**
- Consumes: `(*service.AuthService).Guest`, `.UpgradeGuest`, `.AuthenticateSession`, `.SessionUser` (Task 3)
- Produces: `middleware.IsGuestLocalsKey`; `middleware.IsGuest(c *fiber.Ctx) bool`; `middleware.RequireRegistered() fiber.Handler`; `middleware.RateLimitLLM(registered, guest *RateLimiter) fiber.Handler`; `apperr.GuestForbidden()`; `POST /api/v1/auth/guest`

- [ ] **Step 1: Написать падающий тест на RequireRegistered**

Создать `backend/internal/http/middleware/registered_test.go`:

```go
package middleware

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func newRegisteredApp(isGuest bool) *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: ErrorHandler})
	app.Get("/protected", func(c *fiber.Ctx) error {
		c.Locals(UserIDLocalsKey, uuid.New())
		c.Locals(IsGuestLocalsKey, isGuest)
		return c.Next()
	}, RequireRegistered(), func(c *fiber.Ctx) error {
		return c.SendStatus(fiber.StatusNoContent)
	})
	return app
}

func TestRequireRegisteredLetsRegisteredThrough(t *testing.T) {
	resp, err := newRegisteredApp(false).Test(httptest.NewRequest("GET", "/protected", nil))
	if err != nil {
		t.Fatalf("запрос: %v", err)
	}
	if resp.StatusCode != fiber.StatusNoContent {
		t.Fatalf("статус = %d, ожидался 204", resp.StatusCode)
	}
}

// Гостю тут нельзя, но отказ обязан объяснять, что делать: 403 с кодом
// guest_forbidden — это точка регистрации, а не поломка.
func TestRequireRegisteredBlocksGuestWithActionableCode(t *testing.T) {
	resp, err := newRegisteredApp(true).Test(httptest.NewRequest("GET", "/protected", nil))
	if err != nil {
		t.Fatalf("запрос: %v", err)
	}
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("статус = %d, ожидался 403", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var got struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("разбор конверта: %v (%s)", err, body)
	}
	if got.Error.Code != "guest_forbidden" {
		t.Fatalf("code = %q, ожидался guest_forbidden", got.Error.Code)
	}
	if got.Error.Message == "" {
		t.Fatal("пустое сообщение: гость не поймёт, что от него хотят")
	}
}
```

- [ ] **Step 2: Прогнать тест и убедиться, что он падает**

Run: `cd backend && go test ./internal/http/middleware/ -run TestRequireRegistered -v`
Expected: FAIL — `undefined: IsGuestLocalsKey`, `undefined: RequireRegistered`

- [ ] **Step 3: Добавить код ошибки**

В `backend/internal/apperr/apperr.go` добавить:

```go
// GuestForbidden — гостю сюда нельзя. Не 401: сессия у него настоящая, дело
// в том, что действие требует аккаунта, на который можно ответить.
func GuestForbidden(message string) *Error {
	return New(http.StatusForbidden, "guest_forbidden", message)
}
```

- [ ] **Step 4: Реализовать RequireRegistered**

Создать `backend/internal/http/middleware/registered.go`:

```go
package middleware

import (
	"github.com/gofiber/fiber/v2"

	"habitus-backend/internal/apperr"
)

// RequireRegistered закрывает ручки, где анонимный пользователь бессмыслен:
// кабинет продавца — объявление должно кому-то принадлежать. Ставится ПОСЛЕ
// Auth: тот кладёт признак гостя в Locals.
//
// На заявке этого middleware НЕТ намеренно: там гостю не отказывают, а заводят
// аккаунт тем же запросом (см. LeadHandler.Send) — отдельный редирект на
// регистрацию терял бы заполненную форму.
func RequireRegistered() fiber.Handler {
	return func(c *fiber.Ctx) error {
		if IsGuest(c) {
			return apperr.GuestForbidden(
				"Зарегистрируйтесь, чтобы продолжить — сохранённые поиски останутся при вас")
		}
		return c.Next()
	}
}
```

- [ ] **Step 5: Прокинуть признак гостя через Auth**

В `backend/internal/http/middleware/auth.go` заменить содержимое на:

```go
package middleware

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/service"
)

const SessionCookieName = "habitus_session"
const UserIDLocalsKey = "user_id"
const IsGuestLocalsKey = "is_guest"

// Auth reads the session cookie (see plan §7 — real cookie-session, chosen
// over Authorization: Bearer specifically because the browser EventSource
// used for SSE can't send custom headers) and stores the authenticated
// user_id in fiber.Locals for downstream handlers.
//
// Признак гостя кладётся рядом и берётся тем же запросом, что и user_id:
// RequireRegistered и рейт-лимит спрашивают его на каждом запросе, и второй
// поход в БД ради одного булева поля тут не оправдан.
func Auth(auth *service.AuthService) fiber.Handler {
	return func(c *fiber.Ctx) error {
		token := c.Cookies(SessionCookieName)
		if token == "" {
			return apperr.Unauthorized()
		}
		userID, isGuest, err := auth.AuthenticateSession(c.Context(), token)
		if err != nil {
			return err
		}
		c.Locals(UserIDLocalsKey, userID)
		c.Locals(IsGuestLocalsKey, isGuest)
		return c.Next()
	}
}

func UserID(c *fiber.Ctx) uuid.UUID {
	id, _ := c.Locals(UserIDLocalsKey).(uuid.UUID)
	return id
}

// IsGuest — аккаунт без учётных данных. Отсутствие значения трактуется как
// «не гость»: ручки вне authMw про гостей ничего не знают и не должны
// внезапно отказывать.
func IsGuest(c *fiber.Ctx) bool {
	v, _ := c.Locals(IsGuestLocalsKey).(bool)
	return v
}
```

- [ ] **Step 6: Прогнать тест RequireRegistered**

Run: `cd backend && go test ./internal/http/middleware/ -run TestRequireRegistered -v`
Expected: PASS

- [ ] **Step 7: Написать падающий тест на раздельный лимит**

Дописать в `backend/internal/http/middleware/ratelimit_test.go`:

```go
func TestRateLimitLLMAppliesGuestBudget(t *testing.T) {
	registered := NewRateLimiter(10, time.Hour)
	guest := NewRateLimiter(1, time.Hour)

	app := fiber.New(fiber.Config{ErrorHandler: ErrorHandler})
	userID := uuid.New()
	app.Get("/llm", func(c *fiber.Ctx) error {
		c.Locals(UserIDLocalsKey, userID)
		c.Locals(IsGuestLocalsKey, true)
		return c.Next()
	}, RateLimitLLM(registered, guest), func(c *fiber.Ctx) error {
		return c.SendStatus(fiber.StatusNoContent)
	})

	first, err := app.Test(httptest.NewRequest("GET", "/llm", nil))
	if err != nil {
		t.Fatalf("первый запрос: %v", err)
	}
	if first.StatusCode != fiber.StatusNoContent {
		t.Fatalf("первый статус = %d, ожидался 204", first.StatusCode)
	}

	// Второй должен упереться в гостевой лимит (1), а не в общий (10).
	second, err := app.Test(httptest.NewRequest("GET", "/llm", nil))
	if err != nil {
		t.Fatalf("второй запрос: %v", err)
	}
	if second.StatusCode != fiber.StatusTooManyRequests {
		t.Fatalf("второй статус = %d, ожидался 429", second.StatusCode)
	}
}
```

Добавить недостающие импорты в этот файл: `"time"`, `"net/http/httptest"`, `"github.com/gofiber/fiber/v2"`, `"github.com/google/uuid"` — если их там ещё нет.

- [ ] **Step 8: Прогнать тест и убедиться, что он падает**

Run: `cd backend && go test ./internal/http/middleware/ -run TestRateLimitLLMAppliesGuestBudget -v`
Expected: FAIL — `too many arguments in call to RateLimitLLM`

- [ ] **Step 9: Разделить лимит по типу аккаунта**

В `backend/internal/http/middleware/ratelimit.go` заменить сигнатуру `RateLimitLLM`:

```go
// RateLimitLLM — middleware для LLM-ручек (POST .../messages/stream и
// .../ask/stream). Лимитов два: гостевой аккаунт заводится одним запросом,
// поэтому общий потолок по user_id его не сдерживает — анонимный трафик жёг
// бы бюджет OpenRouter кратно. guest == nil означает «гостям тот же лимит».
func RateLimitLLM(registered, guest *RateLimiter) fiber.Handler {
	return func(c *fiber.Ctx) error {
		limiter := registered
		if guest != nil && IsGuest(c) {
			limiter = guest
		}
		allowed, retryAfter := limiter.Allow(UserID(c))
		if !allowed {
			observability.Default.IncRateLimited()
			c.Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
			return apperr.RateLimited(fmt.Sprintf(
				"Превышен лимит запросов к ИИ (%d в час). Попробуйте снова через %d мин.",
				limiter.Limit(), int(retryAfter.Minutes())+1))
		}
		return c.Next()
	}
}
```

Сигнатура изменилась, поэтому существующий вызов в `backend/internal/http/middleware/ratelimit_test.go:139` перестанет компилироваться — поправить его на:

```go
	app.Post("/probe", RateLimitLLM(rl, nil), func(c *fiber.Ctx) error {
```

`nil` вторым аргументом означает «гостям тот же лимит» и сохраняет смысл того теста.

Сохранить существующий текст сообщения и логику `Retry-After` — меняется только выбор лимитера и источник числа в сообщении. Если у `RateLimiter` нет геттера предела, добавить:

```go
// Limit — предел лимитера, нужен сообщению об отказе: пользователь должен
// видеть тот потолок, в который упёрся именно он, а не общий.
func (r *RateLimiter) Limit() int { return r.limit }
```

- [ ] **Step 10: Прогнать тесты middleware**

Run: `cd backend && go test ./internal/http/middleware/ -v`
Expected: PASS

- [ ] **Step 11: Написать падающий тест на POST /auth/guest и апгрейд**

Создать `backend/internal/http/handlers/auth_guest_test.go`:

```go
package handlers

import (
	"encoding/json"
	"testing"
)

// Тест формы ответа: гостевой вход отдаёт то же тело, что и login/register,
// плюс явный признак — фронт по нему решает, показывать ли «Зарегистрируйтесь».
func TestGuestResponseShape(t *testing.T) {
	body := guestResponseBody("11111111-1111-1111-1111-111111111111", "Гость")

	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"id", "email", "name", "is_guest"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("в ответе нет поля %q", key)
		}
	}
	if got["is_guest"] != true {
		t.Fatalf("is_guest = %v, ожидалось true", got["is_guest"])
	}
	if got["email"] != "" {
		t.Fatalf("email = %v, у гостя его нет", got["email"])
	}
}
```

- [ ] **Step 12: Прогнать тест и убедиться, что он падает**

Run: `cd backend && go test ./internal/http/handlers/ -run TestGuestResponseShape -v`
Expected: FAIL — `undefined: guestResponseBody`

- [ ] **Step 13: Реализовать ручку гостя и апгрейд при регистрации**

В `backend/internal/http/handlers/auth_handler.go` добавить импорт `"github.com/google/uuid"` и заменить `Register`, а также добавить `Guest` и хелпер:

```go
// userResponseBody — общая форма ответа auth-ручек. is_guest отдаётся всегда,
// а не только гостю: фронту нужно знать тип аккаунта на каждом входе, чтобы
// решать, показывать ли призыв зарегистрироваться.
func userResponseBody(id any, email, name string, isGuest bool) fiber.Map {
	return fiber.Map{"id": id, "email": email, "name": name, "is_guest": isGuest}
}

func guestResponseBody(id any, name string) fiber.Map {
	return userResponseBody(id, "", name, true)
}

// Guest implements POST /auth/guest — сессия без регистрации под первый поиск.
// Если сессия уже есть и она живая, новый гость НЕ заводится: иначе перезагрузка
// вкладки плодила бы пользователей и теряла историю поиска.
func (h *AuthHandler) Guest(c *fiber.Ctx) error {
	if token := c.Cookies(middleware.SessionCookieName); token != "" {
		if u, err := h.auth.SessionUser(c.Context(), token); err == nil {
			return c.JSON(userResponseBody(u.ID, u.Email, u.Name, u.IsGuest))
		}
	}
	u, token, expiresAt, err := h.auth.Guest(c.Context())
	if err != nil {
		return err
	}
	h.setSessionCookie(c, token, expiresAt)
	return c.Status(fiber.StatusCreated).JSON(guestResponseBody(u.ID, u.Name))
}

func (h *AuthHandler) Register(c *fiber.Ctx) error {
	var req registerRequest
	if err := c.BodyParser(&req); err != nil || req.Email == "" || req.Password == "" {
		return apperr.Validation("email и password обязательны")
	}

	// Регистрация из-под гостевой сессии — это АПГРЕЙД той же строки users,
	// а не новый пользователь: иначе всё, что человек успел найти и сохранить
	// до регистрации, осталось бы на брошенном аккаунте.
	var guestID uuid.UUID
	if token := c.Cookies(middleware.SessionCookieName); token != "" {
		if u, err := h.auth.SessionUser(c.Context(), token); err == nil && u.IsGuest {
			guestID = u.ID
		}
	}

	var (
		u         domain.User
		token     string
		expiresAt time.Time
		err       error
	)
	if guestID != uuid.Nil {
		u, token, expiresAt, err = h.auth.UpgradeGuest(c.Context(), guestID, req.Email, req.Password, req.Name)
	} else {
		u, token, expiresAt, err = h.auth.Register(c.Context(), req.Email, req.Password, req.Name)
	}
	if err != nil {
		return err
	}
	h.setSessionCookie(c, token, expiresAt)
	return c.Status(fiber.StatusCreated).JSON(userResponseBody(u.ID, u.Email, u.Name, false))
}
```

Добавить импорт `"habitus-backend/internal/domain"` в этот файл.

Привести `Login` и `Me` к общей форме — заменить их тела возврата на:

```go
	return c.JSON(userResponseBody(u.ID, u.Email, u.Name, u.IsGuest))
```

- [ ] **Step 14: Прогнать тест и убедиться, что он проходит**

Run: `cd backend && go test ./internal/http/handlers/ -run TestGuestResponseShape -v`
Expected: PASS

- [ ] **Step 15: Подключить маршрут и границы в роутере**

В `backend/internal/http/router.go`:

Заменить сигнатуру на приём готового middleware регистрации:

```go
func RegisterRoutes(app *fiber.App, h Handlers, authSvc *service.AuthService, rateLimitLLM fiber.Handler) {
```

оставить как есть, а внутри добавить после `api.Post("/auth/login", ...)`:

```go
	// Гостевая сессия: первый поиск без регистрации. Стена перед первым
	// поиском стояла ровно там, где у продукта единственный шанс показать
	// ценность, — поэтому её здесь нет.
	api.Post("/auth/guest", h.Auth.Guest)
```

и обернуть группу кабинета продавца:

```go
	// Кабинет закрыт для гостей: объявление должно принадлежать аккаунту,
	// который переживёт чистку брошенных гостей.
	ownerGroup := api.Group("/owner", authMw, middleware.RequireRegistered())
```

- [ ] **Step 16: Прокинуть два лимитера в app.New**

В `backend/internal/app/app.go` заменить сборку лимитера:

```go
	rateLimitPerHour := cfg.RateLimitLLMPerHour
	if rateLimitPerHour <= 0 {
		rateLimitPerHour = rateLimitPerHourDef
	}
	// Гостевой потолок: тот же приём fallback'а. Ноль/отрицательное значение
	// в конфиге означает «конфиг не задан» (например, тест собирает
	// config.Settings{} напрямую) — берём дефолт, а не «ничего не пропускать».
	guestPerHour := cfg.RateLimitLLMGuestPerHour
	if guestPerHour <= 0 {
		guestPerHour = rateLimitGuestPerHourDef
	}
	rateLimiter := middleware.NewRateLimiter(rateLimitPerHour, time.Hour)
	guestLimiter := middleware.NewRateLimiter(guestPerHour, time.Hour)
```

добавить константу рядом с `rateLimitPerHourDef`:

```go
	rateLimitGuestPerHourDef = 5
```

и в вызове `RegisterRoutes` передать:

```go
	}, svc.Auth, middleware.RateLimitLLM(rateLimiter, guestLimiter))
```

- [ ] **Step 17: Запустить свипер гостей в main.go**

В `backend/cmd/api/main.go` после `service.StartSessionSweeper(...)` добавить:

```go
	service.StartGuestSweeper(ctx, userRepo,
		time.Duration(cfg.GuestSweepMinutes)*time.Minute,
		time.Duration(cfg.GuestRetentionDays)*24*time.Hour)
```

- [ ] **Step 18: Дописать контракт для фронта**

В `frontend/Пайплайн фронт.md` добавить раздел после описания login:

```markdown
### Гостевая сессия

- **Эндпоинт:** `POST /api/v1/auth/guest`
- **Тело:** пустое
- **Ответ 201:** `{"id": uuid, "email": "", "name": "Гость", "is_guest": true}`
- Если валидная сессия уже есть — новый гость не создаётся, приходит **200** с
  текущим пользователем (в том числе зарегистрированным).
- Кука сессии ставится та же (`habitus_session`), TTL — 7 дней против 30 у
  зарегистрированного.

Все auth-ручки (`/auth/guest`, `/auth/register`, `/auth/login`, `/me`) отдают
единую форму `{id, email, name, is_guest}`.

**Регистрация из-под гостя** (`POST /auth/register` с живой гостевой кукой) —
это апгрейд того же пользователя: `id` не меняется, чаты, результаты поиска и
избранное остаются при нём. Ответ `is_guest: false`.

**Что гостю недоступно:** кабинет продавца (`/api/v1/owner/*`). Отказ приходит
как **403** `{"error":{"code":"guest_forbidden","message":"Зарегистрируйтесь,
чтобы продолжить — сохранённые поиски останутся при вас"}}` — это точка
регистрации, показывать её надо формой, а не ошибкой.

**Заявка гостю доступна** — аккаунт заводится тем же запросом, см. раздел
«Заявка продавцу».

**Лимит LLM у гостя отдельный** — `RATE_LIMIT_LLM_GUEST_PER_HOUR` (дефолт 5)
против 30 у зарегистрированного. Тот же конверт 429 с `Retry-After`.
```

- [ ] **Step 19: Прогнать весь набор**

Run: `cd backend && go test ./...`
Expected: PASS

- [ ] **Step 20: Проверить сквозной путь гостя вручную**

```bash
docker compose up -d db
cd backend && go build ./... && cd ..
# в отдельном окне поднять стек целиком, затем:
curl -i -c /tmp/habitus-guest.txt -X POST localhost:8080/api/v1/auth/guest
# ожидается 201 и {"is_guest":true}

curl -s -b /tmp/habitus-guest.txt localhost:8080/api/v1/me | jq
# ожидается is_guest: true

curl -i -b /tmp/habitus-guest.txt localhost:8080/api/v1/owner/listings
# ожидается 403 guest_forbidden

curl -i -b /tmp/habitus-guest.txt -c /tmp/habitus-guest.txt \
  -X POST localhost:8080/api/v1/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"email":"upgrade@example.test","password":"password1","name":"Покупатель"}'
# ожидается 201, тот же id, что вернул /auth/guest, is_guest: false
```

- [ ] **Step 21: Коммит**

```bash
cd /Users/yarik/PycharmProjects/Habitus
git add backend/internal/http/middleware/auth.go backend/internal/http/middleware/registered.go \
        backend/internal/http/middleware/registered_test.go \
        backend/internal/http/middleware/ratelimit.go backend/internal/http/middleware/ratelimit_test.go \
        backend/internal/http/handlers/auth_handler.go backend/internal/http/handlers/auth_guest_test.go \
        backend/internal/http/router.go backend/internal/app/app.go backend/cmd/api/main.go \
        backend/internal/apperr/apperr.go "frontend/Пайплайн фронт.md"
git commit -m "feat: первый поиск без регистрации — гостевая сессия и апгрейд в аккаунт"
```

---

## Task 5: паспорт называет, как связаться

Сейчас `ObjectPassport` — тупик: ни ссылки на источник, ни продавца, ни действия. Добавляем блок `contact`, который прямо говорит фронту, какую кнопку рисовать: `lead` — объявление ведёт продавец в кабинете, показываем форму заявки; `external` — витринный объект с Циана, показываем уход по `source_url`; `none` — связаться нечем, кнопки нет (и это честнее выдуманной).

**Files:**
- Modify: `backend/internal/domain/domain.go` (`Listing.SourceURL`)
- Modify: `backend/internal/repository/listing_repo.go` (обе выборки + `scanListing`)
- Modify: `backend/internal/service/object_service.go`
- Create: `backend/internal/service/object_contact_test.go`
- Modify: `frontend/Пайплайн фронт.md`

**Interfaces:**
- Consumes: `(*repository.OwnerListingRepo).GetByExternalID(ctx, externalID) (domain.OwnerListing, error)`
- Produces: `domain.Listing.SourceURL *string`; `service.PassportContact{Kind string; SourceURL string}`; `service.ObjectPassport.Contact PassportContact`; `service.BuildPassportContact(owner domain.OwnerListing, ownerFound bool, l domain.Listing) PassportContact`; константы `service.ContactKindLead = "lead"`, `ContactKindExternal = "external"`, `ContactKindNone = "none"`; конструктор `NewObjectService` принимает `owners *repository.OwnerListingRepo` седьмым аргументом

- [ ] **Step 1: Написать падающий тест на выбор способа связи**

Создать `backend/internal/service/object_contact_test.go`:

```go
package service

import (
	"testing"

	"habitus-backend/internal/domain"
)

// strp — существующий хелпер пакета (display_fields_test.go), свой не заводим.

// Объявление ведёт продавец в кабинете и оно опубликовано — значит есть кому
// перезвонить, показываем форму заявки.
func TestContactIsLeadForPublishedOwnerListing(t *testing.T) {
	got := BuildPassportContact(
		domain.OwnerListing{Status: "published"}, true,
		domain.Listing{SourceURL: strp("https://www.cian.ru/sale/flat/1/")})

	if got.Kind != ContactKindLead {
		t.Fatalf("kind = %q, ожидался lead", got.Kind)
	}
	// Ссылка на источник в режиме заявки не отдаётся: увести покупателя на
	// Циан мимо продавца, который завёл объявление здесь, — прямой вред.
	if got.SourceURL != "" {
		t.Fatalf("source_url = %q, при заявке его быть не должно", got.SourceURL)
	}
}

// Черновик и снятое с публикации заявки не принимают: продавец сам их скрыл.
func TestContactIsNotLeadForUnpublishedOwnerListing(t *testing.T) {
	for _, status := range []string{"draft", "publishing", "unpublished", "failed"} {
		got := BuildPassportContact(domain.OwnerListing{Status: status}, true, domain.Listing{})
		if got.Kind == ContactKindLead {
			t.Fatalf("статус %q принимает заявки, а не должен", status)
		}
	}
}

func TestContactIsExternalForShowcaseListing(t *testing.T) {
	got := BuildPassportContact(domain.OwnerListing{}, false,
		domain.Listing{SourceURL: strp("https://www.cian.ru/sale/flat/318394906/")})

	if got.Kind != ContactKindExternal {
		t.Fatalf("kind = %q, ожидался external", got.Kind)
	}
	if got.SourceURL != "https://www.cian.ru/sale/flat/318394906/" {
		t.Fatalf("source_url = %q", got.SourceURL)
	}
}

// Ни продавца, ни ссылки — честное «связаться нечем». Выдуманная кнопка тут
// хуже отсутствия кнопки: она ведёт в никуда.
func TestContactIsNoneWithoutSellerOrSource(t *testing.T) {
	got := BuildPassportContact(domain.OwnerListing{}, false, domain.Listing{})

	if got.Kind != ContactKindNone {
		t.Fatalf("kind = %q, ожидался none", got.Kind)
	}
	if got.SourceURL != "" {
		t.Fatalf("source_url = %q, ожидалась пустая строка", got.SourceURL)
	}
}

// Пустой source_url в витрине — не ссылка, а отсутствие ссылки.
func TestContactIsNoneOnEmptySourceURL(t *testing.T) {
	got := BuildPassportContact(domain.OwnerListing{}, false,
		domain.Listing{SourceURL: strp("")})

	if got.Kind != ContactKindNone {
		t.Fatalf("kind = %q, ожидался none", got.Kind)
	}
}
```

- [ ] **Step 2: Прогнать тест и убедиться, что он падает**

Run: `cd backend && go test ./internal/service/ -run TestContact -v`
Expected: FAIL — `undefined: BuildPassportContact`, `unknown field SourceURL in domain.Listing`

- [ ] **Step 3: Добавить source_url в проекцию витрины**

В `backend/internal/domain/domain.go` в структуру `Listing` добавить после `MetroStation`:

```go
	// SourceURL — страница объявления у источника. Нужен паспорту: для
	// витринного объекта продавца в системе нет, и единственный способ
	// связаться — уйти на источник. nil и "" одинаково означают «ссылки нет».
	SourceURL *string
```

В `backend/internal/repository/listing_repo.go` в `scanListing` добавить `&l.SourceURL` после `&l.MetroStation`:

```go
	err := rows.Scan(&l.ExternalID, &l.Price, &l.Area, &l.Rooms, &l.Level, &l.Levels,
		&l.Lon, &l.Lat, &l.Address, &l.MetroStation, &l.SourceURL, &l.Photos,
		&l.WalkMinSchool, &l.WalkMinMetro, &l.WalkMinPark, &l.BarDensity500m, &l.NoiseLevel)
```

и в **обоих** запросах, которые используют `scanListing` (`GetByExternalIDs` и `ListInBBox`), добавить колонку в том же месте списка:

```sql
		SELECT external_id, price, area, rooms, level, levels,
		       ST_X(geom), ST_Y(geom), address, metro_station, source_url, photos,
		       walk_min_school, walk_min_metro, walk_min_park,
		       bar_density_500m, noise_level
```

- [ ] **Step 4: Реализовать выбор способа связи**

В `backend/internal/service/object_service.go` добавить рядом с остальными DTO паспорта:

```go
// Способы связаться с объектом. Ровно один из трёх — фронт по нему решает,
// какую кнопку рисовать, и не гадает по косвенным признакам.
const (
	ContactKindLead     = "lead"     // объявление продавца в кабинете — форма заявки
	ContactKindExternal = "external" // витринный объект — уход на источник
	ContactKindNone     = "none"     // связаться нечем
)

// PassportContact — единственное действие, которое паспорт предлагает
// пользователю. До его появления путь обрывался на «вот красивое досье».
type PassportContact struct {
	Kind string `json:"kind"`
	// SourceURL заполняется только при kind == external.
	SourceURL string `json:"source_url,omitempty"`
}

// BuildPassportContact. Приоритет у продавца в системе: если объявление ведут
// в кабинете, уводить покупателя на Циан мимо него — прямой вред. Заявки
// принимает только опубликованное объявление: черновик и снятое с витрины
// продавец скрыл сознательно.
func BuildPassportContact(owner domain.OwnerListing, ownerFound bool, l domain.Listing) PassportContact {
	if ownerFound && owner.Status == "published" {
		return PassportContact{Kind: ContactKindLead}
	}
	if l.SourceURL != nil && *l.SourceURL != "" {
		return PassportContact{Kind: ContactKindExternal, SourceURL: *l.SourceURL}
	}
	return PassportContact{Kind: ContactKindNone}
}
```

Добавить поле в `ObjectPassport`:

```go
type ObjectPassport struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	Address           string            `json:"address"`
	Price             *int64            `json:"price"`
	Rooms             *int              `json:"rooms"`
	AreaSqm           *float64          `json:"area_sqm"`
	Floor             string            `json:"floor"`
	Images            []string          `json:"images"`
	Coordinates       []float64         `json:"coordinates"`
	Contact           PassportContact   `json:"contact"`
	LifestyleAnalysis LifestyleAnalysis `json:"lifestyle_analysis"`
}
```

- [ ] **Step 5: Подключить поиск продавца в ObjectService**

В `backend/internal/service/object_service.go` рядом с `listingSource` добавить интерфейс и поле:

```go
// ownerLookup — часть OwnerListingRepo, нужная паспорту: узнать, ведёт ли
// объект продавец в кабинете. Обособленный интерфейс — чтобы тест мог
// подставить «продавца нет» без реальной БД.
type ownerLookup interface {
	GetByExternalID(ctx context.Context, externalID string) (domain.OwnerListing, error)
}
```

В структуру `ObjectService` добавить `owners ownerLookup`, в конструктор — параметр:

```go
func NewObjectService(chats *ChatService, results *repository.ChatSearchRepo,
	listings *repository.ListingRepo, owners *repository.OwnerListingRepo,
	ml *client.MLClient, mlTimeout time.Duration, ttlHours int) *ObjectService {
	return &ObjectService{chats: chats, results: results, listings: listings,
		owners: owners, ml: ml, mlTimeout: mlTimeout, ttlHours: ttlHours,
		inFlight: make(map[string]*dossierCall)}
}
```

Добавить метод и вызвать его во всех трёх точках возврата `GetPassport`:

```go
// attachContact дописывает способ связи. Ошибка поиска продавца НЕ роняет
// паспорт: объект показать всё ещё можно, просто без кнопки заявки —
// деградация, а не отказ, как везде в этом сервисе.
func (s *ObjectService) attachContact(ctx context.Context, p ObjectPassport, l domain.Listing) ObjectPassport {
	var owner domain.OwnerListing
	found := false
	if s.owners != nil {
		o, err := s.owners.GetByExternalID(ctx, l.ExternalID)
		if err == nil {
			owner, found = o, true
		}
	}
	p.Contact = BuildPassportContact(owner, found, l)
	return p
}
```

В `GetPassport` заменить возвраты:

```go
		return s.attachContact(ctx, buildStandalonePassport(listing), listing), nil
```

(обе ветки `buildStandalonePassport`) и в конце:

```go
	p := staticPassport(listing)
	p.LifestyleAnalysis = analysis
	return s.attachContact(ctx, p, listing), nil
```

- [ ] **Step 6: Прогнать тест и убедиться, что он проходит**

Run: `cd backend && go test ./internal/service/ -run TestContact -v`
Expected: PASS (пять тестов)

- [ ] **Step 7: Починить вызов конструктора**

В `backend/cmd/api/main.go` перенести создание `ownerRepo` ВЫШЕ создания `objectService` и передать его:

```go
	ownerRepo := repository.NewOwnerListingRepo(pool)
	objectService := service.NewObjectService(chatService, chatSearchRepo, listingRepo,
		ownerRepo, mlClient, dossierTimeout, cfg.DossierTTLHours)
```

удалив прежнюю строку `ownerRepo := repository.NewOwnerListingRepo(pool)` из блока кабинета продавца.

- [ ] **Step 8: Прогнать весь набор**

Run: `cd backend && go test ./...`
Expected: PASS. Если `object_dossier_contract_test.go` падает на новом поле — дописать в него проверку наличия `contact` в ответе, а не убирать поле.

- [ ] **Step 9: Дописать контракт для фронта**

В `frontend/Пайплайн фронт.md` в раздел 4 (`GET /api/v1/objects/{id}`) добавить:

````markdown
#### Блок `contact` — единственное действие паспорта

Аддитивное поле верхнего уровня рядом с `lifestyle_analysis`:

```json
"contact": { "kind": "lead" }
"contact": { "kind": "external", "source_url": "https://www.cian.ru/sale/flat/318394906/" }
"contact": { "kind": "none" }
```

- `lead` — объявление ведёт продавец в кабинете и оно опубликовано. Показать
  форму заявки, отправлять в `POST /api/v1/objects/{object_id}/lead`.
- `external` — витринный объект, продавца в системе нет. Показать уход на
  источник по `source_url`.
- `none` — связаться нечем. Кнопку не показывать: выдуманная кнопка ведёт
  в никуда и хуже её отсутствия.

`source_url` приходит **только** при `kind: "external"`.
````

- [ ] **Step 10: Коммит**

```bash
cd /Users/yarik/PycharmProjects/Habitus
git add backend/internal/domain/domain.go backend/internal/repository/listing_repo.go \
        backend/internal/service/object_service.go backend/internal/service/object_contact_test.go \
        backend/cmd/api/main.go "frontend/Пайплайн фронт.md"
git commit -m "feat: паспорт называет способ связи — заявка продавцу или уход на источник"
```

---

## Task 6: заявка покупателя продавцу

Таблица `leads` плюс `POST /objects/{object_id}/lead`. Гостю сюда нельзя: заявка без аккаунта, на который можно ответить, бесполезна продавцу, — поэтому это естественная точка регистрации, а не поломка. Уникальность `(buyer_id, listing_id)` гасит повторные отправки без отдельного рейт-лимитера.

**Files:**
- Create: `backend/migrations/0012_leads.up.sql`, `backend/migrations/0012_leads.down.sql`
- Modify: `backend/internal/domain/domain.go` (`Lead`)
- Create: `backend/internal/repository/lead_repo.go`
- Create: `backend/internal/repository/lead_repo_test.go`
- Create: `backend/internal/service/lead_service.go`
- Create: `backend/internal/service/lead_service_test.go`
- Create: `backend/internal/http/handlers/lead_handler.go`
- Modify: `backend/internal/apperr/apperr.go`
- Modify: `backend/internal/http/router.go`, `backend/internal/app/app.go`, `backend/cmd/api/main.go`
- Modify: `frontend/Пайплайн фронт.md`

**Interfaces:**
- Consumes: `(*repository.OwnerListingRepo).GetByExternalID`; `middleware.IsGuest` (Task 4); `(*service.AuthService).UpgradeGuest` (Task 3); `repository.ErrNotFound`
- Produces: `domain.Lead`; `(*repository.LeadRepo).Create(ctx, domain.Lead) (domain.Lead, error)` (возвращает `repository.ErrDuplicateLead` на повторе); `(*repository.LeadRepo).ListForSeller(ctx, sellerID uuid.UUID, limit, offset int) ([]domain.Lead, int, error)`; `service.LeadInput{Name, Contact, Message string}`; `service.ValidateLeadInput(in LeadInput) (LeadInput, error)`; `(*service.LeadService).Send(ctx, buyerID uuid.UUID, externalID string, in service.LeadInput) (domain.Lead, error)`; `handlers.setSessionCookie(c, token, expiresAt, secure)`; `handlers.guestUpgrader`; `handlers.NewLeadHandler(leads *service.LeadService, auth guestUpgrader, cookieSecure bool)`; `apperr.LeadTargetNotFound()`, `apperr.LeadAlreadySent()`, `apperr.LeadToSelf()`, `apperr.RegistrationRequired()`

- [ ] **Step 1: Написать миграцию**

Создать `backend/migrations/0012_leads.up.sql`:

```sql
-- Заявка покупателя продавцу. Контакт продавца при этом НЕ раскрывается:
-- наружу уходит только то, что покупатель сам о себе сообщил.
CREATE TABLE leads (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    listing_id  uuid NOT NULL REFERENCES owner_listings(id) ON DELETE CASCADE,
    -- seller_id денормализован из owner_listings.user_id: список заявок
    -- продавца — самый частый запрос кабинета, и join ради него на каждой
    -- странице не нужен. Владелец объявления не меняется.
    seller_id   uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    buyer_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- external_id хранится копией: объявление могут снять и удалить, а заявка
    -- в истории продавца должна остаться читаемой.
    external_id text NOT NULL,
    name        text NOT NULL,
    contact     text NOT NULL,
    message     text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX leads_seller_ix ON leads (seller_id, created_at DESC);

-- Одна заявка на объявление от одного покупателя. Это и есть защита от
-- повторной отправки: отдельный рейт-лимитер тут был бы лишней деталью.
CREATE UNIQUE INDEX leads_buyer_listing_uq ON leads (buyer_id, listing_id);
```

Создать `backend/migrations/0012_leads.down.sql`:

```sql
DROP TABLE IF EXISTS leads;
```

- [ ] **Step 2: Написать падающий тест на репозиторий**

Создать `backend/internal/repository/lead_repo_test.go`:

```go
package repository

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"habitus-backend/internal/domain"
)

// newTestOwnerListing заводит ОПУБЛИКОВАННОЕ объявление продавца — цель заявки.
// Публикация делается отдельным SetStatus: Create не пишет колонку status
// (её нет в его INSERT), и объявление всегда рождается черновиком.
func newTestOwnerListing(t *testing.T, repo *OwnerListingRepo, sellerID uuid.UUID) domain.OwnerListing {
	t.Helper()
	ctx := context.Background()
	lng, lat := 37.6739, 55.7086
	l, err := repo.Create(ctx, domain.OwnerListing{
		UserID: sellerID, ExternalID: newExternalID(), Origin: "manual",
		City: "msk", Address: "Москва, Кожуховская улица, 14",
		Lng: &lng, Lat: &lat,
	})
	if err != nil {
		t.Fatalf("создать объявление: %v", err)
	}
	if err := repo.SetStatus(ctx, l.ID, "published", ""); err != nil {
		t.Fatalf("опубликовать объявление: %v", err)
	}
	published, err := repo.GetOwned(ctx, l.ID, sellerID)
	if err != nil {
		t.Fatalf("перечитать объявление: %v", err)
	}
	return published
}

func TestLeadCreateAndListForSeller(t *testing.T) {
	pool := testPool(t)
	users := NewUserRepo(pool)
	owners := NewOwnerListingRepo(pool)
	leads := NewLeadRepo(pool)
	ctx := context.Background()

	sellerID := newTestUser(t, users)
	buyerID := newTestUser(t, users)
	listing := newTestOwnerListing(t, owners, sellerID)

	created, err := leads.Create(ctx, domain.Lead{
		ListingID: listing.ID, SellerID: sellerID, BuyerID: buyerID,
		ExternalID: listing.ExternalID, Name: "Иван", Contact: "+7 999 000-00-00",
		Message: "Можно посмотреть в субботу?",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == uuid.Nil {
		t.Fatal("id заявки пустой")
	}
	if created.CreatedAt.IsZero() {
		t.Fatal("created_at не заполнен")
	}

	rows, total, err := leads.ListForSeller(ctx, sellerID, 10, 0)
	if err != nil {
		t.Fatalf("ListForSeller: %v", err)
	}
	if total != 1 || len(rows) != 1 {
		t.Fatalf("total = %d, строк = %d, ожидалось по 1", total, len(rows))
	}
	if rows[0].Contact != "+7 999 000-00-00" {
		t.Fatalf("contact = %q", rows[0].Contact)
	}
	if rows[0].Address != listing.Address {
		t.Fatalf("address = %q, ожидался %q", rows[0].Address, listing.Address)
	}
}

// Повтор гасится уникальным индексом, а не проверкой-перед-вставкой: две
// одновременные отправки иначе обе прошли бы.
func TestLeadCreateRejectsDuplicate(t *testing.T) {
	pool := testPool(t)
	users := NewUserRepo(pool)
	owners := NewOwnerListingRepo(pool)
	leads := NewLeadRepo(pool)
	ctx := context.Background()

	sellerID := newTestUser(t, users)
	buyerID := newTestUser(t, users)
	listing := newTestOwnerListing(t, owners, sellerID)
	lead := domain.Lead{
		ListingID: listing.ID, SellerID: sellerID, BuyerID: buyerID,
		ExternalID: listing.ExternalID, Name: "Иван", Contact: "+7 999 000-00-00",
	}

	if _, err := leads.Create(ctx, lead); err != nil {
		t.Fatalf("первая заявка: %v", err)
	}
	_, err := leads.Create(ctx, lead)

	if err != ErrDuplicateLead {
		t.Fatalf("err = %v, ожидался ErrDuplicateLead", err)
	}
}

// Чужие заявки в кабинет не попадают ни при каких обстоятельствах.
func TestLeadListForSellerIsScoped(t *testing.T) {
	pool := testPool(t)
	users := NewUserRepo(pool)
	owners := NewOwnerListingRepo(pool)
	leads := NewLeadRepo(pool)
	ctx := context.Background()

	sellerID := newTestUser(t, users)
	otherSellerID := newTestUser(t, users)
	buyerID := newTestUser(t, users)
	listing := newTestOwnerListing(t, owners, sellerID)

	if _, err := leads.Create(ctx, domain.Lead{
		ListingID: listing.ID, SellerID: sellerID, BuyerID: buyerID,
		ExternalID: listing.ExternalID, Name: "Иван", Contact: "+7 999 000-00-00",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	rows, total, err := leads.ListForSeller(ctx, otherSellerID, 10, 0)
	if err != nil {
		t.Fatalf("ListForSeller: %v", err)
	}
	if total != 0 || len(rows) != 0 {
		t.Fatalf("чужому продавцу видно %d заявок", len(rows))
	}
}
```

- [ ] **Step 3: Прогнать тест и убедиться, что он падает**

Run: `cd backend && go test ./internal/repository/ -run TestLead -v`
Expected: FAIL — `undefined: NewLeadRepo`, `undefined: ErrDuplicateLead`

- [ ] **Step 4: Добавить domain.Lead**

В `backend/internal/domain/domain.go` добавить:

```go
// Lead — заявка покупателя по объявлению продавца. Name/Contact — то, что
// покупатель сообщил о себе; контакт продавца в обратную сторону не уходит.
// Address дублируется из объявления при чтении списка — заявка должна
// оставаться читаемой, даже если объявление уже сняли.
type Lead struct {
	ID         uuid.UUID
	ListingID  uuid.UUID
	SellerID   uuid.UUID
	BuyerID    uuid.UUID
	ExternalID string
	Address    string
	Name       string
	Contact    string
	Message    string
	CreatedAt  time.Time
}
```

- [ ] **Step 5: Реализовать LeadRepo**

Создать `backend/internal/repository/lead_repo.go`:

```go
package repository

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"habitus-backend/internal/domain"
)

// ErrDuplicateLead — этот покупатель уже отправлял заявку по этому объявлению.
var ErrDuplicateLead = errors.New("lead already sent")

type LeadRepo struct {
	pool *pgxpool.Pool
}

func NewLeadRepo(pool *pgxpool.Pool) *LeadRepo {
	return &LeadRepo{pool: pool}
}

func (r *LeadRepo) Create(ctx context.Context, l domain.Lead) (domain.Lead, error) {
	var out domain.Lead
	err := r.pool.QueryRow(ctx, `
		INSERT INTO leads(listing_id, seller_id, buyer_id, external_id, name, contact, message)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, listing_id, seller_id, buyer_id, external_id, name, contact, message, created_at`,
		l.ListingID, l.SellerID, l.BuyerID, l.ExternalID, l.Name, l.Contact, l.Message,
	).Scan(&out.ID, &out.ListingID, &out.SellerID, &out.BuyerID, &out.ExternalID,
		&out.Name, &out.Contact, &out.Message, &out.CreatedAt)
	if err != nil {
		// Повтор ловим на уникальном индексе, а не проверкой-перед-вставкой:
		// две одновременные отправки иначе обе прошли бы.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return domain.Lead{}, ErrDuplicateLead
		}
		return domain.Lead{}, err
	}
	return out, nil
}

// ListForSeller отдаёт заявки продавца, свежие сверху, вместе с адресом
// объявления. total считается тем же запросом через оконную функцию —
// отдельный COUNT(*) удваивал бы поход в БД ради одного числа.
func (r *LeadRepo) ListForSeller(ctx context.Context, sellerID uuid.UUID,
	limit, offset int) ([]domain.Lead, int, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT l.id, l.listing_id, l.seller_id, l.buyer_id, l.external_id,
		       ol.address, l.name, l.contact, l.message, l.created_at,
		       COUNT(*) OVER () AS total
		FROM leads l
		JOIN owner_listings ol ON ol.id = l.listing_id
		WHERE l.seller_id = $1
		ORDER BY l.created_at DESC
		LIMIT $2 OFFSET $3`, sellerID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := make([]domain.Lead, 0, limit)
	total := 0
	for rows.Next() {
		var l domain.Lead
		if err := rows.Scan(&l.ID, &l.ListingID, &l.SellerID, &l.BuyerID, &l.ExternalID,
			&l.Address, &l.Name, &l.Contact, &l.Message, &l.CreatedAt, &total); err != nil {
			return nil, 0, err
		}
		out = append(out, l)
	}
	return out, total, rows.Err()
}
```

- [ ] **Step 6: Прогнать тест и убедиться, что он проходит**

Run: `cd backend && go test ./internal/repository/ -run TestLead -v`
Expected: PASS (три теста)

- [ ] **Step 7: Написать падающий тест на LeadService**

Создать `backend/internal/service/lead_service_test.go`:

```go
package service

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/domain"
	"habitus-backend/internal/repository"
)

type fakeLeadTarget struct {
	listing domain.OwnerListing
	err     error
}

func (f fakeLeadTarget) GetByExternalID(context.Context, string) (domain.OwnerListing, error) {
	return f.listing, f.err
}

type fakeLeadStore struct {
	created domain.Lead
	err     error
}

func (f *fakeLeadStore) Create(_ context.Context, l domain.Lead) (domain.Lead, error) {
	if f.err != nil {
		return domain.Lead{}, f.err
	}
	f.created = l
	l.ID = uuid.New()
	return l, nil
}

func newLeadService(target fakeLeadTarget, store *fakeLeadStore) *LeadService {
	return &LeadService{targets: target, leads: store}
}

func TestLeadSendFillsSellerFromListing(t *testing.T) {
	sellerID := uuid.New()
	listingID := uuid.New()
	store := &fakeLeadStore{}
	svc := newLeadService(fakeLeadTarget{listing: domain.OwnerListing{
		ID: listingID, UserID: sellerID, Status: "published", ExternalID: "cian_1",
	}}, store)
	buyerID := uuid.New()

	got, err := svc.Send(context.Background(), buyerID, "cian_1",
		LeadInput{Name: "Иван", Contact: "+7 999 000-00-00", Message: "В субботу?"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got.ID == uuid.Nil {
		t.Fatal("id заявки пустой")
	}
	if store.created.SellerID != sellerID {
		t.Fatalf("seller_id = %s, ожидался %s", store.created.SellerID, sellerID)
	}
	if store.created.ListingID != listingID {
		t.Fatalf("listing_id = %s, ожидался %s", store.created.ListingID, listingID)
	}
	if store.created.BuyerID != buyerID {
		t.Fatalf("buyer_id = %s, ожидался %s", store.created.BuyerID, buyerID)
	}
}

// Объект витринный (продавца в системе нет) — заявке некуда идти. 404 с
// собственным кодом, а не молчаливое «ок»: фронт обязан показать уход на
// источник, а не форму заявки.
func TestLeadSendRejectsListingWithoutSeller(t *testing.T) {
	svc := newLeadService(fakeLeadTarget{err: repository.ErrNotFound}, &fakeLeadStore{})

	_, err := svc.Send(context.Background(), uuid.New(), "cian_1",
		LeadInput{Name: "Иван", Contact: "+7 999 000-00-00"})

	assertAppErrCode(t, err, "lead_target_not_found")
}

// Неопубликованное объявление продавец скрыл сознательно — заявки не принимает.
func TestLeadSendRejectsUnpublishedListing(t *testing.T) {
	svc := newLeadService(fakeLeadTarget{listing: domain.OwnerListing{
		ID: uuid.New(), UserID: uuid.New(), Status: "draft",
	}}, &fakeLeadStore{})

	_, err := svc.Send(context.Background(), uuid.New(), "cian_1",
		LeadInput{Name: "Иван", Contact: "+7 999 000-00-00"})

	assertAppErrCode(t, err, "lead_target_not_found")
}

func TestLeadSendRejectsSelf(t *testing.T) {
	sellerID := uuid.New()
	svc := newLeadService(fakeLeadTarget{listing: domain.OwnerListing{
		ID: uuid.New(), UserID: sellerID, Status: "published",
	}}, &fakeLeadStore{})

	_, err := svc.Send(context.Background(), sellerID, "cian_1",
		LeadInput{Name: "Иван", Contact: "+7 999 000-00-00"})

	assertAppErrCode(t, err, "lead_to_self")
}

func TestLeadSendRequiresContact(t *testing.T) {
	svc := newLeadService(fakeLeadTarget{listing: domain.OwnerListing{
		ID: uuid.New(), UserID: uuid.New(), Status: "published",
	}}, &fakeLeadStore{})

	_, err := svc.Send(context.Background(), uuid.New(), "cian_1",
		LeadInput{Name: "Иван", Contact: "   "})

	assertAppErrCode(t, err, "validation_error")
}

func TestLeadSendMapsDuplicateTo409(t *testing.T) {
	svc := newLeadService(fakeLeadTarget{listing: domain.OwnerListing{
		ID: uuid.New(), UserID: uuid.New(), Status: "published",
	}}, &fakeLeadStore{err: repository.ErrDuplicateLead})

	_, err := svc.Send(context.Background(), uuid.New(), "cian_1",
		LeadInput{Name: "Иван", Contact: "+7 999 000-00-00"})

	assertAppErrCode(t, err, "lead_already_sent")
}

func assertAppErrCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("err = nil, ожидался %s", code)
	}
	appErr, ok := err.(*apperr.Error)
	if !ok || appErr.Code != code {
		t.Fatalf("err = %#v, ожидался *apperr.Error{Code: %s}", err, code)
	}
}
```

- [ ] **Step 8: Прогнать тест и убедиться, что он падает**

Run: `cd backend && go test ./internal/service/ -run TestLeadSend -v`
Expected: FAIL — `undefined: LeadService`, `undefined: LeadInput`

- [ ] **Step 9: Добавить коды ошибок**

В `backend/internal/apperr/apperr.go` добавить:

```go
func LeadTargetNotFound() *Error {
	return New(http.StatusNotFound, "lead_target_not_found",
		"По этому объекту заявку оставить нельзя — свяжитесь с продавцом у источника")
}

func LeadAlreadySent() *Error {
	return New(http.StatusConflict, "lead_already_sent",
		"Вы уже отправляли заявку по этому объявлению — продавец её видит")
}

func LeadToSelf() *Error {
	return New(http.StatusBadRequest, "lead_to_self",
		"Это ваше собственное объявление")
}

// RegistrationRequired — не отказ, а приглашение. Гость дошёл до заявки, и
// именно здесь аккаунт впервые нужен по делу: продавцу нужно, кому ответить.
// По этому коду фронт открывает регистрацию ПРЯМО В ФОРМЕ заявки, не теряя
// заполненного, и повторяет запрос с блоком register.
func RegistrationRequired() *Error {
	return New(http.StatusForbidden, "registration_required",
		"Заведите аккаунт, чтобы отправить заявку — продавцу нужно, кому ответить. "+
			"Всё, что вы уже нашли и сохранили, останется при вас")
}
```

- [ ] **Step 10: Реализовать LeadService**

Создать `backend/internal/service/lead_service.go`:

```go
// lead_service.go — заявка покупателя продавцу. Это единственное место, где
// путь пользователя из «нашли квартиру» переходит в «договорились о просмотре»:
// до его появления паспорт был тупиком.
package service

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/domain"
	"habitus-backend/internal/repository"
)

// Границы полей заявки. Не про безопасность (BodyLimit уже есть), а про то,
// что продавец должен прочитать заявку, а не простыню.
const (
	leadNameMaxLen    = 120
	leadContactMaxLen = 200
	leadMessageMaxLen = 1000
)

// LeadInput — то, что покупатель сообщает о себе. Контакт продавца в обратную
// сторону не уходит: связь идёт через кабинет.
type LeadInput struct {
	Name    string
	Contact string
	Message string
}

// leadTarget — часть OwnerListingRepo: найти объявление продавца по внешнему id.
type leadTarget interface {
	GetByExternalID(ctx context.Context, externalID string) (domain.OwnerListing, error)
}

// leadStore — часть LeadRepo.
type leadStore interface {
	Create(ctx context.Context, l domain.Lead) (domain.Lead, error)
}

type LeadService struct {
	targets leadTarget
	leads   leadStore
}

func NewLeadService(targets *repository.OwnerListingRepo, leads *repository.LeadRepo) *LeadService {
	return &LeadService{targets: targets, leads: leads}
}

// ValidateLeadInput нормализует и проверяет поля заявки. Вынесена из Send
// намеренно: хендлер обязан вызвать её ДО того, как заведёт гостю аккаунт —
// иначе человек с пустым телефоном сначала регистрировался бы и только потом
// видел ошибку формы.
func ValidateLeadInput(in LeadInput) (LeadInput, error) {
	out := LeadInput{
		Name:    strings.TrimSpace(in.Name),
		Contact: strings.TrimSpace(in.Contact),
		Message: strings.TrimSpace(in.Message),
	}
	if out.Name == "" {
		return LeadInput{}, apperr.Validation("Представьтесь — продавцу нужно знать, кто пишет")
	}
	if out.Contact == "" {
		return LeadInput{}, apperr.Validation("Оставьте телефон или другой способ связи")
	}
	if len(out.Name) > leadNameMaxLen || len(out.Contact) > leadContactMaxLen ||
		len(out.Message) > leadMessageMaxLen {
		return LeadInput{}, apperr.Validation("Слишком длинный текст заявки")
	}
	return out, nil
}

func (s *LeadService) Send(ctx context.Context, buyerID uuid.UUID, externalID string,
	in LeadInput) (domain.Lead, error) {
	// Повторная проверка, даже если хендлер уже вызывал ValidateLeadInput:
	// сервис не полагается на дисциплину вызывающего.
	in, err := ValidateLeadInput(in)
	if err != nil {
		return domain.Lead{}, err
	}
	name, contact, message := in.Name, in.Contact, in.Message

	listing, err := s.targets.GetByExternalID(ctx, externalID)
	if errors.Is(err, repository.ErrNotFound) {
		// Объект витринный — продавца в системе нет, заявке некуда идти.
		return domain.Lead{}, apperr.LeadTargetNotFound()
	}
	if err != nil {
		return domain.Lead{}, err
	}
	// Заявки принимает только опубликованное: черновик и снятое с витрины
	// продавец скрыл сознательно. Тот же критерий, что у contact.kind в паспорте.
	if listing.Status != "published" {
		return domain.Lead{}, apperr.LeadTargetNotFound()
	}
	if listing.UserID == buyerID {
		return domain.Lead{}, apperr.LeadToSelf()
	}

	lead, err := s.leads.Create(ctx, domain.Lead{
		ListingID: listing.ID, SellerID: listing.UserID, BuyerID: buyerID,
		ExternalID: listing.ExternalID, Name: name, Contact: contact, Message: message,
	})
	if errors.Is(err, repository.ErrDuplicateLead) {
		return domain.Lead{}, apperr.LeadAlreadySent()
	}
	if err != nil {
		return domain.Lead{}, err
	}
	return lead, nil
}
```

- [ ] **Step 11: Прогнать тест и убедиться, что он проходит**

Run: `cd backend && go test ./internal/service/ -run TestLeadSend -v`
Expected: PASS (шесть тестов)

- [ ] **Step 12: Написать падающий тест на приглашение к регистрации**

Создать `backend/internal/http/handlers/lead_guest_test.go`:

```go
package handlers

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"habitus-backend/internal/http/middleware"
)

// newGuestLeadApp собирает хендлер с НУЛЕВЫМИ зависимостями намеренно: обе
// проверяемые ветки обязаны отработать до похода в сервис заявок и в auth.
// Если ветка «протечёт» дальше, тест упадёт паникой на nil — это и есть
// проверка порядка.
func newGuestLeadApp() *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: middleware.ErrorHandler})
	h := NewLeadHandler(nil, nil, false)
	app.Post("/objects/:object_id/lead", func(c *fiber.Ctx) error {
		c.Locals(middleware.UserIDLocalsKey, uuid.New())
		c.Locals(middleware.IsGuestLocalsKey, true)
		return c.Next()
	}, h.Send)
	return app
}

func postLead(t *testing.T, app *fiber.App, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("POST", "/objects/cian_1/lead", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("запрос: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("разбор тела: %v (%s)", err, raw)
	}
	return resp.StatusCode, got
}

// Гость без блока register получает ПРИГЛАШЕНИЕ зарегистрироваться, а не
// глухой отказ: по этому коду фронт раскрывает поля email/пароля в той же
// форме и повторяет запрос, не теряя заполненного.
func TestLeadSendInvitesGuestToRegister(t *testing.T) {
	status, got := postLead(t, newGuestLeadApp(),
		`{"name":"Иван","contact":"+7 999 000-00-00"}`)

	if status != fiber.StatusForbidden {
		t.Fatalf("статус = %d, ожидался 403", status)
	}
	envelope, _ := got["error"].(map[string]any)
	if envelope == nil {
		t.Fatalf("ответ вне конверта ошибки: %v", got)
	}
	if envelope["code"] != "registration_required" {
		t.Fatalf("code = %v, ожидался registration_required", envelope["code"])
	}
	message, _ := envelope["message"].(string)
	if message == "" {
		t.Fatal("пустое сообщение: гость не поймёт, что ему предлагают")
	}
}

// Форма проверяется ДО регистрации: иначе человек с пустым телефоном сначала
// получил бы аккаунт и только потом — ошибку поля. Нулевой auth в хендлере
// это и доказывает: дойди сюда регистрация, тест упал бы паникой.
func TestLeadSendValidatesFormBeforeRegistering(t *testing.T) {
	status, got := postLead(t, newGuestLeadApp(),
		`{"name":"Иван","contact":"   ","register":{"email":"a@example.test","password":"password1"}}`)

	if status != fiber.StatusBadRequest {
		t.Fatalf("статус = %d, ожидался 400", status)
	}
	envelope, _ := got["error"].(map[string]any)
	if envelope == nil || envelope["code"] != "validation_error" {
		t.Fatalf("ожидался validation_error, получено %v", got)
	}
}
```

- [ ] **Step 13: Прогнать тест и убедиться, что он падает**

Run: `cd backend && go test ./internal/http/handlers/ -run TestLeadSend -v`
Expected: FAIL — `undefined: NewLeadHandler`

- [ ] **Step 14: Написать хендлер и общий хелпер куки**

Заявка выдаёт новую сессию, как и `/auth/register`, поэтому установка куки
переезжает из метода `AuthHandler` в функцию пакета — двух копий одной
настройки куки быть не должно.

Создать `backend/internal/http/handlers/session_cookie.go`:

```go
package handlers

import (
	"time"

	"github.com/gofiber/fiber/v2"

	"habitus-backend/internal/http/middleware"
)

// setSessionCookie — единственное место, где задаются параметры сессионной
// куки. Ставит её и вход, и регистрация, и заявка гостя: разъехавшиеся
// SameSite или Path у разных ручек означали бы, что часть сессий молча теряется.
func setSessionCookie(c *fiber.Ctx, token string, expiresAt time.Time, secure bool) {
	c.Cookie(&fiber.Cookie{
		Name:     middleware.SessionCookieName,
		Value:    token,
		Expires:  expiresAt,
		HTTPOnly: true,
		Secure:   secure,
		SameSite: "Lax",
		Path:     "/",
	})
}
```

В `backend/internal/http/handlers/auth_handler.go` заменить тело метода на делегирование:

```go
func (h *AuthHandler) setSessionCookie(c *fiber.Ctx, token string, expiresAt time.Time) {
	setSessionCookie(c, token, expiresAt, h.cookieSecure)
}
```

Создать `backend/internal/http/handlers/lead_handler.go`:

```go
package handlers

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/domain"
	"habitus-backend/internal/http/middleware"
	"habitus-backend/internal/service"
)

// guestUpgrader — часть AuthService, нужная заявке. Обособленный интерфейс:
// AuthService держит конкретные репозитории, и без него хендлер нельзя было бы
// проверить без поднятой БД.
type guestUpgrader interface {
	UpgradeGuest(ctx context.Context, guestID uuid.UUID, email, password, name string) (domain.User, string, time.Time, error)
}

type LeadHandler struct {
	leads        *service.LeadService
	auth         guestUpgrader
	cookieSecure bool
}

func NewLeadHandler(leads *service.LeadService, auth guestUpgrader, cookieSecure bool) *LeadHandler {
	return &LeadHandler{leads: leads, auth: auth, cookieSecure: cookieSecure}
}

// leadRegisterRequest — регистрация прямо в форме заявки. Пароль здесь тот же,
// что и в /auth/register, и проверяется тем же UpgradeGuest.
type leadRegisterRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type leadRequest struct {
	Name    string `json:"name"`
	Contact string `json:"contact"`
	Message string `json:"message"`
	// Register присылает гость. Отсутствует — гость получает приглашение
	// зарегистрироваться (403 registration_required), а не глухой отказ.
	Register *leadRegisterRequest `json:"register"`
}

// LeadDTO — форма заявки в ответах. Контакт покупателя тут есть (продавец за
// ним и пришёл), контакта продавца нет нигде.
func LeadDTO(l domain.Lead) fiber.Map {
	return fiber.Map{
		"id":          l.ID,
		"listing_id":  l.ListingID,
		"external_id": l.ExternalID,
		"address":     l.Address,
		"name":        l.Name,
		"contact":     l.Contact,
		"message":     l.Message,
		"created_at":  l.CreatedAt,
	}
}

// Send implements POST /objects/{object_id}/lead.
//
// Заявка от гостя, которого через месяц вычистит свипер, продавцу бесполезна —
// но отказывать здесь неправильно: это ровно та точка, где аккаунт впервые
// нужен по делу. Поэтому гостю не говорят «нельзя», а заводят аккаунт ТЕМ ЖЕ
// запросом: отдельный поход на регистрацию потерял бы заполненную форму, а
// вместе с ней и заявку.
func (h *LeadHandler) Send(c *fiber.Ctx) error {
	var req leadRequest
	if err := c.BodyParser(&req); err != nil {
		return apperr.Validation("invalid request body")
	}
	objectID := c.Params("object_id")
	if objectID == "" {
		return apperr.ObjectNotFound()
	}

	// Поля заявки проверяются ДО регистрации: иначе человек с пустым телефоном
	// сначала получил бы аккаунт и только потом — ошибку формы.
	input, err := service.ValidateLeadInput(service.LeadInput{
		Name: req.Name, Contact: req.Contact, Message: req.Message,
	})
	if err != nil {
		return err
	}

	userID := middleware.UserID(c)
	registered := false
	if middleware.IsGuest(c) {
		if req.Register == nil || req.Register.Email == "" || req.Register.Password == "" {
			// Приглашение, а не отказ: фронт по этому коду раскрывает поля
			// email/пароля в той же форме и повторяет запрос.
			return apperr.RegistrationRequired()
		}
		// Имя из заявки становится именем аккаунта — отдельное поле спрашивать
		// незачем, человек его уже ввёл.
		u, token, expiresAt, err := h.auth.UpgradeGuest(c.Context(), userID,
			req.Register.Email, req.Register.Password, input.Name)
		if err != nil {
			return err
		}
		setSessionCookie(c, token, expiresAt, h.cookieSecure)
		userID = u.ID
		registered = true
	}

	lead, err := h.leads.Send(c.Context(), userID, objectID, input)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"lead": LeadDTO(lead),
		// registered говорит фронту, что сессия сменилась и гость стал
		// аккаунтом: перечитывать /me ради одного флага незачем.
		"registered": registered,
	})
}
```

- [ ] **Step 15: Прогнать тест и убедиться, что он проходит**

Run: `cd backend && go test ./internal/http/handlers/ -run TestLeadSend -v`
Expected: PASS (два теста)

- [ ] **Step 16: Подключить маршрут и сборку**

В `backend/internal/http/router.go` добавить `Lead *handlers.LeadHandler` в `Handlers` и маршрут после `objects/:object_id/ask/stream`:

```go
	// Без RequireRegistered: гостя здесь не отвергают, а регистрируют тем же
	// запросом — решение принимает сам хендлер.
	api.Post("/objects/:object_id/lead", authMw, h.Lead.Send)
```

В `backend/internal/app/app.go` добавить `Leads *service.LeadService` в `Services` и `Lead: handlers.NewLeadHandler(svc.Leads, svc.Auth, cfg.SessionCookieSecure),` в `RegisterRoutes`.

В `backend/cmd/api/main.go` добавить:

```go
	leadRepo := repository.NewLeadRepo(pool)
	leadService := service.NewLeadService(ownerRepo, leadRepo)
```

и передать `Leads: leadService,` в `app.Services`.

- [ ] **Step 17: Прогнать весь набор**

Run: `cd backend && go test ./...`
Expected: PASS

- [ ] **Step 18: Дописать контракт для фронта**

В `frontend/Пайплайн фронт.md` добавить раздел:

```markdown
### Заявка продавцу

- **Эндпоинт:** `POST /api/v1/objects/{object_id}/lead`
- **Когда показывать:** только при `contact.kind == "lead"` в паспорте объекта.
- **Тело:** `{"name": "Иван", "contact": "+7 999 000-00-00", "message": "Можно посмотреть в субботу?"}`
  `message` необязателен; `name` и `contact` обязательны.
- **Ответ 201:** `{"lead": {...}, "registered": false}`, где `lead` =
  `{id, listing_id, external_id, address, name, contact, message, created_at}`.

#### Гость: регистрация прямо в форме заявки

Гостю здесь **не отказывают** — аккаунт заводится тем же запросом. Отдельный
поход на страницу регистрации потерял бы заполненную форму, а вместе с ней и
заявку.

Сценарий:

1. Гость (`is_guest: true` из `/me`) отправляет заявку без блока `register` —
   приходит **403** `registration_required` с текстом-приглашением.
2. Фронт **раскрывает поля email/пароля в той же форме**, ничего не очищая, и
   повторяет запрос с блоком `register`:

   `{"name": "Иван", "contact": "+7 999 000-00-00", "message": "...",
   "register": {"email": "ivan@example.test", "password": "password1"}}`

3. Ответ **201** `{"lead": {...}, "registered": true}`. Кука сессии заменена на
   обычную (30 дней), `id` пользователя не изменился — чаты, избранное и
   оценки остались при нём.

Фронт, знающий из `/me`, что перед ним гость, может показать поля email/пароля
сразу и обойтись одним запросом; 403 остаётся страховкой.

`name` из заявки становится именем аккаунта — отдельно его не спрашивать.

Порядок проверок на сервере: сначала поля заявки, потом регистрация. Поэтому
`validation_error` по пустому `contact` придёт **до** того, как аккаунт будет
заведён, — форму можно просто дать поправить.

Отказы:

|Код|HTTP|Что показать|
|---|---|---|
|`registration_required`|403|Раскрыть email/пароль в форме заявки и повторить запрос с `register`|
|`validation_error`|400|Текст из `message` — под соответствующим полем формы (в том числе «email уже зарегистрирован» и «пароль короче 8 символов» из `register`)|
|`lead_target_not_found`|404|Объект не принимает заявки — предложить уход на источник|
|`lead_already_sent`|409|«Вы уже отправляли заявку» — не ошибка, а состояние|
|`lead_to_self`|400|Это собственное объявление пользователя|
```

- [ ] **Step 19: Коммит**

```bash
cd /Users/yarik/PycharmProjects/Habitus
git add backend/migrations/0012_leads.up.sql backend/migrations/0012_leads.down.sql \
        backend/internal/domain/domain.go backend/internal/repository/lead_repo.go \
        backend/internal/repository/lead_repo_test.go backend/internal/service/lead_service.go \
        backend/internal/service/lead_service_test.go \
        backend/internal/http/handlers/lead_handler.go backend/internal/http/handlers/lead_guest_test.go \
        backend/internal/http/handlers/session_cookie.go backend/internal/http/handlers/auth_handler.go \
        backend/internal/apperr/apperr.go \
        backend/internal/http/router.go backend/internal/app/app.go backend/cmd/api/main.go \
        "frontend/Пайплайн фронт.md"
git commit -m "feat: заявка продавцу — гость заводит аккаунт тем же запросом"
```

---

## Task 7: продавец видит входящие заявки

**Files:**
- Modify: `backend/internal/service/lead_service.go`
- Modify: `backend/internal/service/lead_service_test.go`
- Modify: `backend/internal/http/handlers/lead_handler.go`
- Create: `backend/internal/http/handlers/lead_handler_test.go`
- Modify: `backend/internal/http/router.go`
- Modify: `frontend/Пайплайн фронт.md`

**Interfaces:**
- Consumes: `(*repository.LeadRepo).ListForSeller` (Task 6); `handlers.parseLimitOffset` (`chat_handler.go:63`)
- Produces: `(*service.LeadService).ListForSeller(ctx, sellerID uuid.UUID, limit, offset int) ([]domain.Lead, int, error)`; `(*handlers.LeadHandler).List`; `GET /api/v1/owner/leads`

- [ ] **Step 1: Написать падающий тест**

Дописать в `backend/internal/service/lead_service_test.go`:

```go
type fakeLeadLister struct {
	rows       []domain.Lead
	total      int
	gotSeller  uuid.UUID
	gotLimit   int
	gotOffset  int
}

func (f *fakeLeadLister) ListForSeller(_ context.Context, sellerID uuid.UUID,
	limit, offset int) ([]domain.Lead, int, error) {
	f.gotSeller, f.gotLimit, f.gotOffset = sellerID, limit, offset
	return f.rows, f.total, nil
}

// Продавец из сессии, а не из параметра запроса: иначе чужие заявки читались
// бы подстановкой id в URL.
func TestLeadListScopesToSessionSeller(t *testing.T) {
	lister := &fakeLeadLister{rows: []domain.Lead{{Name: "Иван"}}, total: 1}
	svc := &LeadService{lists: lister}
	sellerID := uuid.New()

	rows, total, err := svc.ListForSeller(context.Background(), sellerID, 20, 40)
	if err != nil {
		t.Fatalf("ListForSeller: %v", err)
	}
	if lister.gotSeller != sellerID {
		t.Fatalf("seller_id = %s, ожидался %s", lister.gotSeller, sellerID)
	}
	if lister.gotLimit != 20 || lister.gotOffset != 40 {
		t.Fatalf("пагинация не доехала: limit=%d offset=%d", lister.gotLimit, lister.gotOffset)
	}
	if total != 1 || len(rows) != 1 {
		t.Fatalf("total = %d, строк = %d", total, len(rows))
	}
}
```

- [ ] **Step 2: Прогнать тест и убедиться, что он падает**

Run: `cd backend && go test ./internal/service/ -run TestLeadList -v`
Expected: FAIL — `unknown field lists in struct literal`

- [ ] **Step 3: Добавить чтение в LeadService**

В `backend/internal/service/lead_service.go` добавить интерфейс, поле и метод:

```go
// leadLister — часть LeadRepo для кабинета продавца.
type leadLister interface {
	ListForSeller(ctx context.Context, sellerID uuid.UUID, limit, offset int) ([]domain.Lead, int, error)
}
```

В структуру `LeadService` добавить `lists leadLister`, в `NewLeadService` — `lists: leads`, и метод:

```go
// ListForSeller. sellerID берётся ИЗ СЕССИИ вызывающим хендлером и никогда из
// параметров запроса — иначе чужие заявки читались бы подстановкой id в URL.
func (s *LeadService) ListForSeller(ctx context.Context, sellerID uuid.UUID,
	limit, offset int) ([]domain.Lead, int, error) {
	return s.lists.ListForSeller(ctx, sellerID, limit, offset)
}
```

- [ ] **Step 4: Прогнать тест и убедиться, что он проходит**

Run: `cd backend && go test ./internal/service/ -run TestLeadList -v`
Expected: PASS

- [ ] **Step 5: Написать падающий тест на форму ответа**

Создать `backend/internal/http/handlers/lead_handler_test.go`:

```go
package handlers

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"habitus-backend/internal/domain"
)

func TestLeadDTOShape(t *testing.T) {
	dto := LeadDTO(domain.Lead{
		ID: uuid.New(), ListingID: uuid.New(), ExternalID: "cian_318394906",
		Address: "Москва, улица Мельникова, 3к1", Name: "Иван",
		Contact: "+7 999 000-00-00", Message: "В субботу?",
		CreatedAt: time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC),
	})

	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"id", "listing_id", "external_id", "address",
		"name", "contact", "message", "created_at"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("в ответе нет поля %q", key)
		}
	}
	// Идентификаторов сторон в ответе быть не должно: продавцу они не нужны,
	// а покупательский id — лишняя утечка из кабинета.
	for _, key := range []string{"seller_id", "buyer_id"} {
		if _, ok := got[key]; ok {
			t.Fatalf("в ответе есть лишнее поле %q", key)
		}
	}
}
```

- [ ] **Step 6: Прогнать тест и убедиться, что он падает или проходит**

Run: `cd backend && go test ./internal/http/handlers/ -run TestLeadDTOShape -v`
Expected: PASS, если `LeadDTO` из Task 6 уже не отдаёт `seller_id`/`buyer_id`; FAIL — убрать эти поля из `LeadDTO`

- [ ] **Step 7: Добавить хендлер списка**

В `backend/internal/http/handlers/lead_handler.go` добавить:

```go
const (
	leadsDefaultLimit = 20
	leadsMaxLimit     = 100
)

// List implements GET /api/v1/owner/leads?limit=&offset= — входящие заявки
// продавца, свежие сверху. Продавец берётся из сессии, а не из параметров.
func (h *LeadHandler) List(c *fiber.Ctx) error {
	limit, offset := parseLimitOffset(c, leadsDefaultLimit)
	if limit > leadsMaxLimit {
		limit = leadsMaxLimit
	}

	rows, total, err := h.leads.ListForSeller(c.Context(), middleware.UserID(c), limit, offset)
	if err != nil {
		return err
	}
	leads := make([]fiber.Map, 0, len(rows))
	for _, l := range rows {
		leads = append(leads, LeadDTO(l))
	}
	return c.JSON(fiber.Map{"leads": leads, "count": len(leads), "total": total})
}
```

- [ ] **Step 8: Подключить маршрут**

В `backend/internal/http/router.go` добавить в группу кабинета, ПЕРВОЙ строкой группы (до `/listings/:listing_id`, чтобы Fiber не принял `leads` за uuid — та же причина, что у `/listings/import`):

```go
	ownerGroup.Get("/leads", h.Lead.List)
```

- [ ] **Step 9: Прогнать весь набор**

Run: `cd backend && go test ./...`
Expected: PASS

- [ ] **Step 10: Проверить сквозной путь заявки вручную**

```bash
# продавец: регистрируется, создаёт и публикует объявление
curl -s -c /tmp/seller.txt -X POST localhost:8080/api/v1/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"email":"seller@example.test","password":"password1","name":"Продавец"}' | jq

# ... создать объявление через POST /api/v1/owner/listings и опубликовать,
# запомнить его external_id

# покупатель: регистрируется и шлёт заявку
curl -s -c /tmp/buyer.txt -X POST localhost:8080/api/v1/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"email":"buyer@example.test","password":"password1","name":"Покупатель"}' | jq

curl -s -b /tmp/buyer.txt "localhost:8080/api/v1/objects/<external_id>" | jq .contact
# ожидается {"kind":"lead"}

curl -i -b /tmp/buyer.txt -X POST "localhost:8080/api/v1/objects/<external_id>/lead" \
  -H 'Content-Type: application/json' \
  -d '{"name":"Покупатель","contact":"+7 999 000-00-00","message":"В субботу?"}'
# ожидается 201 и {"lead":{...},"registered":false}
# повтор той же команды — 409 lead_already_sent

# гость: заявка заводит ему аккаунт тем же запросом
curl -s -c /tmp/guest.txt -X POST localhost:8080/api/v1/auth/guest | jq .id
# запомнить id — он не должен измениться после регистрации

curl -i -b /tmp/guest.txt -X POST "localhost:8080/api/v1/objects/<external_id>/lead" \
  -H 'Content-Type: application/json' \
  -d '{"name":"Гость","contact":"+7 999 111-11-11"}'
# ожидается 403 registration_required — приглашение, а не отказ

curl -i -b /tmp/guest.txt -c /tmp/guest.txt \
  -X POST "localhost:8080/api/v1/objects/<external_id>/lead" \
  -H 'Content-Type: application/json' \
  -d '{"name":"Гость","contact":"+7 999 111-11-11","register":{"email":"guest@example.test","password":"password1"}}'
# ожидается 201 и {"registered":true}

curl -s -b /tmp/guest.txt localhost:8080/api/v1/me | jq
# ожидается is_guest: false и ТОТ ЖЕ id, что вернул /auth/guest

curl -s -b /tmp/seller.txt localhost:8080/api/v1/owner/leads | jq
# ожидаются две заявки — от зарегистрированного покупателя и от бывшего гостя
```

- [ ] **Step 11: Дописать контракт для фронта**

В `frontend/Пайплайн фронт.md` в таблицу ручек кабинета добавить строку:

```markdown
|`GET`|`/api/v1/owner/leads?limit=20&offset=0`|Входящие заявки, свежие сверху → `{"leads": Lead[], "count", "total"}`|
```

и под таблицей:

```markdown
`Lead` = `{id, listing_id, external_id, address, name, contact, message, created_at}`.
`name`/`contact` — то, что оставил **покупатель**. Идентификаторы сторон
наружу не отдаются.
```

- [ ] **Step 12: Коммит**

```bash
cd /Users/yarik/PycharmProjects/Habitus
git add backend/internal/service/lead_service.go backend/internal/service/lead_service_test.go \
        backend/internal/http/handlers/lead_handler.go backend/internal/http/handlers/lead_handler_test.go \
        backend/internal/http/router.go "frontend/Пайплайн фронт.md"
git commit -m "feat: кабинет продавца показывает входящие заявки"
```

---

## Task 8: избранное

Сейчас объект живёт только внутри чата (`chat_search_results`): закрыл вкладку — потерял. Избранное переживает чат и работает у гостя тоже (после регистрации оно остаётся при нём — id пользователя не меняется, см. Task 2).

Карточка избранного НЕ содержит `match_score` и `tags`: они принадлежат конкретному запросу, а не объекту. Подставить туда ноль означало бы выдумать «0% совпадения» — прямо запрещено правилами проекта.

**Files:**
- Create: `backend/migrations/0013_favorites.up.sql`, `backend/migrations/0013_favorites.down.sql`
- Modify: `backend/internal/domain/domain.go` (`Favorite`)
- Create: `backend/internal/repository/favorite_repo.go`
- Create: `backend/internal/repository/favorite_repo_test.go`
- Modify: `backend/internal/service/display_fields.go` (`FavoriteObject`, `BuildFavoriteObject`)
- Create: `backend/internal/service/favorite_service.go`
- Create: `backend/internal/service/favorite_service_test.go`
- Create: `backend/internal/http/handlers/favorite_handler.go`
- Modify: `backend/internal/http/router.go`, `backend/internal/app/app.go`, `backend/cmd/api/main.go`
- Modify: `frontend/Пайплайн фронт.md`

**Interfaces:**
- Consumes: `(*repository.ListingRepo).GetByExternalIDs`; `service.SynthName`, `service.FormatFloor`, `service.PlaceholderCoverImage` (`display_fields.go`)
- Produces: `domain.Favorite{UserID, ExternalID, ChatID *uuid.UUID, CreatedAt}`; `(*repository.FavoriteRepo).Add/Remove/List`; `service.FavoriteObject`; `service.BuildFavoriteObject(f domain.Favorite, l domain.Listing) (FavoriteObject, bool)`; `(*service.FavoriteService).Add/Remove/List`

- [ ] **Step 1: Написать миграцию**

Создать `backend/migrations/0013_favorites.up.sql`:

```sql
-- Сохранённые объекты. Переживают чат: до этого объект жил только в
-- chat_search_results, и закрытая вкладка означала потерю находки.
CREATE TABLE favorites (
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- Ссылки на listings нет: таблица Python-owned, и внешний ключ из
    -- Go-схемы связал бы две системы миграций. Пропавший из витрины объект
    -- просто не попадает в выдачу списка.
    external_id text NOT NULL,
    -- Откуда сохранён: с этим chat_id паспорт откроется с досье и процентом
    -- совпадения, без него — как «с карты». ON DELETE SET NULL, потому что
    -- удаление чата не должно уносить находку.
    chat_id     uuid REFERENCES chats(id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, external_id)
);

CREATE INDEX favorites_user_ix ON favorites (user_id, created_at DESC);
```

Создать `backend/migrations/0013_favorites.down.sql`:

```sql
DROP TABLE IF EXISTS favorites;
```

- [ ] **Step 2: Написать падающий тест на репозиторий**

Создать `backend/internal/repository/favorite_repo_test.go`:

```go
package repository

import (
	"context"
	"testing"
)

func TestFavoriteAddIsIdempotent(t *testing.T) {
	pool := testPool(t)
	users := NewUserRepo(pool)
	favs := NewFavoriteRepo(pool)
	ctx := context.Background()

	userID := newTestUser(t, users)
	externalID := newExternalID()

	if err := favs.Add(ctx, userID, externalID, nil); err != nil {
		t.Fatalf("первое сохранение: %v", err)
	}
	// Повторный клик по «сохранить» — не ошибка: это то же состояние.
	if err := favs.Add(ctx, userID, externalID, nil); err != nil {
		t.Fatalf("повторное сохранение: %v", err)
	}

	rows, total, err := favs.List(ctx, userID, 10, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 1 || len(rows) != 1 {
		t.Fatalf("total = %d, строк = %d, ожидалось по 1", total, len(rows))
	}
}

func TestFavoriteAddKeepsChatContext(t *testing.T) {
	pool := testPool(t)
	users := NewUserRepo(pool)
	chats := NewChatRepo(pool)
	favs := NewFavoriteRepo(pool)
	ctx := context.Background()

	userID := newTestUser(t, users)
	chat, err := chats.Create(ctx, userID, "msk", "Поиск")
	if err != nil {
		t.Fatalf("создать чат: %v", err)
	}
	externalID := newExternalID()

	if err := favs.Add(ctx, userID, externalID, &chat.ID); err != nil {
		t.Fatalf("Add: %v", err)
	}

	rows, _, err := favs.List(ctx, userID, 10, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 || rows[0].ChatID == nil || *rows[0].ChatID != chat.ID {
		t.Fatalf("chat_id не сохранился: %+v", rows)
	}
}

func TestFavoriteRemove(t *testing.T) {
	pool := testPool(t)
	users := NewUserRepo(pool)
	favs := NewFavoriteRepo(pool)
	ctx := context.Background()

	userID := newTestUser(t, users)
	externalID := newExternalID()
	if err := favs.Add(ctx, userID, externalID, nil); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := favs.Remove(ctx, userID, externalID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	// Удаление отсутствующего — тоже не ошибка: состояние уже такое.
	if err := favs.Remove(ctx, userID, externalID); err != nil {
		t.Fatalf("повторное удаление: %v", err)
	}

	_, total, err := favs.List(ctx, userID, 10, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 0 {
		t.Fatalf("total = %d, ожидался 0", total)
	}
}

func TestFavoriteListIsScopedToUser(t *testing.T) {
	pool := testPool(t)
	users := NewUserRepo(pool)
	favs := NewFavoriteRepo(pool)
	ctx := context.Background()

	mine := newTestUser(t, users)
	other := newTestUser(t, users)
	if err := favs.Add(ctx, mine, newExternalID(), nil); err != nil {
		t.Fatalf("Add: %v", err)
	}

	_, total, err := favs.List(ctx, other, 10, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 0 {
		t.Fatalf("чужому видно %d сохранённых", total)
	}
}
```

- [ ] **Step 3: Прогнать тест и убедиться, что он падает**

Run: `cd backend && go test ./internal/repository/ -run TestFavorite -v`
Expected: FAIL — `undefined: NewFavoriteRepo`

- [ ] **Step 4: Добавить domain.Favorite**

В `backend/internal/domain/domain.go` добавить:

```go
// Favorite — сохранённый объект. ChatID помнит, из какого подбора он сохранён:
// с ним паспорт откроется с досье, без него — как «с карты». nil — законное
// значение (объект сохранён с карты или чат удалён).
type Favorite struct {
	UserID     uuid.UUID
	ExternalID string
	ChatID     *uuid.UUID
	CreatedAt  time.Time
}
```

- [ ] **Step 5: Реализовать FavoriteRepo**

Создать `backend/internal/repository/favorite_repo.go`:

```go
package repository

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"habitus-backend/internal/domain"
)

type FavoriteRepo struct {
	pool *pgxpool.Pool
}

func NewFavoriteRepo(pool *pgxpool.Pool) *FavoriteRepo {
	return &FavoriteRepo{pool: pool}
}

// Add идемпотентен: повторный клик по «сохранить» — то же состояние, а не
// ошибка. chat_id при повторе обновляется: последний контекст сохранения
// полезнее первого.
func (r *FavoriteRepo) Add(ctx context.Context, userID uuid.UUID,
	externalID string, chatID *uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO favorites(user_id, external_id, chat_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id, external_id)
		DO UPDATE SET chat_id = COALESCE(EXCLUDED.chat_id, favorites.chat_id)`,
		userID, externalID, chatID)
	return err
}

// Remove тоже идемпотентен: удаление отсутствующего — уже нужное состояние.
func (r *FavoriteRepo) Remove(ctx context.Context, userID uuid.UUID, externalID string) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM favorites WHERE user_id = $1 AND external_id = $2`, userID, externalID)
	return err
}

func (r *FavoriteRepo) List(ctx context.Context, userID uuid.UUID,
	limit, offset int) ([]domain.Favorite, int, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT user_id, external_id, chat_id, created_at, COUNT(*) OVER () AS total
		FROM favorites
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := make([]domain.Favorite, 0, limit)
	total := 0
	for rows.Next() {
		var f domain.Favorite
		if err := rows.Scan(&f.UserID, &f.ExternalID, &f.ChatID, &f.CreatedAt, &total); err != nil {
			return nil, 0, err
		}
		out = append(out, f)
	}
	return out, total, rows.Err()
}
```

- [ ] **Step 6: Прогнать тест и убедиться, что он проходит**

Run: `cd backend && go test ./internal/repository/ -run TestFavorite -v`
Expected: PASS (четыре теста)

- [ ] **Step 7: Написать падающий тест на карточку и сервис**

Создать `backend/internal/service/favorite_service_test.go`:

```go
package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"habitus-backend/internal/domain"
)

type fakeFavoriteStore struct {
	rows  []domain.Favorite
	total int
}

func (f *fakeFavoriteStore) Add(context.Context, uuid.UUID, string, *uuid.UUID) error { return nil }
func (f *fakeFavoriteStore) Remove(context.Context, uuid.UUID, string) error          { return nil }
func (f *fakeFavoriteStore) List(context.Context, uuid.UUID, int, int) ([]domain.Favorite, int, error) {
	return f.rows, f.total, nil
}

// Хелперы указателей уже есть в пакете (owner_import_service_test.go):
// f64p, i64p, intp — свои не заводим, иначе в пакете окажется два набора
// имён для одного и того же.

// Карточка избранного собирается из фактов объекта. match_score и tags тут
// намеренно отсутствуют: они принадлежат запросу, и ноль вместо них был бы
// выдуманным «0% совпадения».
func TestBuildFavoriteObjectUsesListingFacts(t *testing.T) {
	chatID := uuid.New()
	addr := "Москва, улица Мельникова, 3к1"
	got, ok := BuildFavoriteObject(
		domain.Favorite{ExternalID: "cian_1", ChatID: &chatID, CreatedAt: time.Now()},
		domain.Listing{
			ExternalID: "cian_1", Lon: f64p(37.6595), Lat: f64p(55.7108),
			Address: &addr, Price: i64p(12_500_000), Rooms: intp(2), Area: f64p(54.3),
			Level: intp(4), Levels: intp(17),
			Photos: []string{"https://images.cdn-cian.ru/1.jpg"},
		})

	if !ok {
		t.Fatal("объект отброшен, хотя координаты есть")
	}
	if got.Address != addr {
		t.Fatalf("address = %q", got.Address)
	}
	if len(got.Coordinates) != 2 || got.Coordinates[0] != 37.6595 || got.Coordinates[1] != 55.7108 {
		t.Fatalf("координаты = %v, контракт проекта — [lng, lat]", got.Coordinates)
	}
	if got.Floor != "4/17" {
		t.Fatalf("floor = %q, ожидалось 4/17", got.Floor)
	}
	if got.CoverImage != "https://images.cdn-cian.ru/1.jpg" {
		t.Fatalf("cover_image = %q", got.CoverImage)
	}
	if got.ChatID == nil || *got.ChatID != chatID {
		t.Fatalf("chat_id потерян: %v", got.ChatID)
	}
}

// Объект без координат на карту не поставить — как и в выдаче, он отбрасывается.
func TestBuildFavoriteObjectSkipsListingWithoutCoordinates(t *testing.T) {
	if _, ok := BuildFavoriteObject(domain.Favorite{ExternalID: "cian_1"},
		domain.Listing{ExternalID: "cian_1"}); ok {
		t.Fatal("объект без координат попал в избранное")
	}
}

// Пропавший из витрины объект просто не показывается — 500 из-за него быть
// не должно, как и в выдаче результатов.
func TestFavoriteListSkipsMissingListings(t *testing.T) {
	store := &fakeFavoriteStore{
		rows:  []domain.Favorite{{ExternalID: "cian_gone"}, {ExternalID: "cian_here"}},
		total: 2,
	}
	addr := "Москва"
	svc := &FavoriteService{favorites: store, listings: fakeListingLookup{
		byID: map[string]domain.Listing{
			"cian_here": {ExternalID: "cian_here", Lon: f64p(37.6), Lat: f64p(55.7), Address: &addr},
		},
	}}

	objects, count, total, err := svc.List(context.Background(), uuid.New(), 10, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if count != 1 || len(objects) != 1 {
		t.Fatalf("count = %d, объектов = %d, ожидалось по 1", count, len(objects))
	}
	// total — сколько сохранено всего, до отсева: иначе «показать ещё» врёт.
	if total != 2 {
		t.Fatalf("total = %d, ожидалось 2", total)
	}
}
```

- [ ] **Step 8: Прогнать тест и убедиться, что он падает**

Run: `cd backend && go test ./internal/service/ -run "TestBuildFavorite|TestFavoriteList" -v`
Expected: FAIL — `undefined: BuildFavoriteObject`, `undefined: FavoriteService`

- [ ] **Step 9: Добавить карточку избранного**

В `backend/internal/service/display_fields.go` добавить в конец:

```go
// FavoriteObject — карточка сохранённого объекта. Намеренно БЕЗ match_score и
// tags: и то и другое — свойства конкретного запроса, а не объекта, и ноль
// вместо них был бы выдуманным «0% совпадения». ChatID отдаётся, чтобы
// паспорт открылся с досье того подбора, из которого объект сохранён.
type FavoriteObject struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Address     string     `json:"address"`
	CoverImage  string     `json:"cover_image"`
	Coordinates []float64  `json:"coordinates"`
	PriceFrom   *int64     `json:"price_from"`
	Rooms       *int       `json:"rooms"`
	AreaSqm     *float64   `json:"area_sqm"`
	Floor       string     `json:"floor"`
	ChatID      *uuid.UUID `json:"chat_id"`
	SavedAt     time.Time  `json:"saved_at"`
}

// BuildFavoriteObject возвращает false, если объекта нет в витрине или у него
// нет координат — ровно как BuildStoredResultObject: пропавший объект тихо
// выпадает из списка, а не роняет запрос.
func BuildFavoriteObject(f domain.Favorite, l domain.Listing) (FavoriteObject, bool) {
	if l.ExternalID == "" || l.Lon == nil || l.Lat == nil {
		return FavoriteObject{}, false
	}
	address := ""
	if l.Address != nil {
		address = *l.Address
	}
	cover := PlaceholderCoverImage
	if len(l.Photos) > 0 && l.Photos[0] != "" {
		cover = l.Photos[0]
	}
	return FavoriteObject{
		ID:          f.ExternalID,
		Name:        SynthName(l.Rooms, l.Area),
		Address:     address,
		CoverImage:  cover,
		Coordinates: []float64{*l.Lon, *l.Lat},
		PriceFrom:   l.Price,
		Rooms:       l.Rooms,
		AreaSqm:     l.Area,
		Floor:       FormatFloor(l.Level, l.Levels),
		ChatID:      f.ChatID,
		SavedAt:     f.CreatedAt,
	}, true
}
```

Добавить в импорты этого файла `"time"` и `"github.com/google/uuid"`.

- [ ] **Step 10: Реализовать FavoriteService**

Создать `backend/internal/service/favorite_service.go`:

```go
// favorite_service.go — сохранённые объекты. До избранного объект жил только
// внутри чата: закрытая вкладка означала потерю находки.
package service

import (
	"context"

	"github.com/google/uuid"

	"habitus-backend/internal/domain"
	"habitus-backend/internal/repository"
)

// favoriteStore — часть FavoriteRepo.
type favoriteStore interface {
	Add(ctx context.Context, userID uuid.UUID, externalID string, chatID *uuid.UUID) error
	Remove(ctx context.Context, userID uuid.UUID, externalID string) error
	List(ctx context.Context, userID uuid.UUID, limit, offset int) ([]domain.Favorite, int, error)
}

type FavoriteService struct {
	favorites favoriteStore
	listings  listingLookup
}

func NewFavoriteService(favorites *repository.FavoriteRepo, listings *repository.ListingRepo) *FavoriteService {
	return &FavoriteService{favorites: favorites, listings: listings}
}

// Add идемпотентен — намеренно не проверяет наличие объекта в витрине:
// проверка стоила бы похода в БД на каждый клик, а пропавший объект и так
// не попадёт в List.
func (s *FavoriteService) Add(ctx context.Context, userID uuid.UUID,
	externalID string, chatID *uuid.UUID) error {
	return s.favorites.Add(ctx, userID, externalID, chatID)
}

func (s *FavoriteService) Remove(ctx context.Context, userID uuid.UUID, externalID string) error {
	return s.favorites.Remove(ctx, userID, externalID)
}

// List. total — сколько сохранено всего, ДО отсева пропавших из витрины:
// иначе «показать ещё» врёт о размере списка (та же семантика, что у
// ResultsService.List).
func (s *FavoriteService) List(ctx context.Context, userID uuid.UUID,
	limit, offset int) (objects []FavoriteObject, count, total int, err error) {
	rows, total, err := s.favorites.List(ctx, userID, limit, offset)
	if err != nil {
		return nil, 0, 0, err
	}

	ids := make([]string, len(rows))
	for i, f := range rows {
		ids[i] = f.ExternalID
	}
	listings, err := s.listings.GetByExternalIDs(ctx, ids)
	if err != nil {
		listings = map[string]domain.Listing{}
	}

	objects = make([]FavoriteObject, 0, len(rows))
	for _, f := range rows {
		obj, ok := BuildFavoriteObject(f, listings[f.ExternalID])
		if !ok {
			continue
		}
		objects = append(objects, obj)
	}
	return objects, len(objects), total, nil
}
```

- [ ] **Step 11: Прогнать тест и убедиться, что он проходит**

Run: `cd backend && go test ./internal/service/ -run "TestBuildFavorite|TestFavoriteList" -v`
Expected: PASS (три теста)

- [ ] **Step 12: Написать хендлер**

Создать `backend/internal/http/handlers/favorite_handler.go`:

```go
package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/http/middleware"
	"habitus-backend/internal/service"
)

type FavoriteHandler struct {
	favorites *service.FavoriteService
}

func NewFavoriteHandler(favorites *service.FavoriteService) *FavoriteHandler {
	return &FavoriteHandler{favorites: favorites}
}

const (
	favoritesDefaultLimit = 20
	favoritesMaxLimit     = 100
)

type favoriteRequest struct {
	ChatID string `json:"chat_id"`
}

// Add implements PUT /favorites/{object_id}. PUT, а не POST: сохранение
// идемпотентно, повторный клик по «сохранить» — то же состояние.
func (h *FavoriteHandler) Add(c *fiber.Ctx) error {
	objectID := c.Params("object_id")
	if objectID == "" {
		return apperr.ObjectNotFound()
	}

	// Тело необязательно: объект можно сохранить и с карты, вне подбора.
	var req favoriteRequest
	_ = c.BodyParser(&req)

	var chatID *uuid.UUID
	if req.ChatID != "" {
		parsed, err := uuid.Parse(req.ChatID)
		if err != nil {
			return apperr.ChatNotFound()
		}
		chatID = &parsed
	}

	if err := h.favorites.Add(c.Context(), middleware.UserID(c), objectID, chatID); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *FavoriteHandler) Remove(c *fiber.Ctx) error {
	objectID := c.Params("object_id")
	if objectID == "" {
		return apperr.ObjectNotFound()
	}
	if err := h.favorites.Remove(c.Context(), middleware.UserID(c), objectID); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// List implements GET /favorites?limit=&offset=.
func (h *FavoriteHandler) List(c *fiber.Ctx) error {
	limit, offset := parseLimitOffset(c, favoritesDefaultLimit)
	if limit > favoritesMaxLimit {
		limit = favoritesMaxLimit
	}

	objects, count, total, err := h.favorites.List(c.Context(), middleware.UserID(c), limit, offset)
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"objects": objects, "count": count, "total": total})
}
```

- [ ] **Step 13: Подключить маршруты и сборку**

В `backend/internal/http/router.go` добавить `Favorite *handlers.FavoriteHandler` в `Handlers` и маршруты после гео-ручек:

```go
	// Избранное доступно и гостю: сохранённое переживёт регистрацию — id
	// пользователя при апгрейде не меняется.
	api.Get("/favorites", authMw, h.Favorite.List)
	api.Put("/favorites/:object_id", authMw, h.Favorite.Add)
	api.Delete("/favorites/:object_id", authMw, h.Favorite.Remove)
```

В `backend/internal/app/app.go` добавить `Favorites *service.FavoriteService` в `Services` и `Favorite: handlers.NewFavoriteHandler(svc.Favorites),`. Добавить `PUT` в `AllowMethods` CORS:

```go
		AllowMethods:     "GET,POST,PUT,PATCH,DELETE,OPTIONS",
```

В `backend/cmd/api/main.go`:

```go
	favoriteService := service.NewFavoriteService(repository.NewFavoriteRepo(pool), listingRepo)
```

и `Favorites: favoriteService,` в `app.Services`.

- [ ] **Step 14: Прогнать весь набор**

Run: `cd backend && go test ./...`
Expected: PASS

- [ ] **Step 15: Дописать контракт для фронта**

В `frontend/Пайплайн фронт.md` добавить раздел:

```markdown
### Избранное

|Метод|Эндпоинт|Что делает|
|---|---|---|
|`GET`|`/api/v1/favorites?limit=20&offset=0`|Сохранённые объекты, свежие сверху → `{"objects": FavoriteObject[], "count", "total"}`|
|`PUT`|`/api/v1/favorites/{object_id}`|Сохранить (идемпотентно). Тело `{"chat_id": uuid}` необязательно → `204`|
|`DELETE`|`/api/v1/favorites/{object_id}`|Убрать (идемпотентно) → `204`|

`FavoriteObject` = `{id, name, address, cover_image, coordinates, price_from,
rooms, area_sqm, floor, chat_id, saved_at}`.

**`match_score` и `tags` здесь отсутствуют намеренно** — они принадлежат
конкретному запросу, а не объекту. Показывать процент совпадения в избранном
нечестно: он был посчитан под другой запрос.

`chat_id` — из какого подбора объект сохранён. Передавайте его в
`GET /objects/{id}?chat_id=`, чтобы паспорт открылся с досье; при `null`
открывайте паспорт без `chat_id` (режим «с карты»).

**Гостю избранное доступно.** После регистрации сохранённое остаётся при нём.
```

- [ ] **Step 16: Коммит**

```bash
cd /Users/yarik/PycharmProjects/Habitus
git add backend/migrations/0013_favorites.up.sql backend/migrations/0013_favorites.down.sql \
        backend/internal/domain/domain.go backend/internal/repository/favorite_repo.go \
        backend/internal/repository/favorite_repo_test.go \
        backend/internal/service/display_fields.go backend/internal/service/favorite_service.go \
        backend/internal/service/favorite_service_test.go \
        backend/internal/http/handlers/favorite_handler.go \
        backend/internal/http/router.go backend/internal/app/app.go backend/cmd/api/main.go \
        "frontend/Пайплайн фронт.md"
git commit -m "feat: избранное — объект переживает чат и регистрацию"
```

---

## Task 9: оценка выдачи

Единственный способ узнать, работает ли подбор у живых людей. `uv run habitus eval` меряет качество на golden-set оффлайн; продакшн-сигнала нет ни одного. Оценка ставится в контексте чата — «этот объект под ЭТОТ запрос», иначе она бессмысленна.

**Files:**
- Create: `backend/migrations/0014_result_feedback.up.sql`, `backend/migrations/0014_result_feedback.down.sql`
- Modify: `backend/internal/domain/domain.go` (`ResultFeedback`)
- Create: `backend/internal/repository/feedback_repo.go`
- Create: `backend/internal/repository/feedback_repo_test.go`
- Create: `backend/internal/service/feedback_service.go`
- Create: `backend/internal/service/feedback_service_test.go`
- Create: `backend/internal/http/handlers/feedback_handler.go`
- Modify: `backend/internal/http/router.go`, `backend/internal/app/app.go`, `backend/cmd/api/main.go`
- Modify: `frontend/Пайплайн фронт.md`

**Interfaces:**
- Consumes: `chatOwner` и `fakeChatOwner` (`results_service.go:21`, `results_service_test.go`), `(*repository.ChatSearchRepo).GetResult`, `repository.ErrNotFound`, хелпер `assertAppErrCode` (заведён в `lead_service_test.go`, Task 6)
- Produces: `domain.ResultFeedback`; `(*repository.FeedbackRepo).Upsert(ctx, domain.ResultFeedback) error`; `(*service.FeedbackService).Save(ctx, userID, chatID uuid.UUID, externalID, verdict, reason string) error`; `POST /api/v1/chats/{chat_id}/results/{object_id}/feedback`

- [ ] **Step 1: Написать миграцию**

Создать `backend/migrations/0014_result_feedback.up.sql`:

```sql
-- Оценка объекта в выдаче. Ключ включает chat_id: оценка всегда «этот объект
-- под ЭТОТ запрос», вне запроса она ничего не значит.
CREATE TABLE result_feedback (
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    chat_id     uuid NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    external_id text NOT NULL,
    verdict     text NOT NULL CHECK (verdict IN ('up', 'down')),
    reason      text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, chat_id, external_id)
);

-- Разбор качества подбора идёт по объектам и вердиктам, а не по людям.
CREATE INDEX result_feedback_verdict_ix ON result_feedback (verdict, created_at DESC);
```

Создать `backend/migrations/0014_result_feedback.down.sql`:

```sql
DROP TABLE IF EXISTS result_feedback;
```

- [ ] **Step 2: Написать падающий тест на репозиторий**

Создать `backend/internal/repository/feedback_repo_test.go`:

```go
package repository

import (
	"context"
	"testing"

	"habitus-backend/internal/domain"
)

// Оценку можно передумать: upsert, а не вставка. Иначе второй клик падал бы
// на первичном ключе, и пользователь застревал бы с первым вердиктом.
func TestFeedbackUpsertOverwritesVerdict(t *testing.T) {
	pool := testPool(t)
	users := NewUserRepo(pool)
	chats := NewChatRepo(pool)
	feedback := NewFeedbackRepo(pool)
	ctx := context.Background()

	userID := newTestUser(t, users)
	chat, err := chats.Create(ctx, userID, "msk", "Поиск")
	if err != nil {
		t.Fatalf("создать чат: %v", err)
	}
	externalID := newExternalID()

	if err := feedback.Upsert(ctx, domain.ResultFeedback{
		UserID: userID, ChatID: chat.ID, ExternalID: externalID,
		Verdict: "down", Reason: "далеко от метро",
	}); err != nil {
		t.Fatalf("первая оценка: %v", err)
	}
	if err := feedback.Upsert(ctx, domain.ResultFeedback{
		UserID: userID, ChatID: chat.ID, ExternalID: externalID, Verdict: "up",
	}); err != nil {
		t.Fatalf("вторая оценка: %v", err)
	}

	var verdict, reason string
	err = pool.QueryRow(ctx, `
		SELECT verdict, reason FROM result_feedback
		WHERE user_id = $1 AND chat_id = $2 AND external_id = $3`,
		userID, chat.ID, externalID).Scan(&verdict, &reason)
	if err != nil {
		t.Fatalf("чтение: %v", err)
	}
	if verdict != "up" {
		t.Fatalf("verdict = %q, ожидался up", verdict)
	}
	// Причина от прошлого вердикта не должна прилипать к новому.
	if reason != "" {
		t.Fatalf("reason = %q, ожидалась пустая", reason)
	}
}
```

- [ ] **Step 3: Прогнать тест и убедиться, что он падает**

Run: `cd backend && go test ./internal/repository/ -run TestFeedback -v`
Expected: FAIL — `undefined: NewFeedbackRepo`

- [ ] **Step 4: Добавить domain.ResultFeedback и репозиторий**

В `backend/internal/domain/domain.go`:

```go
// ResultFeedback — оценка объекта в выдаче. Всегда в контексте чата: вне
// запроса «подходит / не подходит» ничего не значит.
type ResultFeedback struct {
	UserID     uuid.UUID
	ChatID     uuid.UUID
	ExternalID string
	Verdict    string
	Reason     string
}
```

Создать `backend/internal/repository/feedback_repo.go`:

```go
package repository

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"habitus-backend/internal/domain"
)

type FeedbackRepo struct {
	pool *pgxpool.Pool
}

func NewFeedbackRepo(pool *pgxpool.Pool) *FeedbackRepo {
	return &FeedbackRepo{pool: pool}
}

// Upsert: оценку можно передумать. Причина перезаписывается вместе с
// вердиктом — иначе к «подходит» прилипло бы объяснение прошлого «не подходит».
func (r *FeedbackRepo) Upsert(ctx context.Context, f domain.ResultFeedback) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO result_feedback(user_id, chat_id, external_id, verdict, reason)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (user_id, chat_id, external_id)
		DO UPDATE SET verdict = EXCLUDED.verdict,
		              reason = EXCLUDED.reason,
		              updated_at = now()`,
		f.UserID, f.ChatID, f.ExternalID, f.Verdict, f.Reason)
	return err
}
```

- [ ] **Step 5: Прогнать тест и убедиться, что он проходит**

Run: `cd backend && go test ./internal/repository/ -run TestFeedback -v`
Expected: PASS

- [ ] **Step 6: Написать падающий тест на сервис**

Создать `backend/internal/service/feedback_service_test.go`:

```go
package service

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/domain"
	"habitus-backend/internal/repository"
)

type fakeFeedbackStore struct {
	saved domain.ResultFeedback
	calls int
}

func (f *fakeFeedbackStore) Upsert(_ context.Context, in domain.ResultFeedback) error {
	f.saved = in
	f.calls++
	return nil
}

type fakeResultGetter struct {
	err error
}

func (f fakeResultGetter) GetResult(context.Context, uuid.UUID, string) (domain.ChatSearchResult, error) {
	return domain.ChatSearchResult{}, f.err
}

func TestFeedbackSaveStoresVerdict(t *testing.T) {
	store := &fakeFeedbackStore{}
	svc := &FeedbackService{chats: fakeChatOwner{}, results: fakeResultGetter{}, feedback: store}
	userID, chatID := uuid.New(), uuid.New()

	if err := svc.Save(context.Background(), userID, chatID, "cian_1", "down", "далеко от метро"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if store.saved.Verdict != "down" || store.saved.Reason != "далеко от метро" {
		t.Fatalf("сохранено %+v", store.saved)
	}
	if store.saved.UserID != userID || store.saved.ChatID != chatID {
		t.Fatalf("контекст оценки потерян: %+v", store.saved)
	}
}

func TestFeedbackSaveRejectsUnknownVerdict(t *testing.T) {
	svc := &FeedbackService{chats: fakeChatOwner{}, results: fakeResultGetter{}, feedback: &fakeFeedbackStore{}}

	err := svc.Save(context.Background(), uuid.New(), uuid.New(), "cian_1", "maybe", "")

	assertAppErrCode(t, err, "validation_error")
}

// Чужой чат — 404 chat_not_found, тот же приём, что у остальных ручек чата.
func TestFeedbackSaveRejectsForeignChat(t *testing.T) {
	svc := &FeedbackService{
		chats:    fakeChatOwner{err: apperr.ChatNotFound()},
		results:  fakeResultGetter{},
		feedback: &fakeFeedbackStore{},
	}

	err := svc.Save(context.Background(), uuid.New(), uuid.New(), "cian_1", "up", "")

	assertAppErrCode(t, err, "chat_not_found")
}

// Оценивать объект, которого в этом подборе не было, нельзя: такая оценка —
// мусор в данных о качестве подбора.
func TestFeedbackSaveRejectsObjectOutsideChat(t *testing.T) {
	svc := &FeedbackService{
		chats:    fakeChatOwner{},
		results:  fakeResultGetter{err: repository.ErrNotFound},
		feedback: &fakeFeedbackStore{},
	}

	err := svc.Save(context.Background(), uuid.New(), uuid.New(), "cian_1", "up", "")

	assertAppErrCode(t, err, "object_not_found")
}
```

- [ ] **Step 7: Прогнать тест и убедиться, что он падает**

Run: `cd backend && go test ./internal/service/ -run TestFeedbackSave -v`
Expected: FAIL — `undefined: FeedbackService`

- [ ] **Step 8: Реализовать FeedbackService**

Создать `backend/internal/service/feedback_service.go`:

```go
// feedback_service.go — оценка объекта в выдаче. Единственный продакшн-сигнал
// о том, работает ли подбор: eval меряет качество на golden-set оффлайн, а
// что думают живые люди, до этого было неизвестно.
package service

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/domain"
	"habitus-backend/internal/repository"
)

const feedbackReasonMaxLen = 500

// resultGetter — часть ChatSearchRepo: убедиться, что объект вообще был в
// этом подборе.
type resultGetter interface {
	GetResult(ctx context.Context, chatID uuid.UUID, externalID string) (domain.ChatSearchResult, error)
}

// feedbackStore — часть FeedbackRepo.
type feedbackStore interface {
	Upsert(ctx context.Context, f domain.ResultFeedback) error
}

type FeedbackService struct {
	chats    chatOwner
	results  resultGetter
	feedback feedbackStore
}

func NewFeedbackService(chats *ChatService, results *repository.ChatSearchRepo,
	feedback *repository.FeedbackRepo) *FeedbackService {
	return &FeedbackService{chats: chats, results: results, feedback: feedback}
}

func (s *FeedbackService) Save(ctx context.Context, userID, chatID uuid.UUID,
	externalID, verdict, reason string) error {
	if verdict != "up" && verdict != "down" {
		return apperr.Validation("verdict должен быть 'up' или 'down'")
	}
	reason = strings.TrimSpace(reason)
	if len(reason) > feedbackReasonMaxLen {
		return apperr.Validation("Слишком длинное объяснение оценки")
	}

	if _, err := s.chats.GetOwned(ctx, userID, chatID); err != nil {
		return err
	}
	// Объект должен быть в выдаче этого чата: оценка объекта, которого тут не
	// показывали, — мусор в данных о качестве подбора.
	if _, err := s.results.GetResult(ctx, chatID, externalID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return apperr.ObjectNotFound()
		}
		return err
	}

	return s.feedback.Upsert(ctx, domain.ResultFeedback{
		UserID: userID, ChatID: chatID, ExternalID: externalID,
		Verdict: verdict, Reason: reason,
	})
}
```

- [ ] **Step 9: Прогнать тест и убедиться, что он проходит**

Run: `cd backend && go test ./internal/service/ -run TestFeedbackSave -v`
Expected: PASS (четыре теста)

- [ ] **Step 10: Написать хендлер и подключить маршрут**

Создать `backend/internal/http/handlers/feedback_handler.go`:

```go
package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/http/middleware"
	"habitus-backend/internal/service"
)

type FeedbackHandler struct {
	feedback *service.FeedbackService
}

func NewFeedbackHandler(feedback *service.FeedbackService) *FeedbackHandler {
	return &FeedbackHandler{feedback: feedback}
}

type feedbackRequest struct {
	Verdict string `json:"verdict"`
	Reason  string `json:"reason"`
}

// Save implements POST /chats/{chat_id}/results/{object_id}/feedback.
func (h *FeedbackHandler) Save(c *fiber.Ctx) error {
	chatID, err := uuid.Parse(c.Params("chat_id"))
	if err != nil {
		return apperr.ChatNotFound()
	}
	objectID := c.Params("object_id")
	if objectID == "" {
		return apperr.ObjectNotFound()
	}
	var req feedbackRequest
	if err := c.BodyParser(&req); err != nil {
		return apperr.Validation("invalid request body")
	}

	if err := h.feedback.Save(c.Context(), middleware.UserID(c), chatID,
		objectID, req.Verdict, req.Reason); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}
```

В `backend/internal/http/router.go` добавить `Feedback *handlers.FeedbackHandler` в `Handlers` и маршрут рядом с `/chats/:chat_id/results`:

```go
	api.Post("/chats/:chat_id/results/:object_id/feedback", authMw, h.Feedback.Save)
```

В `backend/internal/app/app.go` — `Feedback *service.FeedbackService` в `Services` и `Feedback: handlers.NewFeedbackHandler(svc.Feedback),`.

В `backend/cmd/api/main.go`:

```go
	feedbackService := service.NewFeedbackService(chatService, chatSearchRepo,
		repository.NewFeedbackRepo(pool))
```

и `Feedback: feedbackService,` в `app.Services`.

- [ ] **Step 11: Прогнать весь набор**

Run: `cd backend && go test ./...`
Expected: PASS

- [ ] **Step 12: Дописать контракт для фронта**

В `frontend/Пайплайн фронт.md` добавить:

```markdown
### Оценка объекта в выдаче

- **Эндпоинт:** `POST /api/v1/chats/{chat_id}/results/{object_id}/feedback`
- **Тело:** `{"verdict": "up" | "down", "reason": "далеко от метро"}` — `reason`
  необязателен, до 500 символов.
- **Ответ:** `204`. Повторная отправка перезаписывает оценку — пользователь
  может передумать.
- Оценивать можно только объект, который был в выдаче этого чата: иначе
  `404 object_not_found`.
- Доступно гостю.
```

- [ ] **Step 13: Коммит**

```bash
cd /Users/yarik/PycharmProjects/Habitus
git add backend/migrations/0014_result_feedback.up.sql backend/migrations/0014_result_feedback.down.sql \
        backend/internal/domain/domain.go backend/internal/repository/feedback_repo.go \
        backend/internal/repository/feedback_repo_test.go \
        backend/internal/service/feedback_service.go backend/internal/service/feedback_service_test.go \
        backend/internal/http/handlers/feedback_handler.go \
        backend/internal/http/router.go backend/internal/app/app.go backend/cmd/api/main.go \
        "frontend/Пайплайн фронт.md"
git commit -m "feat: оценка объекта в выдаче — продакшн-сигнал о качестве подбора"
```

---

## Task 10: журнал продуктовых событий

Метрики в `observability/` технические: латентность, `degraded`, 429. На вопрос «доходит ли человек от поиска до заявки» они не отвечают. Журнал пишется **неблокирующе**: телеметрия не имеет права замедлить или уронить запрос, поэтому переполненный буфер теряет событие и пишет об этом в лог — это осознанный размен.

**Files:**
- Create: `backend/migrations/0015_product_events.up.sql`, `backend/migrations/0015_product_events.down.sql`
- Modify: `backend/internal/domain/domain.go` (`ProductEvent`)
- Create: `backend/internal/repository/event_repo.go`
- Create: `backend/internal/repository/event_repo_test.go`
- Create: `backend/internal/service/events.go`
- Create: `backend/internal/service/events_test.go`

**Interfaces:**
- Produces: `domain.ProductEvent{UserID uuid.UUID; IsGuest bool; Kind string; ChatID *uuid.UUID; ExternalID string; Props map[string]any}`; `(*repository.EventRepo).Insert(ctx, domain.ProductEvent) error`; `service.NewEventRecorder(store service.eventStore, buffer int) *service.EventRecorder`; `(*EventRecorder).Record(e domain.ProductEvent)` — не блокирует; `(*EventRecorder).Start(ctx)`; константы имён событий `service.EventGuestCreated` и т.д.

- [ ] **Step 1: Написать миграцию**

Создать `backend/migrations/0015_product_events.up.sql`:

```sql
-- Журнал продуктовых событий: воронка от поиска до заявки. Технические
-- метрики (latency, degraded, 429) на вопрос «дошёл ли человек» не отвечают.
CREATE TABLE product_events (
    id          bigserial PRIMARY KEY,
    -- SET NULL, а не CASCADE: свипер вычищает брошенных гостей, но воронка
    -- за прошлый месяц от этого обнуляться не должна.
    user_id     uuid REFERENCES users(id) ON DELETE SET NULL,
    -- Признак гостя хранится копией: после удаления пользователя восстановить
    -- его по джойну будет уже не с чем.
    is_guest    boolean NOT NULL DEFAULT false,
    kind        text NOT NULL,
    chat_id     uuid,
    external_id text,
    props       jsonb NOT NULL DEFAULT '{}',
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX product_events_kind_ix ON product_events (kind, created_at DESC);
CREATE INDEX product_events_user_ix ON product_events (user_id, created_at DESC);
```

Создать `backend/migrations/0015_product_events.down.sql`:

```sql
DROP TABLE IF EXISTS product_events;
```

- [ ] **Step 2: Написать падающий тест на репозиторий**

Создать `backend/internal/repository/event_repo_test.go`:

```go
package repository

import (
	"context"
	"testing"

	"habitus-backend/internal/domain"
)

func TestEventInsert(t *testing.T) {
	pool := testPool(t)
	users := NewUserRepo(pool)
	events := NewEventRepo(pool)
	ctx := context.Background()

	userID := newTestUser(t, users)
	externalID := newExternalID()

	if err := events.Insert(ctx, domain.ProductEvent{
		UserID: userID, IsGuest: true, Kind: "passport_opened",
		ExternalID: externalID, Props: map[string]any{"contact_kind": "lead"},
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	var kind, contactKind string
	var isGuest bool
	err := pool.QueryRow(ctx, `
		SELECT kind, is_guest, props->>'contact_kind'
		FROM product_events WHERE user_id = $1 AND external_id = $2`,
		userID, externalID).Scan(&kind, &isGuest, &contactKind)
	if err != nil {
		t.Fatalf("чтение: %v", err)
	}
	if kind != "passport_opened" || !isGuest || contactKind != "lead" {
		t.Fatalf("записано kind=%q is_guest=%v props.contact_kind=%q", kind, isGuest, contactKind)
	}
}

// Событие без пользователя — законное состояние (нулевой uuid означает
// «актор неизвестен»), и падать на нём нельзя: телеметрия не критична.
func TestEventInsertAllowsMissingUser(t *testing.T) {
	pool := testPool(t)
	events := NewEventRepo(pool)

	if err := events.Insert(context.Background(), domain.ProductEvent{
		Kind: "search_started",
	}); err != nil {
		t.Fatalf("Insert без пользователя: %v", err)
	}
}
```

- [ ] **Step 3: Прогнать тест и убедиться, что он падает**

Run: `cd backend && go test ./internal/repository/ -run TestEventInsert -v`
Expected: FAIL — `undefined: NewEventRepo`

- [ ] **Step 4: Добавить domain.ProductEvent и репозиторий**

В `backend/internal/domain/domain.go`:

```go
// ProductEvent — шаг воронки. UserID == uuid.Nil означает «актор неизвестен»
// и записывается как NULL: телеметрия не имеет права падать из-за этого.
type ProductEvent struct {
	UserID     uuid.UUID
	IsGuest    bool
	Kind       string
	ChatID     *uuid.UUID
	ExternalID string
	Props      map[string]any
}
```

Создать `backend/internal/repository/event_repo.go`:

```go
package repository

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"habitus-backend/internal/domain"
)

type EventRepo struct {
	pool *pgxpool.Pool
}

func NewEventRepo(pool *pgxpool.Pool) *EventRepo {
	return &EventRepo{pool: pool}
}

func (r *EventRepo) Insert(ctx context.Context, e domain.ProductEvent) error {
	var userID any
	if e.UserID != uuid.Nil {
		userID = e.UserID
	}
	var externalID any
	if e.ExternalID != "" {
		externalID = e.ExternalID
	}
	props := e.Props
	if props == nil {
		props = map[string]any{}
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO product_events(user_id, is_guest, kind, chat_id, external_id, props)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		userID, e.IsGuest, e.Kind, e.ChatID, externalID, props)
	return err
}
```

- [ ] **Step 5: Прогнать тест и убедиться, что он проходит**

Run: `cd backend && go test ./internal/repository/ -run TestEventInsert -v`
Expected: PASS

- [ ] **Step 6: Написать падающий тест на рекордер**

Создать `backend/internal/service/events_test.go`:

```go
package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"habitus-backend/internal/domain"
)

type fakeEventStore struct {
	mu      sync.Mutex
	got     []domain.ProductEvent
	err     error
	written chan struct{}
}

func newFakeEventStore(err error) *fakeEventStore {
	return &fakeEventStore{err: err, written: make(chan struct{}, 16)}
}

func (f *fakeEventStore) Insert(_ context.Context, e domain.ProductEvent) error {
	f.mu.Lock()
	f.got = append(f.got, e)
	f.mu.Unlock()
	select {
	case f.written <- struct{}{}:
	default:
	}
	return f.err
}

func (f *fakeEventStore) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.got)
}

func TestEventRecorderWritesAsynchronously(t *testing.T) {
	store := newFakeEventStore(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec := NewEventRecorder(store, 8)
	rec.Start(ctx)

	rec.Record(domain.ProductEvent{Kind: EventLeadSent, ExternalID: "cian_1"})

	select {
	case <-store.written:
	case <-time.After(2 * time.Second):
		t.Fatal("событие не записалось")
	}
	if store.count() != 1 {
		t.Fatalf("записано %d событий, ожидалось 1", store.count())
	}
}

// Переполненный буфер теряет событие и НЕ блокирует вызывающего: телеметрия
// не имеет права задержать ответ пользователю.
func TestEventRecorderDoesNotBlockOnFullBuffer(t *testing.T) {
	blocked := make(chan struct{})
	store := &blockingEventStore{release: blocked}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec := NewEventRecorder(store, 1)
	rec.Start(ctx)

	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			rec.Record(domain.ProductEvent{Kind: EventSearchStarted})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Record заблокировался на полном буфере")
	}
	close(blocked)
}

// Ошибка записи не роняет воркер: моргнувшая БД не должна выключать
// телеметрию до конца жизни процесса.
func TestEventRecorderSurvivesStoreError(t *testing.T) {
	store := newFakeEventStore(errors.New("db down"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec := NewEventRecorder(store, 8)
	rec.Start(ctx)

	rec.Record(domain.ProductEvent{Kind: EventSearchStarted})
	rec.Record(domain.ProductEvent{Kind: EventPassportOpened})

	deadline := time.After(2 * time.Second)
	for i := 0; i < 2; i++ {
		select {
		case <-store.written:
		case <-deadline:
			t.Fatalf("после ошибки записано только %d событий", i)
		}
	}
}

// Нулевой рекордер — законное состояние (телеметрия выключена в тестах,
// собирающих сервисы напрямую): Record на нём не должен паниковать.
func TestNilEventRecorderIsSafe(t *testing.T) {
	var rec *EventRecorder
	rec.Record(domain.ProductEvent{Kind: EventSearchStarted})
}

type blockingEventStore struct {
	release chan struct{}
}

func (b *blockingEventStore) Insert(context.Context, domain.ProductEvent) error {
	<-b.release
	return nil
}
```

- [ ] **Step 7: Прогнать тест и убедиться, что он падает**

Run: `cd backend && go test ./internal/service/ -run "TestEventRecorder|TestNilEventRecorder" -v`
Expected: FAIL — `undefined: NewEventRecorder`

- [ ] **Step 8: Реализовать рекордер**

Создать `backend/internal/service/events.go`:

```go
// events.go — журнал продуктовых событий: воронка от поиска до заявки.
// Технические метрики (latency, degraded, 429) на вопрос «дошёл ли человек до
// конца» не отвечают, а `uv run habitus eval` меряет качество оффлайн.
//
// Запись неблокирующая и через собственный контекст: контекст HTTP-запроса
// умирает сразу после ответа, и запись «в хвосте» на нём терялась бы гонкой.
package service

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"

	"habitus-backend/internal/domain"
)

// Имена событий. Строки, а не enum: журнал читается SQL-запросами, и константы
// нужны только чтобы не разъехались написания в разных хендлерах.
const (
	EventGuestCreated   = "guest_created"
	EventGuestUpgraded  = "guest_upgraded"
	EventSearchStarted  = "search_started"
	EventPassportOpened = "passport_opened"
	EventFavoriteAdded  = "favorite_added"
	EventFeedbackGiven  = "feedback_given"
	EventLeadSent       = "lead_sent"
)

// eventStore — часть EventRepo.
type eventStore interface {
	Insert(ctx context.Context, e domain.ProductEvent) error
}

// eventWriteTimeout — предел на одну запись. Без него зависший INSERT
// остановил бы воркер навсегда, и журнал молча перестал бы наполняться.
const eventWriteTimeout = 5 * time.Second

type EventRecorder struct {
	store  eventStore
	events chan domain.ProductEvent
}

// NewEventRecorder. buffer — сколько событий переживут всплеск нагрузки;
// переполнение теряет событие, и это осознанный размен: телеметрия не имеет
// права замедлить ответ пользователю.
func NewEventRecorder(store eventStore, buffer int) *EventRecorder {
	if buffer <= 0 {
		buffer = 1024
	}
	return &EventRecorder{store: store, events: make(chan domain.ProductEvent, buffer)}
}

// Start поднимает единственного писателя. Один, а не пул: журнал — это
// последовательные вставки, и конкуренция за соединения пула тут ни к чему.
func (r *EventRecorder) Start(ctx context.Context) {
	if r == nil {
		return
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case e := <-r.events:
				r.write(e)
			}
		}
	}()
}

func (r *EventRecorder) write(e domain.ProductEvent) {
	// Собственный контекст: запрос, породивший событие, уже завершён.
	ctx, cancel := context.WithTimeout(context.Background(), eventWriteTimeout)
	defer cancel()
	if err := r.store.Insert(ctx, e); err != nil {
		// Логируем и живём дальше: моргнувшая БД не должна выключать
		// телеметрию до конца жизни процесса.
		log.Error().Err(err).Str("kind", e.Kind).Msg("product event write failed")
	}
}

// Record никогда не блокирует и никогда не паникует — в том числе на nil
// рекордере (телеметрия выключена: так собраны тесты сервисов).
func (r *EventRecorder) Record(e domain.ProductEvent) {
	if r == nil {
		return
	}
	select {
	case r.events <- e:
	default:
		log.Warn().Str("kind", e.Kind).Msg("product event dropped: buffer full")
	}
}
```

- [ ] **Step 9: Прогнать тест и убедиться, что он проходит**

Run: `cd backend && go test ./internal/service/ -run "TestEventRecorder|TestNilEventRecorder" -v`
Expected: PASS (четыре теста)

- [ ] **Step 10: Прогнать весь набор**

Run: `cd backend && go test ./...`
Expected: PASS

- [ ] **Step 11: Коммит**

```bash
cd /Users/yarik/PycharmProjects/Habitus
git add backend/migrations/0015_product_events.up.sql backend/migrations/0015_product_events.down.sql \
        backend/internal/domain/domain.go backend/internal/repository/event_repo.go \
        backend/internal/repository/event_repo_test.go \
        backend/internal/service/events.go backend/internal/service/events_test.go
git commit -m "feat: неблокирующий журнал продуктовых событий"
```

---

## Task 11: расставить события по воронке

События пишутся **в хендлерах**, а не в сервисах: только там известен признак гостя (`middleware.IsGuest`), и только там точка событий одна на действие.

`search_completed` намеренно НЕ вводится: каждый завершившийся поиск уже лежит в `chat_searches`, а его выдача — в `chat_search_results`. Дублировать в журнал то, что и так в БД, значит завести второй источник правды о том же факте.

**Files:**
- Modify: `backend/internal/http/handlers/auth_handler.go`
- Modify: `backend/internal/http/handlers/stream_handler.go`
- Modify: `backend/internal/http/handlers/object_handler.go`
- Modify: `backend/internal/http/handlers/favorite_handler.go`
- Modify: `backend/internal/http/handlers/feedback_handler.go`
- Modify: `backend/internal/http/handlers/lead_handler.go`
- Modify: `backend/internal/app/app.go`, `backend/cmd/api/main.go`
- Create: `docs/notes/funnel-queries.md`

**Interfaces:**
- Consumes: `service.EventRecorder`, константы имён событий (Task 10); `middleware.UserID`, `middleware.IsGuest` (Task 4)
- Produces: конструкторы хендлеров принимают `*service.EventRecorder` последним аргументом: `NewAuthHandler(auth, cookieSecure, events)`, `NewStreamHandler(chat, stream, events)`, `NewObjectHandler(objects, events)`, `NewFavoriteHandler(favorites, events)`, `NewFeedbackHandler(feedback, events)`, `NewLeadHandler(leads, auth, cookieSecure, events)`

- [ ] **Step 1: Добавить хелпер записи в хендлерах**

Создать `backend/internal/http/handlers/events.go`:

```go
package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"habitus-backend/internal/domain"
	"habitus-backend/internal/http/middleware"
	"habitus-backend/internal/service"
)

// recordEvent пишет шаг воронки из контекста запроса: только здесь известен
// признак гостя. Рекордер может быть nil (телеметрия выключена в тестах) —
// Record это переживает.
func recordEvent(c *fiber.Ctx, rec *service.EventRecorder, kind string,
	chatID *uuid.UUID, externalID string, props map[string]any) {
	rec.Record(domain.ProductEvent{
		UserID:     middleware.UserID(c),
		IsGuest:    middleware.IsGuest(c),
		Kind:       kind,
		ChatID:     chatID,
		ExternalID: externalID,
		Props:      props,
	})
}
```

- [ ] **Step 2: Записать события гостя и регистрации**

В `backend/internal/http/handlers/auth_handler.go` добавить поле `events *service.EventRecorder` в `AuthHandler`, принять его в `NewAuthHandler` третьим аргументом, и записать:

в `Guest`, сразу после `h.setSessionCookie(...)` в ветке создания нового гостя:

```go
	// Событие пишется до ответа, но не блокирует его: Record кладёт в буфер.
	// UserID из Locals тут ещё нет (запрос шёл без сессии), поэтому явно.
	h.events.Record(domain.ProductEvent{UserID: u.ID, IsGuest: true, Kind: service.EventGuestCreated})
```

в `Register`, после успешного апгрейда (внутри ветки `guestID != uuid.Nil`, после `h.setSessionCookie`):

```go
	if guestID != uuid.Nil {
		h.events.Record(domain.ProductEvent{UserID: u.ID, Kind: service.EventGuestUpgraded})
	}
```

Добавить импорты `"habitus-backend/internal/domain"` и `"habitus-backend/internal/service"`, если их ещё нет.

- [ ] **Step 3: Записать начало поиска**

В `backend/internal/http/handlers/stream_handler.go` добавить поле `events *service.EventRecorder`, принять его в `NewStreamHandler` третьим аргументом, и перед `h.stream.Run(...)` записать:

```go
	chatID := chat.ID
	recordEvent(c, h.events, service.EventSearchStarted, &chatID, "",
		map[string]any{"has_point": point != nil, "text_len": len(text)})
```

Текст запроса в журнал НЕ кладём: он целиком лежит в `messages` и `chat_searches.raw_query`, и копия в третьем месте — лишний источник правды о том же.

- [ ] **Step 4: Записать открытие паспорта**

В `backend/internal/http/handlers/object_handler.go` добавить поле `events *service.EventRecorder`, принять его в `NewObjectHandler` вторым аргументом, и перед `return c.JSON(passport)` записать:

```go
	var chatIDPtr *uuid.UUID
	if chatID != uuid.Nil {
		chatIDPtr = &chatID
	}
	// contact.kind в свойствах — по нему видно, у скольких открытых объектов
	// вообще был путь к продавцу: без этого падение конверсии в заявку не
	// отличить от «заявку было некуда отправить».
	recordEvent(c, h.events, service.EventPassportOpened, chatIDPtr, objectID,
		map[string]any{"contact_kind": passport.Contact.Kind})
```

- [ ] **Step 5: Записать сохранение, оценку и заявку**

В `backend/internal/http/handlers/favorite_handler.go` — поле `events`, второй аргумент конструктора, и в `Add` перед `return c.SendStatus(fiber.StatusNoContent)`:

```go
	recordEvent(c, h.events, service.EventFavoriteAdded, chatID, objectID, nil)
```

В `backend/internal/http/handlers/feedback_handler.go` — то же, и в `Save` перед возвратом:

```go
	recordEvent(c, h.events, service.EventFeedbackGiven, &chatID, objectID,
		map[string]any{"verdict": req.Verdict, "has_reason": req.Reason != ""})
```

В `backend/internal/http/handlers/lead_handler.go` — то же (поле `events`,
ЧЕТВЁРТЫЙ аргумент конструктора после `leads`, `auth`, `cookieSecure`), и в
`Send` перед возвратом:

```go
	// Регистрация из формы заявки — тот же шаг воронки, что и обычный апгрейд
	// гостя, и считаться должен вместе с ним, иначе конверсия гостей в аккаунты
	// окажется занижена ровно на самых ценных.
	if registered {
		h.events.Record(domain.ProductEvent{UserID: userID, Kind: service.EventGuestUpgraded,
			Props: map[string]any{"source": "lead_form"}})
	}
	recordEvent(c, h.events, service.EventLeadSent, nil, objectID,
		map[string]any{"has_message": req.Message != "", "registered_inline": registered})
```

Добавить в этот файл импорт `"habitus-backend/internal/domain"`, если его ещё нет.

Событие `lead_sent` пишется с `UserID` из `middleware.UserID(c)` — у только что
зарегистрированного гостя это тот же id, что и был: апгрейд его не меняет.

Во все три файла добавить импорт `"habitus-backend/internal/service"`, если его ещё нет.

- [ ] **Step 6: Прокинуть рекордер через сборку**

В `backend/internal/app/app.go` добавить в `Services`:

```go
	// Events может быть nil — телеметрия выключена (так собраны тесты,
	// строящие app.Services{} напрямую).
	Events *service.EventRecorder
```

и передать во все шесть конструкторов:

```go
	httpapi.RegisterRoutes(app, httpapi.Handlers{
		Health:    handlers.NewHealthHandler(ready),
		Auth:      handlers.NewAuthHandler(svc.Auth, cfg.SessionCookieSecure, svc.Events),
		Chat:      handlers.NewChatHandler(svc.Chat),
		Stream:    handlers.NewStreamHandler(svc.Chat, svc.Stream, svc.Events),
		Object:    handlers.NewObjectHandler(svc.Object, svc.Events),
		ObjectAsk: handlers.NewObjectAskHandler(svc.Object, svc.ObjectAsk),
		Geo:       handlers.NewGeoHandler(svc.GeoLayers),
		Results:   handlers.NewResultsHandler(svc.Results),
		Owner:     handlers.NewOwnerHandler(svc.OwnerListings, svc.OwnerImports, svc.OwnerPhotos),
		Lead:      handlers.NewLeadHandler(svc.Leads, svc.Auth, cfg.SessionCookieSecure, svc.Events),
		Favorite:  handlers.NewFavoriteHandler(svc.Favorites, svc.Events),
		Feedback:  handlers.NewFeedbackHandler(svc.Feedback, svc.Events),
	}, svc.Auth, middleware.RateLimitLLM(rateLimiter, guestLimiter))
```

В `backend/cmd/api/main.go`:

```go
	eventRecorder := service.NewEventRecorder(repository.NewEventRepo(pool), 1024)
	eventRecorder.Start(ctx)
```

и `Events: eventRecorder,` в `app.Services`.

- [ ] **Step 7: Прогнать весь набор**

Run: `cd backend && go test ./...`
Expected: PASS

- [ ] **Step 8: Написать запросы к воронке**

Создать `docs/notes/funnel-queries.md`:

````markdown
# Воронка MVP: как её читать

Журнал — `product_events` (Go-миграция `0015`). События пишутся в хендлерах:
`guest_created`, `guest_upgraded`, `search_started`, `passport_opened`,
`favorite_added`, `feedback_given`, `lead_sent`.

Чего в журнале НЕТ намеренно: завершённые поиски и их выдача уже лежат в
`chat_searches` / `chat_search_results`, а текст запроса — в `messages` и
`chat_searches.raw_query`. Дублировать это событием значило бы завести второй
источник правды об одном факте.

Подключение: `psql postgresql://habitus:habitus@localhost:5544/habitus`.

## Воронка за период, по людям

```sql
SELECT
    count(DISTINCT user_id) FILTER (WHERE kind = 'search_started')  AS искали,
    count(DISTINCT user_id) FILTER (WHERE kind = 'passport_opened') AS открыли_паспорт,
    count(DISTINCT user_id) FILTER (WHERE kind = 'favorite_added')  AS сохранили,
    count(DISTINCT user_id) FILTER (WHERE kind = 'lead_sent')       AS отправили_заявку
FROM product_events
WHERE created_at >= now() - interval '30 days';
```

## Гость против зарегистрированного

Проверяет главную ставку гостевого входа: доходит ли аноним до ценности.

```sql
SELECT is_guest,
       count(DISTINCT user_id) FILTER (WHERE kind = 'search_started')  AS искали,
       count(DISTINCT user_id) FILTER (WHERE kind = 'passport_opened') AS открыли_паспорт
FROM product_events
WHERE created_at >= now() - interval '30 days'
GROUP BY is_guest;
```

## Конверсия гостя в аккаунт

```sql
SELECT
    count(*) FILTER (WHERE kind = 'guest_created')  AS гостей,
    count(*) FILTER (WHERE kind = 'guest_upgraded') AS зарегистрировались
FROM product_events
WHERE created_at >= now() - interval '30 days';
```

Откуда пришла регистрация. `lead_form` — человек завёл аккаунт прямо в форме
заявки: это самая ценная половина, и держать её в одной куче с обычной
регистрацией значит не увидеть, работает ли эта точка входа.

```sql
SELECT COALESCE(props->>'source', 'auth_form') AS откуда, count(*) AS сколько
FROM product_events
WHERE kind = 'guest_upgraded' AND created_at >= now() - interval '30 days'
GROUP BY 1 ORDER BY 2 DESC;
```

## Было ли куда отправлять заявку

Если конверсия в заявку низкая, сначала смотреть сюда: возможно, у открытых
объектов просто не было продавца в системе.

```sql
SELECT props->>'contact_kind' AS способ_связи, count(*) AS открытий
FROM product_events
WHERE kind = 'passport_opened' AND created_at >= now() - interval '30 days'
GROUP BY 1 ORDER BY 2 DESC;
```

## Качество подбора глазами пользователей

```sql
SELECT verdict, count(*) AS оценок,
       count(*) FILTER (WHERE reason <> '') AS с_объяснением
FROM result_feedback
WHERE created_at >= now() - interval '30 days'
GROUP BY verdict;
```

Причины отказов — что чинить в первую очередь:

```sql
SELECT reason, count(*) AS сколько_раз
FROM result_feedback
WHERE verdict = 'down' AND reason <> ''
  AND created_at >= now() - interval '30 days'
GROUP BY reason ORDER BY 2 DESC LIMIT 20;
```

## Доля пустых выдач

Из `chat_search_results`, не из журнала: поиски там и так все.

```sql
SELECT count(*) AS поисков,
       count(*) FILTER (WHERE r.n = 0) AS пустых
FROM chat_searches cs
LEFT JOIN LATERAL (
    SELECT count(*) AS n FROM chat_search_results csr WHERE csr.search_id = cs.id
) r ON true
WHERE cs.created_at >= now() - interval '30 days';
```
````

- [ ] **Step 9: Проверить журнал на живом стеке**

```bash
# пройти путь гостя целиком: /auth/guest → поиск → паспорт → избранное
# затем посмотреть, что записалось:
psql postgresql://habitus:habitus@localhost:5544/habitus -c \
  "SELECT kind, is_guest, external_id, props, created_at FROM product_events ORDER BY id DESC LIMIT 20;"
# ожидается: guest_created, search_started, passport_opened, favorite_added
```

- [ ] **Step 10: Коммит**

```bash
cd /Users/yarik/PycharmProjects/Habitus
git add backend/internal/http/handlers/events.go backend/internal/http/handlers/auth_handler.go \
        backend/internal/http/handlers/stream_handler.go backend/internal/http/handlers/object_handler.go \
        backend/internal/http/handlers/favorite_handler.go backend/internal/http/handlers/feedback_handler.go \
        backend/internal/http/handlers/lead_handler.go \
        backend/internal/app/app.go backend/cmd/api/main.go docs/notes/funnel-queries.md
git commit -m "feat: воронка от поиска до заявки пишется в журнал событий"
```

---

## Приёмка

После всех задач — один прогон на поднятом стеке.

- [ ] `cd backend && go test ./...` — без падений, с поднятым Postgres (иначе репозиторные тесты скипаются и половина покрытия не исполняется)
- [ ] `uv run pytest` — Python-часть не задета, но миграции Go накатываются на ту же базу
- [ ] `cd frontend && npm test` — контракт не сломан
- [ ] `docker compose up` с нуля: `backend` становится healthy по `/health/ready`
- [ ] `curl -s localhost:8080/health/ready | jq` → `{"status":"ready","checks":{"db":"ok","ml":"ok"}}`
- [ ] Погасить `ml-service` → `/health/ready` отдаёт 503 и называет `ml`
- [ ] Путь гостя: `/auth/guest` → поиск → паспорт → избранное → регистрация; `id` до и после регистрации совпадает, чаты и избранное на месте
- [ ] Путь заявки от зарегистрированного: продавец публикует объявление → покупатель видит `contact.kind == "lead"` → отправляет заявку → продавец видит её в `GET /owner/leads`; повтор даёт 409
- [ ] Путь заявки от гостя: `/auth/guest` → паспорт → заявка без `register` даёт **403 `registration_required`** → повтор с `register` даёт **201 `{"registered": true}`**; заявка у продавца на месте, `/me` отдаёт `is_guest: false` с тем же `id`
- [ ] Заявка гостя с пустым `contact` и заполненным `register` даёт 400 и **аккаунт при этом не заводится** (`/me` по-прежнему `is_guest: true`)
- [ ] Витринный объект: `contact.kind == "external"` и непустой `source_url`
- [ ] `psql ... -f` запросы из `docs/notes/funnel-queries.md` отрабатывают и показывают ненулевую воронку

## Что этот план сознательно НЕ делает

Названо явно, чтобы не считалось забытым:

- **Уведомления продавцу о заявке** (почта, телеграм) — заявка видна в кабинете; канал доставки требует отдельного решения об SMTP/боте.
- **Модерация витрины и лимит объявлений на продавца** — `Publish` по-прежнему кладёт объявление в поиск без проверок. Отдельный план.
- **IP-лимит на `/auth/login` и `/auth/register`** — существующий `RateLimiter` ключуется по `user_id` и логин защитить не может. Отдельная задача: вариант лимитера с ключом по IP.
- **Смена и восстановление пароля, удаление аккаунта** — отдельный план.
- **Фронт.** Все контракты дописаны в `frontend/Пайплайн фронт.md`, но ни одна ручка из этого плана не имеет потребителя, пока фронт не подключён. Как и в прошлый раз (`docs/notes/frontend-gaps-2026-08-22.md`), сделанная серверная половина без фронта до пользователя не доходит — это следующий план, а не хвост этого.
