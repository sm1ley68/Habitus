// Package domain holds plain data structs owned by the Go backend. Nothing here
// maps to listings/poi/raw_listings — those are Python-owned and read via
// dedicated read-only repositories (see internal/repository/listing_repo.go).
package domain

import (
	"time"

	"github.com/google/uuid"
)

type User struct {
	ID           uuid.UUID
	Email        string
	PasswordHash string
	Name         string
	// IsGuest — аккаунт без учётных данных, заведённый ради первого поиска.
	// Апгрейд при регистрации сохраняет id, поэтому чаты и результаты гостя
	// остаются при нём.
	IsGuest   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Chat struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	City      string
	Title     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Message struct {
	ID        uuid.UUID
	ChatID    uuid.UUID
	Role      string
	Text      string
	Meta      map[string]any
	CreatedAt time.Time
}

// ChatSearch is one header row per completed search stream — persists what ML
// returned so the object-passport endpoint (and a future Н.1 follow-up) can read
// it back without a second ML call.
type ChatSearch struct {
	ID            uuid.UUID
	ChatID        uuid.UUID
	MessageID     *uuid.UUID
	RawQuery      string
	ParsedQuery   map[string]any
	Relaxed       []string
	DataFreshness string
	Degraded      []string
	// Intent — намерение реплики (Task 4, multi-turn чат). nil, когда ответ ML
	// не содержал intent — колонка остаётся NULL, а не молчаливым умолчанием.
	Intent    *string
	CreatedAt time.Time
}

// ChatSearchResult is the latest-snapshot-per-object row that GET /objects/{id}
// actually reads — upsert-latest-wins by design (see plan §2).
type ChatSearchResult struct {
	ChatID           uuid.UUID
	ExternalID       string
	SearchID         uuid.UUID
	Price            *int64
	Area             *float64
	Rooms            *int
	AddressFacts     map[string]any
	Score            float64
	MatchScore       int
	Explanation      string
	Dossier          map[string]any
	DossierVersion   string
	DossierUpdatedAt *time.Time
	UpdatedAt        time.Time
}

// Listing is a read-only projection of the Python-owned `listings` table —
// only the columns the backend actually needs for display gap-filling.
type Listing struct {
	ExternalID   string
	Price        *int64
	Area         *float64
	Rooms        *int
	Level        *int
	Levels       *int
	Lon          *float64
	Lat          *float64
	Address      *string
	MetroStation *string
	// SourceURL — страница объявления у источника. Нужен паспорту: для
	// витринного объекта продавца в системе нет, и единственный способ
	// связаться — уйти на источник. nil и "" одинаково означают «ссылки нет».
	SourceURL *string
	Photos    []string
	// Факты обогащения — те же величины, что ML кладёт в address_facts.
	// Нужны, чтобы паспорт объекта, открытого с карты (вне подбора), собирал
	// блоки из данных самого объекта, а не из контекста поиска.
	WalkMinSchool  *float64
	WalkMinMetro   *float64
	WalkMinPark    *float64
	BarDensity500m *int
	NoiseLevel     *string
}

// EvidenceFeature — строка urban_evidence с уже сериализованной геометрией.
type EvidenceFeature struct {
	Layer        string
	Source       string
	Weight       *float64
	DB           *float64
	GeometryJSON string
}

// POI is a read-only projection of the Python-owned `poi` table.
type POI struct {
	Kind string
	Name string
	Lon  float64
	Lat  float64
}

// MetroLine — линия рельсового транспорта для отрисовки на карте. System —
// enum, зафиксированный на трёх сторонах: subway / mck / mcd. GeometryJSON
// приходит только для линий, у которых metro_line_geom.geom не NULL —
// репозиторий отфильтровывает остальные, так что здесь всегда валидный
// GeoJSON LineString, а не пустая или нулевая геометрия. Colour — nullable,
// как и в MetroSegment (Задача 14): NULL остаётся nil, а не превращается в
// "" — синтетическое значение вместо отсутствующего замера запрещено.
type MetroLine struct {
	Ref          string
	Name         string
	System       string
	Colour       *string
	GeometryJSON string
}

// Lead — заявка покупателя по объявлению продавца. Name/Contact — то, что
// покупатель сообщил о себе; контакт продавца в обратную сторону не уходит.
// Продавец может снять и удалить объявление — заявка в его истории должна
// остаться читаемой, поэтому ListingID nullable (FK ON DELETE SET NULL), а
// ExternalID/Address хранятся копией на момент отправки, а не джойном.
type Lead struct {
	ID         uuid.UUID
	ListingID  *uuid.UUID
	SellerID   uuid.UUID
	BuyerID    uuid.UUID
	ExternalID string
	Address    string
	Name       string
	Contact    string
	Message    string
	CreatedAt  time.Time
}

// Favorite — сохранённый объект. ChatID помнит, из какого подбора он сохранён:
// с ним паспорт откроется с досье, без него — как «с карты». nil — законное
// значение (объект сохранён с карты или чат удалён).
type Favorite struct {
	UserID     uuid.UUID
	ExternalID string
	ChatID     *uuid.UUID
	CreatedAt  time.Time
}

// ResultFeedback — оценка объекта в выдаче. Всегда в контексте чата: вне
// запроса «подходит / не подходит» ничего не значит.
type ResultFeedback struct {
	UserID     uuid.UUID
	ChatID     uuid.UUID
	ExternalID string
	Verdict    string
	Reason     string
}

// ProductEvent — шаг воронки. UserID == uuid.Nil означает «актор неизвестен»
// и записывается как NULL: телеметрия не имеет права падать из-за этого.
type ProductEvent struct {
	UserID     uuid.UUID
	IsGuest    bool
	Kind       string
	ChatID     *uuid.UUID
	ExternalID string
	Props      map[string]any
}
