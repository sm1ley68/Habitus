// partner_webhook_service.go — доставка событий в системы партнёров.
//
// Без вебхука единственный способ узнать о заявке — опрашивать /leads, и
// между «покупатель написал» и «менеджер увидел» ложатся минуты опроса. При
// этом «выстрелил и забыл» здесь не годится: CRM партнёра лежит ровно в тот
// момент, когда пришла заявка, поэтому очередь живёт в базе и переживает
// перезапуск шлюза.
package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/domain"
	"habitus-backend/internal/repository"
)

// События. Закрытый список: подписка на несуществующее событие молча никогда
// не сработает, и партнёр будет неделю ждать вебхук, которого не будет.
const (
	EventLeadCreated        = "lead.created"
	EventListingPublished   = "listing.published"
	EventListingUnpublished = "listing.unpublished"
	EventPing               = "ping"
)

var AllWebhookEvents = []string{EventLeadCreated, EventListingPublished, EventListingUnpublished}

// Расписание повторов. Пять попыток за ~9 часов: этого хватает пережить
// ночной деплой CRM и мало, чтобы неделю долбиться в мёртвый адрес.
var webhookBackoff = []time.Duration{
	time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour,
}

const webhookMaxAttempts = 5

type webhookStore interface {
	Create(ctx context.Context, w domain.PartnerWebhook) (domain.PartnerWebhook, error)
	List(ctx context.Context, partnerID uuid.UUID) ([]domain.PartnerWebhook, error)
	GetOwned(ctx context.Context, partnerID, id uuid.UUID) (domain.PartnerWebhook, error)
	Delete(ctx context.Context, partnerID, id uuid.UUID) error
	Subscribers(ctx context.Context, partnerID uuid.UUID, event string) ([]domain.PartnerWebhook, error)
	Enqueue(ctx context.Context, webhookID uuid.UUID, event string, payload map[string]any) (uuid.UUID, error)
	ClaimDue(ctx context.Context, limit int, lease time.Duration) ([]domain.WebhookDelivery, error)
	MarkDelivered(ctx context.Context, id uuid.UUID) error
	MarkFailed(ctx context.Context, id uuid.UUID, reason string, retryIn time.Duration, maxAttempts int) error
	ListDeliveries(ctx context.Context, webhookID uuid.UUID, limit, offset int) ([]domain.WebhookDelivery, int, error)
}

// partnerByUser — часть PartnerRepo: заявка знает продавца, а вебхуки висят
// на партнёре.
type partnerByUser interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) (domain.Partner, error)
}

type PartnerWebhookService struct {
	store    webhookStore
	partners partnerByUser
	client   *http.Client
	// allowInsecureTargets разрешает http:// и локальные адреса. Только для
	// разработки: в бою это дыра SSRF, через которую партнёр заставит шлюз
	// стучаться во внутреннюю сеть.
	allowInsecureTargets bool
}

func NewPartnerWebhookService(store webhookStore, partners partnerByUser,
	timeout time.Duration, allowInsecureTargets bool) *PartnerWebhookService {
	return &PartnerWebhookService{
		store: store, partners: partners,
		client:               &http.Client{Timeout: timeout},
		allowInsecureTargets: allowInsecureTargets,
	}
}

func (s *PartnerWebhookService) List(ctx context.Context, partnerID uuid.UUID) ([]domain.PartnerWebhook, error) {
	return s.store.List(ctx, partnerID)
}

func (s *PartnerWebhookService) Get(ctx context.Context, partnerID, id uuid.UUID) (domain.PartnerWebhook, error) {
	w, err := s.store.GetOwned(ctx, partnerID, id)
	if errors.Is(err, repository.ErrNotFound) {
		return domain.PartnerWebhook{}, apperr.WebhookNotFound()
	}
	return w, err
}

func (s *PartnerWebhookService) Create(ctx context.Context, partnerID uuid.UUID,
	rawURL string, events []string) (domain.PartnerWebhook, error) {
	if err := s.validateTarget(rawURL); err != nil {
		return domain.PartnerWebhook{}, err
	}
	clean, err := normalizeEvents(events)
	if err != nil {
		return domain.PartnerWebhook{}, err
	}
	secret, err := newWebhookSecret()
	if err != nil {
		return domain.PartnerWebhook{}, err
	}
	return s.store.Create(ctx, domain.PartnerWebhook{
		PartnerID: partnerID, URL: rawURL, Secret: secret, Events: clean, Active: true,
	})
}

