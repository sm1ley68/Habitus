// partner.go — сквозной слой Partner API: ключ, права, лимиты, идемпотентность
// и конверт ошибки. Всё, что отличает публичный B2B-контур от внутреннего
// API фронта, живёт здесь, а не расползается по хендлерам.
package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/domain"
	"habitus-backend/internal/observability"
	"habitus-backend/internal/repository"
	"habitus-backend/internal/service"
)

const (
	PartnerIdentityLocalsKey = "partner_identity"
	// RequestIDHeader — тот же заголовок, что ставит fiber/requestid. Наружу
	// он уходит и в конверте ошибки: без общего идентификатора обращение в
	// поддержку начинается с «примерно в час дня что-то не сработало».
	RequestIDHeader = "X-Request-Id"
)

// PartnerIdentity достаёт партнёра из контекста запроса. Вызывается только
// после PartnerAuth — до него значения нет, и это ошибка проводки, а не
// ситуация времени выполнения.
func PartnerIdentity(c *fiber.Ctx) service.Identity {
	id, _ := c.Locals(PartnerIdentityLocalsKey).(service.Identity)
	return id
}

// partnerAuthenticator — часть PartnerService, нужная middleware.
type partnerAuthenticator interface {
	Authenticate(ctx context.Context, raw string) (service.Identity, error)
	TouchKey(ctx context.Context, keyID uuid.UUID)
}

// PartnerAuth читает Authorization: Bearer. Заголовок, а не cookie: у
// интеграции нет браузера, и cookie ей взять неоткуда.
func PartnerAuth(svc partnerAuthenticator) fiber.Handler {
	// last_used_at обновляется не чаще раза в минуту на ключ: это отметка
	// «ключом пользуются», а не счётчик запросов, и писать её на каждый вызов
	// значит добавить UPDATE в каждый запрос API.
	var mu sync.Mutex
	touched := map[uuid.UUID]time.Time{}

	return func(c *fiber.Ctx) error {
		raw := strings.TrimSpace(c.Get(fiber.HeaderAuthorization))
		if raw == "" {
			return apperr.PartnerKeyMissing()
		}
		if !strings.HasPrefix(strings.ToLower(raw), "bearer ") {
			return apperr.PartnerKeyInvalid().
				WithHint("Формат заголовка: Authorization: Bearer hab_live_…")
		}
		identity, err := svc.Authenticate(c.Context(), raw[len("bearer "):])
		if err != nil {
			return err
		}
		// Адрес проверяется ПОСЛЕ секрета: иначе по разнице ответов с чужого
		// адреса выясняется, существует ли ключ вообще.
		if !service.IPAllowed(identity.Key.AllowedIPs, c.IP()) {
			log.Warn().Str("prefix", identity.Key.Prefix).Str("ip", c.IP()).
				Str("partner", identity.Partner.Slug).
				Msg("partner key used from an address outside its allowlist")
			return apperr.PartnerIPNotAllowed()
		}
		c.Locals(PartnerIdentityLocalsKey, identity)

		now := time.Now()
		mu.Lock()
		stale := now.Sub(touched[identity.Key.ID]) > time.Minute
		if stale {
			touched[identity.Key.ID] = now
		}
		mu.Unlock()
		if stale {
			svc.TouchKey(c.Context(), identity.Key.ID)
		}
		return c.Next()
	}
}

// RequireScope закрывает ручку правом. Проверка на уровне маршрута, а не
// внутри хендлера: так список прав читается прямо в таблице роутов.
func RequireScope(scope string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if !PartnerIdentity(c).HasScope(scope) {
			return apperr.PartnerScopeRequired(scope)
		}
		return c.Next()
	}
}

// --- лимиты ---

// QuotaLimiter — скользящее окно по произвольному ключу с лимитом, который
// задаётся на каждый вызов. Отличие от RateLimiter (LLM-ручки B2C) в том, что
// у партнёров лимит индивидуальный и приходит из их же строки в базе.
type QuotaLimiter struct {
	mu     sync.Mutex
	window time.Duration
	now    func() time.Time
	hits   map[uuid.UUID][]time.Time
	calls  int
}

