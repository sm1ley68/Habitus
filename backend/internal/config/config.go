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