func (s *PartnerWebhookService) Delete(ctx context.Context, partnerID, id uuid.UUID) error {
	err := s.store.Delete(ctx, partnerID, id)
	if errors.Is(err, repository.ErrNotFound) {
		return apperr.WebhookNotFound()
	}
	return err
}

func (s *PartnerWebhookService) Deliveries(ctx context.Context, partnerID, id uuid.UUID,
	limit, offset int) ([]domain.WebhookDelivery, int, error) {
	if _, err := s.Get(ctx, partnerID, id); err != nil {
		return nil, 0, err
	}
	return s.store.ListDeliveries(ctx, id, limit, offset)
}

// SendTest ставит в очередь событие ping. Отдельная ручка нужна, потому что
// иначе первая проверка подписи у партнёра случается на боевой заявке.
func (s *PartnerWebhookService) SendTest(ctx context.Context, partnerID, id uuid.UUID) (uuid.UUID, error) {
	w, err := s.Get(ctx, partnerID, id)
	if err != nil {
		return uuid.Nil, err
	}
	return s.store.Enqueue(ctx, w.ID, EventPing, map[string]any{
		"message": "Проверочное событие Habitus Partner API",
	})
}

// Emit ставит событие в очередь всем подписчикам партнёра. Ошибка сюда не
// поднимается вызывающему: заявка уже создана, и падать из-за вебхука она не
// должна — доставка это следствие, а не часть операции.
func (s *PartnerWebhookService) Emit(ctx context.Context, partnerID uuid.UUID,
	event string, payload map[string]any) {
	hooks, err := s.store.Subscribers(ctx, partnerID, event)
	if err != nil {
		log.Error().Err(err).Str("event", event).Msg("webhook subscribers lookup failed")
		return
	}
	for _, w := range hooks {
		if _, err := s.store.Enqueue(ctx, w.ID, event, payload); err != nil {
			log.Error().Err(err).Str("event", event).
				Str("webhook_id", w.ID.String()).Msg("webhook enqueue failed")
		}
	}
}

// LeadCreated — реализация LeadNotifier. Продавец, которому пришла заявка,
// может быть обычным пользователем: тогда партнёра за ним нет, и событию
// некуда идти — это нормальный ход, а не ошибка.
func (s *PartnerWebhookService) LeadCreated(ctx context.Context, lead domain.Lead) {
	partner, err := s.partners.GetByUserID(ctx, lead.SellerID)
	if errors.Is(err, repository.ErrNotFound) {
		return
	}
	if err != nil {
		log.Error().Err(err).Msg("webhook partner lookup failed")
		return
	}
	s.Emit(ctx, partner.ID, EventLeadCreated, map[string]any{
		"id":          lead.ID.String(),
		"external_id": lead.ExternalID,
		"address":     lead.Address,
		"name":        lead.Name,
		"contact":     lead.Contact,
		"message":     lead.Message,
		"created_at":  lead.CreatedAt.UTC().Format(time.RFC3339),
	})
}

// StartDispatcher крутит очередь доставок. Отдельная горутина с тикером —
// тот же приём, что у StartSessionSweeper и StartGuestSweeper.
func (s *PartnerWebhookService) StartDispatcher(ctx context.Context, interval time.Duration, batch int) {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	if batch <= 0 {
		batch = 20
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.dispatchOnce(ctx, batch)
			}
		}
	}()
}

func (s *PartnerWebhookService) dispatchOnce(ctx context.Context, batch int) {
	// Аренда чуть больше таймаута запроса: строка не должна вернуться в
	// очередь, пока первая попытка ещё висит в сети.
	lease := s.client.Timeout + 30*time.Second
	due, err := s.store.ClaimDue(ctx, batch, lease)
	if err != nil {
		log.Error().Err(err).Msg("webhook queue read failed")
		return
	}
	for _, delivery := range due {
		s.deliver(ctx, delivery)
	}
}