func NewQuotaLimiter(window time.Duration) *QuotaLimiter {
	return &QuotaLimiter{window: window, now: time.Now, hits: map[uuid.UUID][]time.Time{}}
}

// Quota — состояние окна после попытки. Remaining и Reset уезжают в заголовки
// ответа: клиент должен уметь притормозить сам, не доводя до 429.
type Quota struct {
	Allowed   bool
	Limit     int
	Remaining int
	Reset     time.Duration
}

func (q *QuotaLimiter) Allow(key uuid.UUID, limit int) Quota {
	q.mu.Lock()
	defer q.mu.Unlock()

	now := q.now()
	cutoff := now.Add(-q.window)
	kept := q.hits[key][:0]
	for _, t := range q.hits[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}

	if limit <= 0 || len(kept) >= limit {
		q.store(key, kept)
		reset := q.window
		if len(kept) > 0 {
			reset = kept[0].Add(q.window).Sub(now)
		}
		return Quota{Allowed: false, Limit: limit, Remaining: 0, Reset: reset}
	}

	kept = append(kept, now)
	q.store(key, kept)
	q.calls++
	if q.calls >= 1000 {
		q.calls = 0
		q.sweepLocked(now)
	}
	return Quota{Allowed: true, Limit: limit, Remaining: limit - len(kept),
		Reset: kept[0].Add(q.window).Sub(now)}
}

// store удаляет опустевшее окно целиком: иначе карта растёт по числу
// когда-либо заходивших ключей — та же течь, что закрыта в RateLimiter.
func (q *QuotaLimiter) store(key uuid.UUID, window []time.Time) {
	if len(window) == 0 {
		delete(q.hits, key)
		return
	}
	q.hits[key] = window
}

func (q *QuotaLimiter) sweepLocked(now time.Time) {
	cutoff := now.Add(-q.window)
	for key, window := range q.hits {
		if len(window) == 0 || !window[len(window)-1].After(cutoff) {
			delete(q.hits, key)
		}
	}
}

// PartnerQuotas — оба потолка партнёра: общий на минуту и отдельный на
// ручки, которые зовут LLM. Один общий лимит здесь не годится: тысяча
// дешёвых чтений в час — норма, а тысяча вызовов модели — счёт за месяц.
type PartnerQuotas struct {
	Requests   *QuotaLimiter
	LLM        *QuotaLimiter
	DefaultRPM int
	DefaultLLM int
}

func NewPartnerQuotas(defaultRPM, defaultLLMPerHour int) *PartnerQuotas {
	return &PartnerQuotas{
		Requests:   NewQuotaLimiter(time.Minute),
		LLM:        NewQuotaLimiter(time.Hour),
		DefaultRPM: defaultRPM,
		DefaultLLM: defaultLLMPerHour,
	}
}

func limitOr(configured *int, fallback int) int {
	if configured != nil && *configured > 0 {
		return *configured
	}
	return fallback
}

// PartnerRateLimit — общий потолок запросов. Заголовки RateLimit-* ставятся
// на КАЖДЫЙ ответ, а не только на отказ: иначе клиент узнаёт о лимите ровно в
// тот момент, когда в него уже упёрся.
func (q *PartnerQuotas) PartnerRateLimit() fiber.Handler {
	return func(c *fiber.Ctx) error {
		partner := PartnerIdentity(c).Partner
		quota := q.Requests.Allow(partner.ID, limitOr(partner.RateLimitPerMin, q.DefaultRPM))
		writeQuotaHeaders(c, quota)
		if !quota.Allowed {
			observability.Default.IncRateLimited()
			c.Set(fiber.HeaderRetryAfter, strconv.Itoa(seconds(quota.Reset)))
			return apperr.PartnerQuotaExceeded(
				"Превышен лимит запросов (" + strconv.Itoa(quota.Limit) +
					" в минуту). Повторите через " + strconv.Itoa(seconds(quota.Reset)) + " с")
		}
		return c.Next()
	}
}

