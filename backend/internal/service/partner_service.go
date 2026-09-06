// partner_service.go — Partner API: аутентификация по ключу и выдача ключей.
//
// Ключ — единственное, что стоит между интеграцией партнёра и боевыми
// данными, поэтому здесь ровно те же правила, что у сессий B2C: наружу уходит
// секрет, в базе лежит только его SHA-256, сравнение — постоянное по времени.
package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/domain"
	"habitus-backend/internal/repository"
)

// Скоупы. Ключ получает пересечение того, что партнёру нужно, и того, за что
// он платит: интегратору-агрегатору не нужен доступ к чужим заявкам, а CRM —
// к гео-слоям.
const (
	ScopeSearchRead    = "search:read"
	ScopeGeoRead       = "geo:read"
	ScopeListingsRead  = "listings:read"
	ScopeListingsWrite = "listings:write"
	ScopeLeadsRead     = "leads:read"
	ScopeWebhooksWrite = "webhooks:write"
)

// AllScopes — закрытый список. Ключ с незнакомым скоупом выдать нельзя:
// опечатка в скоупе иначе тихо превращается в «прав нет», и разбираться с
// этим партнёр будет по 403 в продакшене.
var AllScopes = []string{
	ScopeSearchRead, ScopeGeoRead, ScopeListingsRead, ScopeListingsWrite,
	ScopeLeadsRead, ScopeWebhooksWrite,
}

// keyPrefixLen — длина случайной части открытого префикса. Восьми символов
// base32 (40 бит) хватает, чтобы префиксы не сталкивались, и мало, чтобы по
// нему нельзя было подобрать секрет.
const (
	keyPrefixLen = 8
	keySecretLen = 32
	// DefaultKeyTTL — срок жизни ключа по умолчанию. Бессрочный ключ живёт
	// ровно до того дня, когда кто-то заметит утечку, — а замечают её обычно
	// по счёту за трафик. Полгода — компромисс: достаточно редко, чтобы не
	// раздражать партнёра, и достаточно часто, чтобы забытый ключ умер сам.
	DefaultKeyTTL = 180 * 24 * time.Hour
)

var keyAlphabet = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// PartnerStore — часть PartnerRepo, нужная сервису. Обособленный интерфейс —
// тот же приём, что у chatSearchStore: проверить аутентификацию без базы.
type PartnerStore interface {
	Create(ctx context.Context, p domain.Partner) (domain.Partner, error)
	LogAdminAction(ctx context.Context, a domain.AdminAction) error
	ListAdminLog(ctx context.Context, slug string, limit int) ([]domain.AdminAction, error)
	GetBySlug(ctx context.Context, slug string) (domain.Partner, error)
	List(ctx context.Context) ([]domain.Partner, error)
	SetStatus(ctx context.Context, id uuid.UUID, status string) error
	CreateKey(ctx context.Context, k domain.APIKey) (domain.APIKey, error)
	GetKeyWithPartner(ctx context.Context, prefix string) (domain.APIKey, domain.Partner, error)
	ListKeys(ctx context.Context, partnerID uuid.UUID) ([]domain.APIKey, error)
	RevokeKey(ctx context.Context, prefix string) error
	TouchKey(ctx context.Context, id uuid.UUID, at time.Time) error
}

// partnerUserStore — часть UserRepo: служебный аккаунт партнёра.
type partnerUserStore interface {
	Create(ctx context.Context, email, passwordHash, name string) (domain.User, error)
	GetByEmail(ctx context.Context, email string) (domain.User, error)
}

type PartnerService struct {
	store PartnerStore
	users partnerUserStore
	// pepper — секрет из конфига, которым перчится хеш ключа. В базе его нет
	// и быть не должно: без него дамп базы не даёт ни рабочих ключей (нельзя
	// вписать свой), ни возможности подобрать существующие offline.
	pepper []byte
	now    func() time.Time
}

