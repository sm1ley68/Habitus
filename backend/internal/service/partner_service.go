// partner_service.go — Partner API: аутентификация по ключу и выдача ключей.
//
// Ключ — единственное, что стоит между интеграцией партнёра и боевыми
// данными, поэтому здесь ровно те же правила, что у сессий B2C: наружу уходит
// секрет, в базе лежит только его SHA-256, сравнение — постоянное по времени.
package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

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
)

var keyAlphabet = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// PartnerStore — часть PartnerRepo, нужная сервису. Обособленный интерфейс —
// тот же приём, что у chatSearchStore: проверить аутентификацию без базы.
type PartnerStore interface {
	Create(ctx context.Context, p domain.Partner) (domain.Partner, error)
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
	now   func() time.Time
}

func NewPartnerService(store PartnerStore, users partnerUserStore) *PartnerService {
	return &PartnerService{store: store, users: users, now: time.Now}
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

// NewAPIKey генерирует ключ. Возвращает открытую часть, полный секрет (его
// видно ровно один раз) и хеш для хранения.
func NewAPIKey(environment string) (prefix, full, hash string, err error) {
	prefixPart, err := randomToken(keyPrefixLen)
	if err != nil {
		return "", "", "", err
	}
	secret, err := randomToken(keySecretLen)
	if err != nil {
		return "", "", "", err
	}
	prefix = "hab_" + environment + "_" + prefixPart
	return prefix, prefix + "_" + secret, hashSecret(secret), nil
}

func randomToken(n int) (string, error) {
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return keyAlphabet.EncodeToString(raw)[:n], nil
}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
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
	if subtle.ConstantTimeCompare([]byte(hashSecret(secret)), []byte(key.SecretHash)) != 1 {
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
	rateLimitPerMin, llmPerHour *int) (domain.Partner, error) {
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

	return s.store.Create(ctx, domain.Partner{
		Slug: slug, Name: name, UserID: user.ID,
		RateLimitPerMin: rateLimitPerMin, LLMPerHour: llmPerHour,
	})
}

// IssueKey выдаёт ключ. Полный секрет возвращается ЕДИНСТВЕННЫЙ раз — в базе
// его нет, и восстановить его потом нельзя ни нам, ни партнёру.
func (s *PartnerService) IssueKey(ctx context.Context, partnerID uuid.UUID, name,
	environment string, scopes []string, ttl time.Duration) (domain.APIKey, string, error) {
	if environment != "live" && environment != "test" {
		return domain.APIKey{}, "", apperr.Validation("environment должен быть live или test")
	}
	clean, err := NormalizeScopes(scopes)
	if err != nil {
		return domain.APIKey{}, "", err
	}
	prefix, full, hash, err := NewAPIKey(environment)
	if err != nil {
		return domain.APIKey{}, "", err
	}
	var expiresAt *time.Time
	if ttl > 0 {
		t := s.now().Add(ttl)
		expiresAt = &t
	}
	key, err := s.store.CreateKey(ctx, domain.APIKey{
		PartnerID: partnerID, Name: name, Prefix: prefix, SecretHash: hash,
		Environment: environment, Scopes: clean, ExpiresAt: expiresAt,
	})
	if err != nil {
		return domain.APIKey{}, "", err
	}
	return key, full, nil
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

func (s *PartnerService) RevokeKey(ctx context.Context, prefix string) error {
	return s.store.RevokeKey(ctx, prefix)
}

func (s *PartnerService) SetStatus(ctx context.Context, id uuid.UUID, status string) error {
	if status != domain.PartnerStatusActive && status != domain.PartnerStatusSuspended {
		return apperr.Validation("status должен быть active или suspended")
	}
	return s.store.SetStatus(ctx, id, status)
}
