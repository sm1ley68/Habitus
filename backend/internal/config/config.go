// Package config reads process env into a Settings struct. No external deps,
// no config file — matches the rest of the stack's env-driven convention
// (see habitus/config.py on the Python side).
package config

import (
	"os"
	"strconv"
	"strings"
)

type Settings struct {
	DBDSN               string
	DBMaxConns          int
	MigrationsPath      string
	HTTPPort            string
	MLServiceURL        string
	MLSearchTimeoutS    int
	MLExplainTimeoutS   int
	MLWarmupTimeoutS    int
	MLDossierTimeoutS   int
	MLObjectAskTimeoutS int
	SessionCookieSecure bool
	SessionSweepMinutes int
	CORSAllowedOrigin   string
	StaticDir           string
	BodyLimitBytes      int
	// DossierTTLHours — срок жизни кэша chat_search_results.dossier (Task 7):
	// старше этого числа часов кэш считается протухшим и досье перезапрашивается
	// у ML, как при отсутствии кэша.
	DossierTTLHours int
	// RateLimitLLMPerHour — Task 8: сколько раз за скользящий час один
	// пользователь может дёрнуть LLM-ручки (messages/stream, ask/stream).
	RateLimitLLMPerHour int
	// MLOwnerTimeoutS — публикация объявления продавца: ML считает эмбеддинг
	// BGE-M3, на холодной модели это заметно дольше остальных ручек.
	MLOwnerTimeoutS int
	// CianFetchPerMin — общий потолок исходящих запросов к Циану. Бан прилетает
	// по IP всему сервису сразу, поэтому лимит суммарный, а не на пользователя.
	CianFetchPerMin int
	// OwnerImportPerHour — сколько импортов в час доступно одному продавцу.
	OwnerImportPerHour int
	// OwnerAutopublish — публиковать импортированное объявление сразу.
	// Рубильник на случай наплыва чужих ссылок: false оставляет всё в draft.
	OwnerAutopublish   bool
	OwnerPhotoMaxMB    int
	OwnerPhotoMaxCount int
	// CianProxies — пул прокси для импорта; та же переменная, что у батч-парсера.
	CianProxies []string
	CianRegion  int
	// GuestRetentionDays — через сколько дней брошенный гость (без живой
	// сессии) вычищается вместе со своими чатами.
	GuestRetentionDays int
	// GuestSweepMinutes — как часто крутить чистку гостей.
	GuestSweepMinutes int
	// RateLimitLLMGuestPerHour — отдельный, более скупой потолок LLM-ручек для
	// гостя: гостевой аккаунт заводится в один запрос, поэтому общий лимит по
	// user_id его не сдерживает.
	RateLimitLLMGuestPerHour int

	// --- Partner API (B2B) ---
	// PartnerAPIEnabled — рубильник всего B2B-контура. Выключенный контур не
	// регистрирует ни одного маршрута: /partner/v1 отвечает 404, а не «ключ
	// не подошёл», и снаружи не видно даже, что он существует.
	PartnerAPIEnabled bool
	// PartnerRateLimitPerMin — общий потолок запросов партнёра, когда в его
	// строке в базе не задан индивидуальный.
	PartnerRateLimitPerMin int
	// PartnerLLMPerHour — отдельный, более скупой потолок на ручки, за
	// каждой из которых стоит вызов модели.
	PartnerLLMPerHour int
	// PartnerIdempotencyTTLHours — сколько помнить ответ на Idempotency-Key.
	PartnerIdempotencyTTLHours int
	// PartnerSearchTTLDays — сколько живёт сохранённый поиск партнёра вместе
	// с выдачей. Это рабочий контекст интеграции, а не архив.
	PartnerSearchTTLDays int
	// PartnerSweepMinutes — как часто чистить протухшие поиски и ключи
	// идемпотентности.
	PartnerSweepMinutes    int
	PartnerWebhookTimeoutS int
	PartnerWebhookPollSec  int
	PartnerWebhookBatch    int
	// PartnerWebhookAllowInsecure разрешает http:// и локальные адреса
	// подписок. Только для разработки: в бою это дыра SSRF.
	PartnerWebhookAllowInsecure bool
	// PublicBaseURL — по нему собирается doc_url в конверте ошибки и адрес
	// страницы документации.
	PublicBaseURL string
	// PartnerKeyPepper — секрет, которым перчится хеш ключа API. В базе его
	// нет: без него дамп базы не даёт ни рабочих ключей (нельзя вписать свой),
	// ни возможности подобрать существующие offline. Пустое значение при
	// включённом контуре — отказ на старте, а не тихая деградация до голого
	// SHA-256.
	PartnerKeyPepper string
	// PartnerAdminTokenHash — SHA-256 токена, которым администратор
	// подтверждает право выдавать доступ. Хранится ХЕШЕМ: чтение конфига
	// сервера не должно давать сам токен.
	PartnerAdminTokenHash string
	// TrustedProxies / ProxyHeader — откуда брать адрес клиента. Без них
	// allowlist ключа за балансировщиком проверял бы адрес балансировщика,
	// то есть не проверял бы ничего.
	TrustedProxies []string
	ProxyHeader    string
}

