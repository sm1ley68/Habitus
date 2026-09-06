package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// verifyLikePartner повторяет проверку подписи так, как её описывает
// документация. Если тест разойдётся с ней, разойдётся и интеграция.
func verifyLikePartner(secret, header string, body []byte) bool {
	parts := map[string]string{}
	for _, chunk := range strings.Split(header, ",") {
		k, v, _ := strings.Cut(chunk, "=")
		parts[k] = v
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(parts["t"] + "."))
	mac.Write(body)
	return hmac.Equal([]byte(hex.EncodeToString(mac.Sum(nil))), []byte(parts["v1"]))
}

func TestSignWebhookMatchesDocumentedVerification(t *testing.T) {
	body := []byte(`{"event":"lead.created"}`)
	header := SignWebhook("whsec_test", 1788684862, body)

	if !strings.HasPrefix(header, "t=1788684862,v1=") {
		t.Fatalf("header = %q; ожидался формат t=<unix>,v1=<hex>", header)
	}
	if !verifyLikePartner("whsec_test", header, body) {
		t.Fatal("подпись не проходит проверку по описанной в документации схеме")
	}
}

func TestSignWebhookBindsTimestampAndBody(t *testing.T) {
	body := []byte(`{"event":"lead.created"}`)
	header := SignWebhook("whsec_test", 1788684862, body)

	// Подменённое тело при той же метке — подпись обязана разойтись.
	if verifyLikePartner("whsec_test", header, []byte(`{"event":"lead.deleted"}`)) {
		t.Fatal("подпись не привязана к телу")
	}
	// Та же подпись с другой меткой времени: если бы метка не входила в
	// подписываемую строку, перехваченный запрос можно было бы повторять вечно.
	replayed := strings.Replace(header, "t=1788684862", "t=1788700000", 1)
	if verifyLikePartner("whsec_test", replayed, body) {
		t.Fatal("подпись не привязана к метке времени")
	}
	if verifyLikePartner("whsec_other", header, body) {
		t.Fatal("подпись прошла проверку на чужом секрете")
	}
}

func TestWebhookEnvelopeAlwaysCarriesObjectAndData(t *testing.T) {
	raw, err := json.Marshal(WebhookEnvelope{ID: "d1", Event: EventPing, CreatedAt: "2026-09-06T09:00:00Z"})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if out["object"] != "event" {
		t.Fatalf("object = %v; want event", out["object"])
	}
	// data должен быть объектом, а не null: обработчик партнёра не обязан
	// проверять его на существование ради события без полезной нагрузки.
	if _, ok := out["data"].(map[string]any); !ok {
		t.Fatalf("data = %#v; ожидался пустой объект", out["data"])
	}
}

func TestRetryDelayGrowsAndStopsAtCeiling(t *testing.T) {
	prev := time.Duration(0)
	for attempt := 0; attempt < len(webhookBackoff); attempt++ {
		got := retryDelay(attempt)
		if got <= prev {
			t.Fatalf("retryDelay(%d) = %v; пауза должна расти", attempt, got)
		}
		prev = got
	}
	if retryDelay(99) != webhookBackoff[len(webhookBackoff)-1] {
		t.Fatal("после исчерпания расписания пауза должна упереться в потолок")
	}
}

func TestNormalizeEventsRejectsUnknown(t *testing.T) {
	got, err := normalizeEvents([]string{EventLeadCreated, EventLeadCreated})
	if err != nil || len(got) != 1 {
		t.Fatalf("normalizeEvents() = %v, %v; дубли должны схлопываться", got, err)
	}
	// ping приходит только по явному запросу — подписаться на него нельзя.
	if _, err := normalizeEvents([]string{EventPing}); err == nil {
		t.Fatal("подписка на ping принята")
	}
	if _, err := normalizeEvents([]string{"lead.crated"}); err == nil {
		t.Fatal("опечатка в имени события принята")
	}
}

// Адрес подписки задаёт партнёр. Без этой проверки он превращает шлюз в
// прокси к нашему же контуру: достаточно подписаться на адрес метаданных
// облака и прочитать ответ в журнале доставок.
func TestValidateTargetBlocksInternalAddresses(t *testing.T) {
	svc := NewPartnerWebhookService(nil, nil, time.Second, false)
	blocked := []string{
		"http://crm.example.com/hook",
		"https://localhost/hook",
		"https://127.0.0.1/hook",
		"https://169.254.169.254/latest/meta-data/",
		"https://10.0.0.5/hook",
		"https://backend.internal/hook",
		"не-адрес",
	}
	for _, raw := range blocked {
		if err := svc.validateTarget(raw); err == nil {
			t.Errorf("validateTarget(%q) пропустил внутренний или небезопасный адрес", raw)
		}
	}
	if err := svc.validateTarget("https://crm.example.com/hooks/habitus"); err != nil {
		t.Fatalf("validateTarget() отверг публичный https-адрес: %v", err)
	}
}

func TestValidateTargetAllowsLocalWhenExplicitlyEnabled(t *testing.T) {
	// Рубильник существует ради локальной разработки: без него вебхук нельзя
	// проверить на своей машине вовсе.
	svc := NewPartnerWebhookService(nil, nil, time.Second, true)
	if err := svc.validateTarget("http://localhost:9000/hook"); err != nil {
		t.Fatalf("validateTarget() = %v; при явном разрешении локальный адрес допустим", err)
	}
}
