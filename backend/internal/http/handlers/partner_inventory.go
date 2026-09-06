// partner_inventory.go — витрина партнёра: объявления и заявки по ним.
//
// Под капотом это тот же кабинет продавца, что и в B2C: у партнёра есть
// служебный аккаунт, и owner_listings/leads работают для него без изменений.
// Наружу форма другая — курсорная пагинация и конверт списка Partner API.
package handlers

import (
	"strings"

	"github.com/gofiber/fiber/v2"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/domain"
	"habitus-backend/internal/http/middleware"
	"habitus-backend/internal/service"
)

type PartnerInventoryHandler struct {
	listings *service.OwnerListingService
	leads    *service.LeadService
	// webhooks может быть nil — доставка событий выключена; публикация от
	// этого работать не перестаёт.
	webhooks *service.PartnerWebhookService
}

func NewPartnerInventoryHandler(listings *service.OwnerListingService,
	leads *service.LeadService, webhooks *service.PartnerWebhookService) *PartnerInventoryHandler {
	return &PartnerInventoryHandler{listings: listings, leads: leads, webhooks: webhooks}
}

var validListingStatus = map[string]bool{
	"draft": true, "publishing": true, "published": true,
	"unpublished": true, "failed": true,
}

// ListListings implements GET /partner/v1/listings.
func (h *PartnerInventoryHandler) ListListings(c *fiber.Ctx) error {
	page, err := ParsePage(c)
	if err != nil {
		return err
	}
	status := strings.TrimSpace(c.Query("status"))
	if status != "" && !validListingStatus[status] {
		return apperr.Validation("Неизвестный status объявления").WithParam("status")
	}
	userID := middleware.PartnerIdentity(c).Partner.UserID
	items, total, err := h.listings.ListPage(c.Context(), userID, status, page.Limit, page.Offset)
	if err != nil {
		return err
	}
	data := make([]fiber.Map, 0, len(items))
	for _, l := range items {
		data = append(data, partnerListingDTO(l))
	}
	return c.JSON(ListResponse(ObjectListing, data, page, total, len(items)))
}

// GetListing implements GET /partner/v1/listings/{listing_id}.
func (h *PartnerInventoryHandler) GetListing(c *fiber.Ctx) error {
	id, err := partnerUUID(c, "listing_id", apperr.OwnerListingNotFound)
	if err != nil {
		return err
	}
	l, err := h.listings.Get(c.Context(), middleware.PartnerIdentity(c).Partner.UserID, id)
	if err != nil {
		return err
	}
	return c.JSON(partnerListingDTO(l))
}

type partnerListingBody struct {
	City              *string     `json:"city"`
	Price             *int64      `json:"price"`
	Area              *float32    `json:"area"`
	KitchenArea       *float32    `json:"kitchen_area"`
	Rooms             *int        `json:"rooms"`
	Level             *int        `json:"level"`
	Levels            *int        `json:"levels"`
	Address           *string     `json:"address"`
	Coordinates       *[2]float64 `json:"coordinates"`
	WindowOrientation *[]string   `json:"window_orientation"`
	Description       *string     `json:"description"`
	Photos            *[]string   `json:"photos"`
	// Publish=true публикует объявление тем же запросом. Интеграции почти
	// всегда нужен именно этот сценарий: заливка каталога, где черновик —
	// промежуточное состояние, а не рабочий режим.
	Publish bool `json:"publish"`
}

