package domain

import (
	"time"

	"github.com/google/uuid"
)

// OwnerListing — объявление, которым продавец управляет через личный кабинет.
// Указатели у числовых полей отличают «не заполнено» от нуля: черновик может
// не иметь цены, и это не то же самое, что цена 0.
type OwnerListing struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	ExternalID   string
	Origin       string
	Status       string
	Verification string
	City         string

	Price             *int64
	Area              *float32
	KitchenArea       *float32
	Rooms             *int
	Level             *int
	Levels            *int
	Address           string
	Lng               *float64
	Lat               *float64
	WindowOrientation []string
	Description       string
	Photos            []string

	SourceURL   string
	ImportError string

	CreatedAt   time.Time
	UpdatedAt   time.Time
	PublishedAt *time.Time
}

// OwnerListingFields — редактируемая часть. nil означает «поле не передано» и
// сохраняет прежнее значение; так PATCH не обнуляет то, чего в запросе не было.
type OwnerListingFields struct {
	Price             *int64
	Area              *float32
	KitchenArea       *float32
	Rooms             *int
	Level             *int
	Levels            *int
	Address           *string
	Lng               *float64
	Lat               *float64
	WindowOrientation *[]string
	Description       *string
	City              *string
}
