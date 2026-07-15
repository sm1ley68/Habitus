// Package config reads process env into a Settings struct. No external deps,
// no config file — matches the rest of the stack's env-driven convention
// (see habitus/config.py on the Python side).
package config

import (
	"os"
	"strconv"
)

type Settings struct {
	DBDSN               string
	MigrationsPath      string
	HTTPPort            string
	MLServiceURL        string
	MLSearchTimeoutS    int
	MLWarmupTimeoutS    int
	SessionCookieSecure bool
	CORSAllowedOrigin   string
	StaticDir           string
}

func Load() Settings {
	return Settings{
		DBDSN:               getenv("DB_DSN", "postgresql://habitus:habitus@localhost:5544/habitus"),
		MigrationsPath:      getenv("MIGRATIONS_PATH", "migrations"),
		HTTPPort:            getenv("HTTP_PORT", "8080"),
		MLServiceURL:        getenv("ML_SERVICE_URL", "http://localhost:8000"),
		MLSearchTimeoutS:    getenvInt("ML_SEARCH_TIMEOUT_S", 60),
		MLWarmupTimeoutS:    getenvInt("ML_WARMUP_TIMEOUT_S", 600),
		SessionCookieSecure: getenvBool("SESSION_COOKIE_SECURE", false),
		CORSAllowedOrigin:   getenv("CORS_ALLOWED_ORIGIN", "http://localhost:3000"),
		StaticDir:           getenv("STATIC_DIR", "static"),
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