// CreateListing implements POST /partner/v1/listings.
//
// Провал публикации не отменяет создание: карточка остаётся со статусом
// failed и причиной — терять уже принятые данные из-за недоступного
// индексатора нельзя. Тот же выбор, что в импорте кабинета.
func (h *PartnerInventoryHandler) CreateListing(c *fiber.Ctx) error {
	var body partnerListingBody
	if err := c.BodyParser(&body); err != nil {
		return apperr.Validation("Не удалось разобрать тело запроса")
	}
	if body.City == nil {
		return apperr.Validation("city обязателен (msk или spb)").WithParam("city")
	}
	if body.Coordinates == nil {
		return apperr.Validation("coordinates обязательны: [lng, lat] в WGS84").
			WithParam("coordinates")
	}
	if err := validateCoordinates(*body.Coordinates); err != nil {
		return err
	}
	lng, lat := body.Coordinates[0], body.Coordinates[1]
	draft := domain.OwnerListing{
		City: *body.City, Price: body.Price, Area: body.Area,
		KitchenArea: body.KitchenArea, Rooms: body.Rooms,
		Level: body.Level, Levels: body.Levels, Lng: &lng, Lat: &lat,
	}
	if body.Address != nil {
		draft.Address = *body.Address
	}
	if body.Description != nil {
		draft.Description = *body.Description
	}
	if body.WindowOrientation != nil {
		draft.WindowOrientation = *body.WindowOrientation
	}
	if body.Photos != nil {
		draft.Photos = *body.Photos
	}

	userID := middleware.PartnerIdentity(c).Partner.UserID
	created, err := h.listings.CreateManual(c.Context(), userID, draft)
	if err != nil {
		return err
	}
	if body.Publish {
		if published, pubErr := h.listings.Publish(c.Context(), userID, created.ID); pubErr == nil {
			created = published
			h.emitListing(c, service.EventListingPublished, created)
		} else {
			created, _ = h.listings.Get(c.Context(), userID, created.ID)
		}
	}
	return c.Status(fiber.StatusCreated).JSON(partnerListingDTO(created))
}

// UpdateListing implements PATCH /partner/v1/listings/{listing_id}.
// Непереданное поле сохраняет прежнее значение: PATCH не обнуляет то, чего в
// теле не было.
func (h *PartnerInventoryHandler) UpdateListing(c *fiber.Ctx) error {
	id, err := partnerUUID(c, "listing_id", apperr.OwnerListingNotFound)
	if err != nil {
		return err
	}
	var body partnerListingBody
	if err := c.BodyParser(&body); err != nil {
		return apperr.Validation("Не удалось разобрать тело запроса")
	}
	fields := domain.OwnerListingFields{
		City: body.City, Price: body.Price, Area: body.Area,
		KitchenArea: body.KitchenArea, Rooms: body.Rooms, Level: body.Level,
		Levels: body.Levels, Address: body.Address,
		WindowOrientation: body.WindowOrientation, Description: body.Description,
	}
	if body.Coordinates != nil {
		if err := validateCoordinates(*body.Coordinates); err != nil {
			return err
		}
		lng, lat := body.Coordinates[0], body.Coordinates[1]
		fields.Lng, fields.Lat = &lng, &lat
	}
	userID := middleware.PartnerIdentity(c).Partner.UserID
	updated, err := h.listings.Update(c.Context(), userID, id, fields)
	if err != nil {
		return err
	}
	if body.Photos != nil {
		updated, err = h.listings.SetPhotos(c.Context(), userID, id, *body.Photos)
		if err != nil {
			return err
		}
	}
	return c.JSON(partnerListingDTO(updated))
}

// DeleteListing implements DELETE /partner/v1/listings/{listing_id}. Сначала
// объект гасится в витрине и только потом удаляется карточка — обратный
// порядок оставил бы в поиске объявление, которым уже никто не владеет.
func (h *PartnerInventoryHandler) DeleteListing(c *fiber.Ctx) error {
	id, err := partnerUUID(c, "listing_id", apperr.OwnerListingNotFound)
	if err != nil {
		return err
	}
	if err := h.listings.Delete(c.Context(), middleware.PartnerIdentity(c).Partner.UserID, id); err != nil {
		return err
	}
	return c.JSON(fiber.Map{"object": ObjectListing, "id": id, "deleted": true})
}

// PublishListing implements POST /partner/v1/listings/{listing_id}/publish.
func (h *PartnerInventoryHandler) PublishListing(c *fiber.Ctx) error {
	id, err := partnerUUID(c, "listing_id", apperr.OwnerListingNotFound)
	if err != nil {
		return err
	}
	l, err := h.listings.Publish(c.Context(), middleware.PartnerIdentity(c).Partner.UserID, id)
	if err != nil {
		return err
	}
	h.emitListing(c, service.EventListingPublished, l)
	return c.JSON(partnerListingDTO(l))
}