// PartnerLLMLimit — отдельный часовой потолок для ручек, которые зовут
// модель. Ставится ПОСЛЕ общего: сначала грубый фильтр, потом дорогой.
func (q *PartnerQuotas) PartnerLLMLimit() fiber.Handler {
	return func(c *fiber.Ctx) error {
		partner := PartnerIdentity(c).Partner
		quota := q.LLM.Allow(partner.ID, limitOr(partner.LLMPerHour, q.DefaultLLM))
		c.Set("RateLimit-Policy-LLM", strconv.Itoa(quota.Limit)+";w=3600")
		if !quota.Allowed {
			observability.Default.IncRateLimited()
			c.Set(fiber.HeaderRetryAfter, strconv.Itoa(seconds(quota.Reset)))
			minutes := int(math.Ceil(quota.Reset.Minutes()))
			if minutes < 1 {
				minutes = 1
			}
			return apperr.PartnerQuotaExceeded(
				"Превышен лимит запросов к ИИ (" + strconv.Itoa(quota.Limit) +
					" в час). Повторите через " + strconv.Itoa(minutes) + " мин").
				WithHint("Ручки поиска, досье и вопросов считаются отдельной, более " +
					"скупой квотой: за каждой стоит вызов модели")
		}
		return c.Next()
	}
}

func writeQuotaHeaders(c *fiber.Ctx, q Quota) {
	limit := strconv.Itoa(q.Limit)
	remaining := strconv.Itoa(q.Remaining)
	reset := strconv.Itoa(seconds(q.Reset))
	// Два набора заголовков: RateLimit-* из черновика IETF и X-RateLimit-*,
	// который понимают почти все готовые клиенты. Дублирование дешевле, чем
	// объяснять каждому партнёру, почему его SDK лимита не видит.
	c.Set("RateLimit-Limit", limit)
	c.Set("RateLimit-Remaining", remaining)
	c.Set("RateLimit-Reset", reset)
	c.Set("X-RateLimit-Limit", limit)
	c.Set("X-RateLimit-Remaining", remaining)
	c.Set("X-RateLimit-Reset", reset)
}

func seconds(d time.Duration) int {
	s := int(math.Ceil(d.Seconds()))
	if s < 1 {
		return 1
	}
	return s
}

// --- идемпотентность ---

// IdempotencyStore — часть PartnerRepo.
type IdempotencyStore interface {
	GetIdempotent(ctx context.Context, partnerID uuid.UUID, key, requestHash string) (domain.IdempotencyRecord, error)
	SaveIdempotent(ctx context.Context, rec domain.IdempotencyRecord) error
}

// Idempotency — повтор POST после разрыва сети обязан вернуть тот же ответ, а
// не создать второй объект. Ключ необязателен: без него ручка работает как
// раньше — навязывать его значит ломать простые интеграции.
func Idempotency(store IdempotencyStore) fiber.Handler {
	return func(c *fiber.Ctx) error {
		key := strings.TrimSpace(c.Get("Idempotency-Key"))
		if key == "" || c.Method() != fiber.MethodPost {
			return c.Next()
		}
		if len(key) > 255 {
			return apperr.Validation("Idempotency-Key длиннее 255 символов").
				WithParam("Idempotency-Key")
		}
		partnerID := PartnerIdentity(c).Partner.ID
		endpoint := c.Method() + " " + c.Route().Path
		hash := requestFingerprint(endpoint, c.Body())

		rec, err := store.GetIdempotent(c.Context(), partnerID, key, hash)
		switch {
		case err == nil:
			// Повтор: отдаём ровно то, что ответили в первый раз. Заголовок
			// нужен клиенту, чтобы отличить свой повтор от новой операции.
			c.Set("Idempotent-Replay", "true")
			c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
			return c.Status(rec.StatusCode).Send(rec.Response)
		case errors.Is(err, repository.ErrIdempotencyMismatch):
			return apperr.IdempotencyKeyReuse()
		case !errors.Is(err, repository.ErrNotFound):
			return err
		}

		if err := c.Next(); err != nil {
			return err
		}
		status := c.Response().StatusCode()
		// Сохраняем только успех: отказ 4xx/5xx повторять можно и нужно —
		// зафиксировать его навсегда значит запереть клиента в ошибке.
		if status < 200 || status >= 300 {
			return nil
		}
		body := append([]byte(nil), c.Response().Body()...)
		if !json.Valid(body) {
			return nil
		}
		if err := store.SaveIdempotent(c.Context(), domain.IdempotencyRecord{
			PartnerID: partnerID, Key: key, Endpoint: endpoint, RequestHash: hash,
			StatusCode: status, Response: body,
		}); err != nil {
			// Не роняем ответ: операция уже выполнена, и отдать клиенту 500
			// после успеха хуже, чем не запомнить ключ.
			log.Error().Err(err).Str("endpoint", endpoint).Msg("idempotency save failed")
		}
		return nil
	}
}

