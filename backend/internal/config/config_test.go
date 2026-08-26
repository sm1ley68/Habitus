package config

import "testing"

func TestExplainTimeoutDefaultsAndOverrides(t *testing.T) {
	// Генерация объяснения — отдельный поток со своим бюджетом: он длиннее
	// поиска и не должен зависеть от ML_SEARCH_TIMEOUT_S.
	if got := Load().MLExplainTimeoutS; got != 60 {
		t.Fatalf("MLExplainTimeoutS = %d; want 60", got)
	}

	t.Setenv("ML_EXPLAIN_TIMEOUT_S", "120")
	if got := Load().MLExplainTimeoutS; got != 120 {
		t.Fatalf("MLExplainTimeoutS = %d; want 120", got)
	}
}

func TestPoolSizeDefaultsAndOverrides(t *testing.T) {
	// Пул на дефолтах pgxpool молча упирается в число ядер машины, а под SSE
	// каждое соединение держится дольше обычного запроса.
	if got := Load().DBMaxConns; got != 20 {
		t.Fatalf("DBMaxConns = %d; want 20", got)
	}

	t.Setenv("DB_MAX_CONNS", "8")
	if got := Load().DBMaxConns; got != 8 {
		t.Fatalf("DBMaxConns = %d; want 8", got)
	}
}

func TestSessionSweepIntervalDefault(t *testing.T) {
	if got := Load().SessionSweepMinutes; got != 360 {
		t.Fatalf("SessionSweepMinutes = %d; want 360", got)
	}

	t.Setenv("SESSION_SWEEP_MINUTES", "15")
	if got := Load().SessionSweepMinutes; got != 15 {
		t.Fatalf("SessionSweepMinutes = %d; want 15", got)
	}
}

func TestBodyLimitDefault(t *testing.T) {
	// Публичный шлюз: тело запроса ограничено явно, а не дефолтом фреймворка.
	if got := Load().BodyLimitBytes; got != 1<<20 {
		t.Fatalf("BodyLimitBytes = %d; want %d", got, 1<<20)
	}
}

func TestDossierTTLHoursDefaultAndOverride(t *testing.T) {
	if got := Load().DossierTTLHours; got != 24 {
		t.Fatalf("DossierTTLHours = %d; want 24", got)
	}

	t.Setenv("DOSSIER_TTL_HOURS", "6")
	if got := Load().DossierTTLHours; got != 6 {
		t.Fatalf("DossierTTLHours = %d; want 6", got)
	}
}

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
