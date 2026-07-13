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
	CreatedAt    time.Time
	UpdatedAt    time.Time
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
	CreatedAt     time.Time
}

// ChatSearchResult is the latest-snapshot-per-object row that GET /objects/{id}
// actually reads — upsert-latest-wins by design (see plan §2).
type ChatSearchResult struct {
	ChatID       uuid.UUID
	ExternalID   string
	SearchID     uuid.UUID
	Price        *int64
	Area         *float64
	Rooms        *int
	AddressFacts map[string]any
	Score        float64
	Explanation  string
	UpdatedAt    time.Time
}

// Listing is a read-only projection of the Python-owned `listings` table —
// only the columns the backend actually needs for display gap-filling.
type Listing struct {
	ExternalID string
	Price      *int64
	Area       *float64
	Rooms      *int
	Level      *int
	Levels     *int
	Lon        *float64
	Lat        *float64
}

// POI is a read-only projection of the Python-owned `poi` table.
type POI struct {
	Kind string
	Name string
	Lon  float64
	Lat  float64
}