func requestFingerprint(endpoint string, body []byte) string {
	h := sha256.New()
	h.Write([]byte(endpoint))
	h.Write([]byte{0})
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// --- конверт ошибки ---

// errorType — класс ошибки, выведенный из статуса. Клиенту он нужен для
// ветвления «повторять / чинить запрос / звать нас», и делать его отдельным
// полем в каждом конструкторе ошибки значит повторять статус дважды.
// routerErrorCode переводит статус отказа роутера в машинный код конверта.
func routerErrorCode(status int) string {
	switch status {
	case http.StatusNotFound:
		return "unknown_endpoint"
	case http.StatusMethodNotAllowed:
		return "method_not_allowed"
	case http.StatusRequestEntityTooLarge:
		return "request_too_large"
	case http.StatusUnsupportedMediaType:
		return "unsupported_media_type"
	}
	if status >= 500 {
		return "internal_error"
	}
	return "invalid_request"
}

func errorType(status int) string {
	switch {
	case status == http.StatusUnauthorized:
		return "authentication_error"
	case status == http.StatusForbidden:
		return "permission_error"
	case status == http.StatusTooManyRequests:
		return "rate_limit_error"
	case status == http.StatusConflict:
		return "conflict_error"
	case status >= 500:
		return "api_error"
	case status >= 400:
		return "invalid_request_error"
	}
	return "api_error"
}

// PartnerErrors — конверт ошибки Partner API. Отдельный от B2C намеренно:
// у интеграции другой читатель, и ему нужны type, param, doc_url и
// request_id, которых фронту не требуется.
//
// Перехватывает ошибку на уровне группы, а не в app.ErrorHandler: там уже не
// видно, партнёрский это маршрут или внутренний.
func PartnerErrors(docsURL string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		err := c.Next()
		if err == nil {
			return nil
		}
		var ae *apperr.Error
		if !errors.As(err, &ae) {
			var fe *fiber.Error
			if errors.As(err, &fe) {
				// Отказ самого роутера — чаще всего 404 на несуществующий путь
				// или 405 на неверный метод. Код "internal_error" здесь врал бы:
				// на нашей стороне ничего не сломалось, ошибся клиент.
				ae = apperr.New(fe.Code, routerErrorCode(fe.Code), fe.Message)
			} else {
				log.Error().Err(err).Str("path", c.Path()).Msg("unhandled partner api error")
				ae = apperr.Internal("внутренняя ошибка сервиса")
			}
		}
		observability.Default.IncHTTPRequest(c.Route().Path, strconv.Itoa(ae.Status))

		body := fiber.Map{
			"type":       errorType(ae.Status),
			"code":       ae.Code,
			"message":    ae.Message,
			"request_id": c.Get(RequestIDHeader, c.GetRespHeader(RequestIDHeader)),
			"doc_url":    docsURL + "#errors",
		}
		// Пустые поля в конверт не попадают: «причина известна и она пустая» —
		// неправда, причины просто нет.
		if ae.Param != "" {
			body["param"] = ae.Param
		}
		if ae.Cause != "" {
			body["cause"] = ae.Cause
		}
		if ae.Hint != "" {
			body["hint"] = ae.Hint
		}
		return c.Status(ae.Status).JSON(fiber.Map{"error": body})
	}
}
