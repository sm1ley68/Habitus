package domain

import (
	"time"

	"github.com/google/uuid"
)

// Partner — B2B-клиент Partner API. UserID указывает на служебную строку в
// users: за ней уже стоят owner_listings и leads, поэтому весь код кабинета
// продавца работает для партнёра без изменений.
//
// RateLimitPerMin/LLMPerHour — указатели: nil означает «лимит партнёру не
// назначали, действует общий из конфига», и это не то же самое, что ноль.
type Partner struct {
	ID              uuid.UUID
	Slug            string
	Name            string
	UserID          uuid.UUID
	Status          string
	RateLimitPerMin *int
	LLMPerHour      *int
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (p Partner) Active() bool { return p.Status == PartnerStatusActive }

const (
	PartnerStatusActive    = "active"
	PartnerStatusSuspended = "suspended"
)

// APIKey — ключ доступа. Секрет в базе не лежит: только SHA-256 и открытый
// Prefix, по которому ключ находят и который показывают в интерфейсе.
type APIKey struct {
	ID          uuid.UUID
	PartnerID   uuid.UUID
	Name        string
	Prefix      string
	SecretHash  string
	Environment string
	Scopes      []string
	RevokedAt   *time.Time
	ExpiresAt   *time.Time
	LastUsedAt  *time.Time
	CreatedAt   time.Time
}

// PartnerSearch — сохранённый поиск партнёра: разбор запроса, объяснение и
// контекст, на который опирается досье объекта.
type PartnerSearch struct {
	ID            uuid.UUID
	PartnerID     uuid.UUID
	Query         string
	City          string
	ParsedQuery   map[string]any
	Relaxed       []string
	Degraded      []string
	Notes         []string
	DataFreshness string
	AreaLabel     string
	AreaGeoJSON   any
	Explanation   string
	Total         int
	CreatedAt     time.Time
}

// PartnerSearchResult — снимок одного объекта в выдаче партнёрского поиска.
// Повторяет ChatSearchResult: те же поля, тот же ленивый кэш досье.
type PartnerSearchResult struct {
	SearchID         uuid.UUID
	ExternalID       string
	Rank             int
	Price            *int64
	Area             *float64
	Rooms            *int
	AddressFacts     map[string]any
	Score            float64
	MatchScore       int
	Dossier          map[string]any
	DossierVersion   string
	DossierUpdatedAt *time.Time
}

// PartnerWebhook — подписка партнёра на события. Secret хранится открыто:
// подпись считаем мы, а партнёру секрет нужно уметь показать повторно.
type PartnerWebhook struct {
	ID        uuid.UUID
	PartnerID uuid.UUID
	URL       string
	Secret    string
	Events    []string
	Active    bool
	CreatedAt time.Time
}

// WebhookDelivery — одна попытка доставки в очереди.
type WebhookDelivery struct {
	ID            uuid.UUID
	WebhookID     uuid.UUID
	Event         string
	Payload       map[string]any
	Status        string
	Attempts      int
	LastError     string
	NextAttemptAt time.Time
	DeliveredAt   *time.Time
	CreatedAt     time.Time
	// URL/Secret подтягиваются джойном при выборке очереди: диспетчеру нужен
	// адрес и секрет, а второй запрос на строку доставки — лишний.
	URL    string
	Secret string
}

// IdempotencyRecord — сохранённый ответ на POST с Idempotency-Key.
type IdempotencyRecord struct {
	PartnerID   uuid.UUID
	Key         string
	Endpoint    string
	RequestHash string
	StatusCode  int
	Response    []byte
	CreatedAt   time.Time
}