func Load() Settings {
	return Settings{
		DBDSN:               getenv("DB_DSN", "postgresql://habitus:habitus@localhost:5544/habitus"),
		DBMaxConns:          getenvInt("DB_MAX_CONNS", 20),
		MigrationsPath:      getenv("MIGRATIONS_PATH", "migrations"),
		HTTPPort:            getenv("HTTP_PORT", "8080"),
		MLServiceURL:        getenv("ML_SERVICE_URL", "http://localhost:8000"),
		MLSearchTimeoutS:    getenvInt("ML_SEARCH_TIMEOUT_S", 60),
		MLExplainTimeoutS:   getenvInt("ML_EXPLAIN_TIMEOUT_S", 60),
		MLWarmupTimeoutS:    getenvInt("ML_WARMUP_TIMEOUT_S", 600),
		MLDossierTimeoutS:   getenvInt("ML_DOSSIER_TIMEOUT_S", 30),
		MLObjectAskTimeoutS: getenvInt("ML_OBJECT_ASK_TIMEOUT_S", 45),
		SessionCookieSecure: getenvBool("SESSION_COOKIE_SECURE", false),
		SessionSweepMinutes: getenvInt("SESSION_SWEEP_MINUTES", 360),
		CORSAllowedOrigin:   getenv("CORS_ALLOWED_ORIGIN", "http://localhost:3000"),
		StaticDir:           getenv("STATIC_DIR", "static"),
		BodyLimitBytes:      getenvInt("BODY_LIMIT_BYTES", 1<<20),
		DossierTTLHours:     getenvInt("DOSSIER_TTL_HOURS", 24),
		RateLimitLLMPerHour: getenvInt("RATE_LIMIT_LLM_PER_HOUR", 30),
		MLOwnerTimeoutS:     getenvInt("ML_OWNER_TIMEOUT_S", 60),
		CianFetchPerMin:     getenvInt("CIAN_FETCH_PER_MIN", 6),
		OwnerImportPerHour:  getenvInt("OWNER_IMPORT_PER_HOUR", 20),
		OwnerAutopublish:    getenvBool("OWNER_AUTOPUBLISH", true),
		OwnerPhotoMaxMB:     getenvInt("OWNER_PHOTO_MAX_MB", 10),
		OwnerPhotoMaxCount:  getenvInt("OWNER_PHOTO_MAX_COUNT", 20),
		CianProxies:         getenvList("CIAN_PROXIES"),
		CianRegion:          getenvInt("CIAN_REGION", 1),

		GuestRetentionDays:       getenvInt("GUEST_RETENTION_DAYS", 30),
		GuestSweepMinutes:        getenvInt("GUEST_SWEEP_MINUTES", 720),
		RateLimitLLMGuestPerHour: getenvInt("RATE_LIMIT_LLM_GUEST_PER_HOUR", 5),

		PartnerAPIEnabled:          getenvBool("PARTNER_API_ENABLED", true),
		PartnerRateLimitPerMin:     getenvInt("PARTNER_RATE_LIMIT_PER_MIN", 120),
		PartnerLLMPerHour:          getenvInt("PARTNER_LLM_PER_HOUR", 60),
		PartnerIdempotencyTTLHours: getenvInt("PARTNER_IDEMPOTENCY_TTL_HOURS", 24),
		PartnerSearchTTLDays:       getenvInt("PARTNER_SEARCH_TTL_DAYS", 30),
		PartnerSweepMinutes:        getenvInt("PARTNER_SWEEP_MINUTES", 360),

		PartnerWebhookTimeoutS:      getenvInt("PARTNER_WEBHOOK_TIMEOUT_S", 10),
		PartnerWebhookPollSec:       getenvInt("PARTNER_WEBHOOK_POLL_S", 15),
		PartnerWebhookBatch:         getenvInt("PARTNER_WEBHOOK_BATCH", 20),
		PartnerWebhookAllowInsecure: getenvBool("PARTNER_WEBHOOK_ALLOW_INSECURE", false),

		PublicBaseURL:         getenv("PUBLIC_BASE_URL", "http://localhost:8080"),
		PartnerKeyPepper:      os.Getenv("PARTNER_KEY_PEPPER"),
		PartnerAdminTokenHash: os.Getenv("PARTNER_ADMIN_TOKEN_HASH"),
		TrustedProxies:        getenvList("TRUSTED_PROXIES"),
		ProxyHeader:           getenv("PROXY_HEADER", ""),
	}
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getenvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func getenvBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

// getenvList читает список, разделённый запятыми или переводами строк, —
// тот же формат, что понимает батч-парсер (cmd/cian-parser/main.go).
func getenvList(key string) []string {
	raw := os.Getenv(key)
	if raw == "" {
		return nil
	}
	var out []string
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == ' '
	}) {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