// UnpublishListing implements POST /partner/v1/listings/{listing_id}/unpublish.
func (h *PartnerInventoryHandler) UnpublishListing(c *fiber.Ctx) error {
	id, err := partnerUUID(c, "listing_id", apperr.OwnerListingNotFound)
	if err != nil {
		return err
	}
	l, err := h.listings.Unpublish(c.Context(), middleware.PartnerIdentity(c).Partner.UserID, id)
	if err != nil {
		return err
	}
	h.emitListing(c, service.EventListingUnpublished, l)
	return c.JSON(partnerListingDTO(l))
}

// ListLeads implements GET /partner/v1/leads.
func (h *PartnerInventoryHandler) ListLeads(c *fiber.Ctx) error {
	page, err := ParsePage(c)
	if err != nil {
		return err
	}
	rows, total, err := h.leads.ListForSeller(c.Context(),
		middleware.PartnerIdentity(c).Partner.UserID, page.Limit, page.Offset)
	if err != nil {
		return err
	}
	data := make([]fiber.Map, 0, len(rows))
	for _, l := range rows {
		data = append(data, partnerLeadDTO(l))
	}
	return c.JSON(ListResponse(ObjectLead, data, page, total, len(rows)))
}

func (h *PartnerInventoryHandler) emitListing(c *fiber.Ctx, event string, l domain.OwnerListing) {
	if h.webhooks == nil {
		return
	}
	h.webhooks.Emit(c.Context(), middleware.PartnerIdentity(c).Partner.ID, event, map[string]any{
		"id": l.ID.String(), "external_id": l.ExternalID, "status": l.Status,
		"city": l.City, "address": l.Address,
	})
}

// validateCoordinates ловит перепутанные местами lng и lat. Порядок [lng, lat]
// — самая частая ошибка интеграции, и молча принять точку в Индийском океане
// хуже, чем отказать с объяснением.
func validateCoordinates(coords [2]float64) error {
	if coords[0] < -180 || coords[0] > 180 {
		return apperr.Validation("coordinates[0] — долгота, от -180 до 180").
			WithParam("coordinates")
	}
	if coords[1] < -90 || coords[1] > 90 {
		return apperr.Validation("coordinates[1] — широта, от -90 до 90. Порядок именно [lng, lat]").
			WithParam("coordinates")
	}
	return nil
}

func partnerListingDTO(l domain.OwnerListing) fiber.Map {
	var coordinates any
	if l.Lng != nil && l.Lat != nil {
		coordinates = [2]float64{*l.Lng, *l.Lat}
	}
	dto := fiber.Map{
		"object":             ObjectListing,
		"id":                 l.ID,
		"external_id":        l.ExternalID,
		"origin":             l.Origin,
		"status":             l.Status,
		"verification":       l.Verification,
		"city":               l.City,
		"price":              l.Price,
		"area":               l.Area,
		"kitchen_area":       l.KitchenArea,
		"rooms":              l.Rooms,
		"level":              l.Level,
		"levels":             l.Levels,
		"address":            l.Address,
		"coordinates":        coordinates,
		"window_orientation": ownerStrings(l.WindowOrientation),
		"description":        l.Description,
		"photos":             ownerStrings(l.Photos),
		"source_url":         nullableString(l.SourceURL),
		"published_at":       l.PublishedAt,
		"created_at":         l.CreatedAt,
		"updated_at":         l.UpdatedAt,
	}
	// import_error появляется только когда он есть: пустая строка в ответе
	// читается как «ошибка была и она пустая».
	if l.ImportError != "" {
		dto["error"] = l.ImportError
	}
	return dto
}

func partnerLeadDTO(l domain.Lead) fiber.Map {
	return fiber.Map{
		"object":      ObjectLead,
		"id":          l.ID,
		"listing_id":  l.ListingID,
		"external_id": l.ExternalID,
		"address":     l.Address,
		"name":        l.Name,
		"contact":     l.Contact,
		"message":     l.Message,
		"created_at":  l.CreatedAt,
	}
}