func (s *PartnerWebhookService) deliver(ctx context.Context, d domain.WebhookDelivery) {
	body, err := json.Marshal(WebhookEnvelope{
		ID:        d.ID.String(),
		Event:     d.Event,
		CreatedAt: d.CreatedAt.UTC().Format(time.RFC3339),
		Data:      d.Payload,
	})
	if err != nil {
		_ = s.store.MarkFailed(ctx, d.ID, "payload marshal: "+err.Error(),
			retryDelay(d.Attempts), webhookMaxAttempts)
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.URL, bytes.NewReader(body))
	if err != nil {
		_ = s.store.MarkFailed(ctx, d.ID, "bad url: "+err.Error(),
			retryDelay(d.Attempts), webhookMaxAttempts)
		return
	}
	ts := time.Now().Unix()
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Habitus-Webhooks/1.0")
	req.Header.Set("X-Habitus-Event", d.Event)
	req.Header.Set("X-Habitus-Delivery", d.ID.String())
	req.Header.Set("X-Habitus-Signature", SignWebhook(d.Secret, ts, body))

	resp, err := s.client.Do(req)
	if err != nil {
		_ = s.store.MarkFailed(ctx, d.ID, err.Error(), retryDelay(d.Attempts), webhookMaxAttempts)
		return
	}
	defer resp.Body.Close()
	// Успех — любой 2xx: партнёр волен ответить 200, 202 или 204, и требовать
	// конкретный код значит ломать интеграцию на пустом месте.
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		_ = s.store.MarkDelivered(ctx, d.ID)
		return
	}
	_ = s.store.MarkFailed(ctx, d.ID, "HTTP "+strconv.Itoa(resp.StatusCode),
		retryDelay(d.Attempts), webhookMaxAttempts)
}

// WebhookEnvelope — форма тела вебхука. Одна и та же для всех событий: у
// интеграции один обработчик, который смотрит на event и разбирает data.
type WebhookEnvelope struct {
	ID        string         `json:"id"`
	Object    string         `json:"object"`
	Event     string         `json:"event"`
	CreatedAt string         `json:"created_at"`
	Data      map[string]any `json:"data"`
}

// MarshalJSON проставляет object: тип ресурса в теле — то же соглашение, что
// и в ответах API, и партнёру не нужно помнить, что вебхук устроен иначе.
func (e WebhookEnvelope) MarshalJSON() ([]byte, error) {
	type alias WebhookEnvelope
	e.Object = "event"
	if e.Data == nil {
		e.Data = map[string]any{}
	}
	return json.Marshal(alias(e))
}

// SignWebhook — подпись в формате `t=<unix>,v1=<hex>`, где подписывается
// строка "<t>.<body>". Метка времени входит в подпись, иначе перехваченный
// запрос можно повторять бесконечно.
func SignWebhook(secret string, ts int64, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d.", ts)
	mac.Write(body)
	return "t=" + strconv.FormatInt(ts, 10) + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

func retryDelay(attempts int) time.Duration {
	if attempts < 0 {
		attempts = 0
	}
	if attempts >= len(webhookBackoff) {
		return webhookBackoff[len(webhookBackoff)-1]
	}
	return webhookBackoff[attempts]
}

func newWebhookSecret() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "whsec_" + base64.RawURLEncoding.EncodeToString(raw), nil
}

func normalizeEvents(events []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(events))
	for _, raw := range events {
		event := strings.TrimSpace(raw)
		if event == "" {
			continue
		}
		known := false
		for _, e := range AllWebhookEvents {
			if e == event {
				known = true
				break
			}
		}
		if !known {
			return nil, apperr.Validation("неизвестное событие: " + event).WithParam("events")
		}
		if !seen[event] {
			seen[event] = true
			out = append(out, event)
		}
	}
	if len(out) == 0 {
		return nil, apperr.Validation("подписке нужно хотя бы одно событие").WithParam("events")
	}
	return out, nil
}

// validateTarget не пускает вебхук во внутреннюю сеть. Партнёр задаёт адрес
// сам, и без этой проверки он превращает шлюз в прокси к нашему же контуру
// (SSRF): достаточно подписаться на http://169.254.169.254/ и прочитать
// метаданные облака в журнале доставок.
func (s *PartnerWebhookService) validateTarget(rawURL string) error {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return apperr.Validation("Некорректный URL вебхука").WithParam("url")
	}
	if s.allowInsecureTargets {
		return nil
	}
	if u.Scheme != "https" {
		return apperr.Validation("URL вебхука должен начинаться с https://").WithParam("url")
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".internal") {
		return apperr.Validation("URL вебхука должен указывать на публичный адрес").WithParam("url")
	}
	if ip := net.ParseIP(host); ip != nil && !isPublicIP(ip) {
		return apperr.Validation("URL вебхука должен указывать на публичный адрес").WithParam("url")
	}
	return nil
}

func isPublicIP(ip net.IP) bool {
	return !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() && !ip.IsUnspecified()
}