func NewPartnerService(store PartnerStore, users partnerUserStore, pepper string) *PartnerService {
	return &PartnerService{store: store, users: users, pepper: []byte(pepper), now: time.Now}
}

// Identity — кто пришёл. Один объект вместо пары (партнёр, ключ) потому, что
// дальше по цепочке нужны оба и всегда вместе.
type Identity struct {
	Partner domain.Partner
	Key     domain.APIKey
}

// HasScope — есть ли у ключа право. Скоупы проверяются по ключу, а не по
// партнёру: у одного партнёра бывает ключ для CRM и ключ для витрины, и
// правами они отличаются.
func (i Identity) HasScope(scope string) bool {
	for _, s := range i.Key.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// newAPIKey генерирует ключ. Возвращает открытую часть, полный секрет (его
// видно ровно один раз) и хеш для хранения.
func (s *PartnerService) newAPIKey(environment string) (prefix, full, hash string, err error) {
	prefixPart, err := randomToken(keyPrefixLen)
	if err != nil {
		return "", "", "", err
	}
	secret, err := randomToken(keySecretLen)
	if err != nil {
		return "", "", "", err
	}
	prefix = "hab_" + environment + "_" + prefixPart
	return prefix, prefix + "_" + secret, s.hashSecret(secret), nil
}

func randomToken(n int) (string, error) {
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return keyAlphabet.EncodeToString(raw)[:n], nil
}

// hashSecret — HMAC-SHA256 на перце из конфига, а не голый SHA-256. Разница
// не косметическая: голый хеш позволяет любому, кто дотянулся до базы,
// вписать себе рабочий ключ (посчитал SHA-256 от выдуманного секрета — и
// вставил строку). С перцем посчитать правильный хеш без конфига нельзя.
func (s *PartnerService) hashSecret(secret string) string {
	mac := hmac.New(sha256.New, s.pepper)
	mac.Write([]byte(secret))
	return hex.EncodeToString(mac.Sum(nil))
}

// SplitAPIKey режет ключ на открытый префикс и секрет. Формат разбирается
// структурно — ровно четыре непустые части `hab_<среда>_<id>_<секрет>`, — а
// не «всё после последнего подчёркивания»: последнее пропускало бы обрубки
// вроде `hab_live` дальше, в поход в базу за несуществующим префиксом.
func SplitAPIKey(raw string) (prefix, secret string, ok bool) {
	parts := strings.Split(strings.TrimSpace(raw), "_")
	if len(parts) != 4 || parts[0] != "hab" {
		return "", "", false
	}
	for _, part := range parts {
		if part == "" {
			return "", "", false
		}
	}
	return strings.Join(parts[:3], "_"), parts[3], true
}

// Authenticate проверяет ключ целиком: формат, существование, секрет, отзыв,
// срок и статус партнёра. Все отказы отдают ОДНО сообщение — по разнице в
// ответах иначе перебирается, какие префиксы существуют.
func (s *PartnerService) Authenticate(ctx context.Context, raw string) (Identity, error) {
	prefix, secret, ok := SplitAPIKey(raw)
	if !ok {
		return Identity{}, apperr.PartnerKeyInvalid()
	}
	key, partner, err := s.store.GetKeyWithPartner(ctx, prefix)
	if errors.Is(err, repository.ErrNotFound) {
		return Identity{}, apperr.PartnerKeyInvalid()
	}
	if err != nil {
		return Identity{}, err
	}
	if subtle.ConstantTimeCompare([]byte(s.hashSecret(secret)), []byte(key.SecretHash)) != 1 {
		return Identity{}, apperr.PartnerKeyInvalid()
	}
	if key.RevokedAt != nil {
		return Identity{}, apperr.PartnerKeyRevoked()
	}
	now := s.now()
	if key.ExpiresAt != nil && !key.ExpiresAt.After(now) {
		return Identity{}, apperr.PartnerKeyExpired()
	}
	if !partner.Active() {
		return Identity{}, apperr.PartnerSuspended()
	}
	return Identity{Partner: partner, Key: key}, nil
}

// TouchKey — отметка «ключом только что ходили». Ошибка сюда не поднимается:
// телеметрия ключа не должна ронять запрос, который уже прошёл проверку.
func (s *PartnerService) TouchKey(ctx context.Context, keyID uuid.UUID) {
	_ = s.store.TouchKey(ctx, keyID, s.now())
}

// partnerServiceEmail — адрес служебного аккаунта партнёра. Домен .invalid
// зарезервирован RFC 2606: на него нельзя ни отправить письмо, ни спутать его
// с настоящим почтовым ящиком продавца.
func partnerServiceEmail(slug string) string {
	return "partner+" + slug + "@habitus.invalid"
}

// unusablePasswordHash — не bcrypt: bcrypt.CompareHashAndPassword на такой
// строке всегда возвращает ошибку, и войти под служебным аккаунтом паролем
// невозможно ни при каком вводе.
const unusablePasswordHash = "!partner-api-no-password"

// Provision заводит партнёра вместе со служебным аккаунтом. Идемпотентен по
// slug настолько, насколько это осмысленно: существующий партнёр возвращается
// как есть, а не дублируется.
func (s *PartnerService) Provision(ctx context.Context, slug, name string,
	rateLimitPerMin, llmPerHour *int, operator, reason string) (domain.Partner, error) {
	slug = strings.ToLower(strings.TrimSpace(slug))
	if slug == "" {
		return domain.Partner{}, apperr.Validation("slug партнёра обязателен")
	}
	if existing, err := s.store.GetBySlug(ctx, slug); err == nil {
		return existing, nil
	} else if !errors.Is(err, repository.ErrNotFound) {
		return domain.Partner{}, err
	}

	email := partnerServiceEmail(slug)
	user, err := s.users.GetByEmail(ctx, email)
	if errors.Is(err, repository.ErrNotFound) {
		user, err = s.users.Create(ctx, email, unusablePasswordHash, name)
	}
	if err != nil {
		return domain.Partner{}, err
	}

	// pending, а не active: «завели партнёра» и «пустили его в бой» — разные
	// решения, и второе должен принять человек отдельным действием.
	partner, err := s.store.Create(ctx, domain.Partner{
		Slug: slug, Name: name, UserID: user.ID,
		Status:          domain.PartnerStatusPending,
		RateLimitPerMin: rateLimitPerMin, LLMPerHour: llmPerHour,
	})
	if err != nil {
		return domain.Partner{}, err
	}
	s.log(ctx, partner, domain.AdminActionPartnerCreated, "", operator, reason, nil)
	return partner, nil
}

// Approve открывает партнёру доступ. Отдельное действие, а не флаг при
// заведении: именно здесь кто-то берёт на себя ответственность, и именно эта
// строка журнала отвечает на вопрос «кто их пустил».
func (s *PartnerService) Approve(ctx context.Context, slug, operator, reason string) (domain.Partner, error) {
	partner, err := s.store.GetBySlug(ctx, slug)
	if err != nil {
		return domain.Partner{}, err
	}
	if partner.Status == domain.PartnerStatusActive {
		return partner, nil
	}
	if err := s.store.SetStatus(ctx, partner.ID, domain.PartnerStatusActive); err != nil {
		return domain.Partner{}, err
	}
	partner.Status = domain.PartnerStatusActive
	s.log(ctx, partner, domain.AdminActionPartnerApproved, "", operator, reason, nil)
	return partner, nil
}

// log пишет строку журнала. Отказ записи не отменяет само действие: ключ уже
// выпущен, и молча провалить операцию из-за журнала было бы хуже, — но и
// потерять запись нельзя, поэтому она уходит в лог процесса.
func (s *PartnerService) log(ctx context.Context, p domain.Partner, action, keyPrefix,
	operator, reason string, details map[string]any) {
	id := p.ID
	err := s.store.LogAdminAction(ctx, domain.AdminAction{
		PartnerID: &id, Slug: p.Slug, Action: action, KeyPrefix: keyPrefix,
		Operator: operator, Reason: reason, Details: details,
	})
	if err != nil {
		log.Error().Err(err).Str("action", action).Str("slug", p.Slug).
			Str("operator", operator).Msg("partner admin log write failed")
	}
}

// AdminLog отдаёт журнал выдачи доступа. Пустой slug — по всем партнёрам.
func (s *PartnerService) AdminLog(ctx context.Context, slug string, limit int) ([]domain.AdminAction, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.store.ListAdminLog(ctx, slug, limit)
}

// IssueKeyInput — всё, что нужно для выпуска. Структурой, а не восемью
// аргументами: половина из них строки, и перепутать их местами слишком легко.
type IssueKeyInput struct {
	Partner     domain.Partner
	Name        string
	Environment string
	Scopes      []string
	// AllowedIPs — адреса и подсети, с которых ключ работает. Пустой список
	// означает «откуда угодно».
	AllowedIPs []string
	// TTL == 0 берёт DefaultKeyTTL. Бессрочный ключ выпускается только явным
	// Forever: молчаливая бессрочность — это утечка, которая не истекает.
	TTL      time.Duration
	Forever  bool
	Operator string
	Reason   string
}

// IssueKey выдаёт ключ. Полный секрет возвращается ЕДИНСТВЕННЫЙ раз — в базе
// его нет, и восстановить его потом нельзя ни нам, ни партнёру.
func (s *PartnerService) IssueKey(ctx context.Context, in IssueKeyInput) (domain.APIKey, string, error) {
	if in.Environment != "live" && in.Environment != "test" {
		return domain.APIKey{}, "", apperr.Validation("environment должен быть live или test")
	}
	// Ключ партнёру, которого не одобрили, — это доступ в обход решения.
	if in.Partner.Status != domain.PartnerStatusActive {
		return domain.APIKey{}, "", apperr.Validation(
			"партнёр в состоянии " + in.Partner.Status + " — сначала approve")
	}
	clean, err := NormalizeScopes(in.Scopes)
	if err != nil {
		return domain.APIKey{}, "", err
	}
	nets, err := NormalizeAllowedIPs(in.AllowedIPs)
	if err != nil {
		return domain.APIKey{}, "", err
	}
	prefix, full, hash, err := s.newAPIKey(in.Environment)
	if err != nil {
		return domain.APIKey{}, "", err
	}

	var expiresAt *time.Time
	if !in.Forever {
		ttl := in.TTL
		if ttl <= 0 {
			ttl = DefaultKeyTTL
		}
		t := s.now().Add(ttl)
		expiresAt = &t
	}

	key, err := s.store.CreateKey(ctx, domain.APIKey{
		PartnerID: in.Partner.ID, Name: in.Name, Prefix: prefix, SecretHash: hash,
		Environment: in.Environment, Scopes: clean, AllowedIPs: nets, ExpiresAt: expiresAt,
	})
	if err != nil {
		return domain.APIKey{}, "", err
	}
	s.log(ctx, in.Partner, domain.AdminActionKeyIssued, prefix, in.Operator, in.Reason,
		map[string]any{
			"scopes": clean, "environment": in.Environment,
			"allowed_ips": nets, "forever": in.Forever, "expires_at": expiresAt,
		})
	return key, full, nil
}

// NormalizeAllowedIPs принимает и одиночные адреса, и подсети в CIDR. Кривая
// запись — отказ при выпуске, а не молчаливо пропущенная строка: список, из
// которого тихо выпал адрес, запирает партнёра снаружи, и разбираться с этим
// он будет по 403 в бою.
func NormalizeAllowedIPs(entries []string) ([]string, error) {
	out := make([]string, 0, len(entries))
	for _, raw := range entries {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		if _, _, err := net.ParseCIDR(entry); err == nil {
			out = append(out, entry)
			continue
		}
		ip := net.ParseIP(entry)
		if ip == nil {
			return nil, apperr.Validation("не адрес и не подсеть: " + entry).
				WithParam("allowed_ips")
		}
		// Одиночный адрес приводится к /32 или /128: дальше проверка одна на
		// оба случая, и незачем держать две ветки сравнения.
		bits := 32
		if ip.To4() == nil {
			bits = 128
		}
		out = append(out, ip.String()+"/"+strconv.Itoa(bits))
	}
	return out, nil
}

// IPAllowed — разрешён ли адрес этим ключом. Пустой список означает «откуда
// угодно»: это осознанный выбор при выпуске, а не забытая настройка.
func IPAllowed(allowed []string, remote string) bool {
	if len(allowed) == 0 {
		return true
	}
	ip := net.ParseIP(strings.TrimSpace(remote))
	if ip == nil {
		// Адрес не разобрался — считаем, что не совпал. Пропустить неизвестное
		// значило бы обойти allowlist кривым заголовком прокси.
		return false
	}
	for _, entry := range allowed {
		_, network, err := net.ParseCIDR(entry)
		if err != nil {
			continue
		}
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// NormalizeScopes отсеивает дубли и незнакомые скоупы. Незнакомый — ошибка, а
// не тихий пропуск: иначе опечатка превращается в «прав нет» уже в бою.
func NormalizeScopes(scopes []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(scopes))
	for _, raw := range scopes {
		scope := strings.TrimSpace(raw)
		if scope == "" {
			continue
		}
		known := false
		for _, s := range AllScopes {
			if s == scope {
				known = true
				break
			}
		}
		if !known {
			return nil, apperr.Validation("неизвестный scope: " + scope)
		}
		if !seen[scope] {
			seen[scope] = true
			out = append(out, scope)
		}
	}
	if len(out) == 0 {
		return nil, apperr.Validation("ключу нужен хотя бы один scope")
	}
	return out, nil
}

func (s *PartnerService) GetBySlug(ctx context.Context, slug string) (domain.Partner, error) {
	return s.store.GetBySlug(ctx, slug)
}

func (s *PartnerService) List(ctx context.Context) ([]domain.Partner, error) {
	return s.store.List(ctx)
}

func (s *PartnerService) ListKeys(ctx context.Context, partnerID uuid.UUID) ([]domain.APIKey, error) {
	return s.store.ListKeys(ctx, partnerID)
}

// RevokeKey отзывает ключ. Партнёр нужен только журналу — сам отзыв ищет
// ключ по префиксу.
func (s *PartnerService) RevokeKey(ctx context.Context, p domain.Partner,
	prefix, operator, reason string) error {
	if err := s.store.RevokeKey(ctx, prefix); err != nil {
		return err
	}
	s.log(ctx, p, domain.AdminActionKeyRevoked, prefix, operator, reason, nil)
	return nil
}

// SetStatus приостанавливает или возвращает доступ. Одобрение живёт отдельно
// (Approve): «впервые пустили» и «вернули после паузы» — разные события, и
// журнал должен их различать.
func (s *PartnerService) SetStatus(ctx context.Context, p domain.Partner,
	status, operator, reason string) error {
	if status != domain.PartnerStatusActive && status != domain.PartnerStatusSuspended {
		return apperr.Validation("status должен быть active или suspended")
	}
	if err := s.store.SetStatus(ctx, p.ID, status); err != nil {
		return err
	}
	action := domain.AdminActionPartnerResumed
	if status == domain.PartnerStatusSuspended {
		action = domain.AdminActionPartnerSuspended
	}
	s.log(ctx, p, action, "", operator, reason, nil)
	return nil
}
