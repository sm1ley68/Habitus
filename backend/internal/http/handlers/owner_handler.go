package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/domain"
	"habitus-backend/internal/http/middleware"
	"habitus-backend/internal/service"
)

type OwnerHandler struct {
	listings *service.OwnerListingService
	imports  *service.OwnerImportService
}

func NewOwnerHandler(listings *service.OwnerListingService, imports *service.OwnerImportService) *OwnerHandler {
	return &OwnerHandler{listings: listings, imports: imports}
}

// OwnerListingDTO — форма ответа кабинета. Координаты отдаются парой
// [lng, lat] тем же контрактом, что и везде в проекте: фронт не делает
// никаких преобразований.
func OwnerListingDTO(l domain.OwnerListing) fiber.Map {
	// null, а не [0, 0]: у черновика без точки на карте координат нет, и
	// выдуманный ноль отрисовался бы на карте настоящей меткой.
	var coordinates any
	if l.Lng != nil && l.Lat != nil {
		coordinates = [2]float64{*l.Lng, *l.Lat}
	}
	return fiber.Map{
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
		"source_url":         l.SourceURL,
		"import_error":       l.ImportError,
		"published_at":       l.PublishedAt,
		"updated_at":         l.UpdatedAt,
	}
}

// ownerStrings превращает nil-срез в пустой массив: в JSON null и []
// различимы, и фронту приходится обрабатывать оба случая на ровном месте.
func ownerStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func ownerPreviewDTO(p service.ImportPreview) fiber.Map {
	similar := make([]fiber.Map, 0, len(p.Similar))
	for _, s := range p.Similar {
		similar = append(similar, fiber.Map{
			"external_id": s.ExternalID, "address": s.Address,
			"price": s.Price, "area": s.Area,
		})
	}
	out := fiber.Map{
		"verdict": p.Verdict,
		"draft":   OwnerListingDTO(p.Draft),
		"similar": similar,
	}
	if p.ExistingID != nil {
		out["existing_id"] = *p.ExistingID
	}
	return out
}

func ownerListingID(c *fiber.Ctx) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Params("listing_id"))
	if err != nil {
		// Кривой uuid неотличим от несуществующего объявления: 404, не 400 —
		// тот же выбор, что сделан для чатов.
		return uuid.Nil, apperr.OwnerListingNotFound()
	}
	return id, nil
}

func (h *OwnerHandler) List(c *fiber.Ctx) error {
	items, err := h.listings.List(c.Context(), middleware.UserID(c))
	if err != nil {
		return err
	}
	out := make([]fiber.Map, 0, len(items))
	for _, l := range items {
		out = append(out, OwnerListingDTO(l))
	}
	return c.JSON(fiber.Map{"listings": out})
}

func (h *OwnerHandler) Get(c *fiber.Ctx) error {
	id, err := ownerListingID(c)
	if err != nil {
		return err
	}
	l, err := h.listings.Get(c.Context(), middleware.UserID(c), id)
	if err != nil {
		return err
	}
	return c.JSON(OwnerListingDTO(l))
}

type ownerListingBody struct {
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
}

func (h *OwnerHandler) Create(c *fiber.Ctx) error {
	var body ownerListingBody
	if err := c.BodyParser(&body); err != nil {
		return apperr.Validation("Не удалось разобрать тело запроса")
	}
	if body.City == nil {
		return apperr.Validation("Не указан город")
	}
	if body.Coordinates == nil {
		return apperr.Validation("Не указаны координаты — поставьте точку на карте")
	}
	lng, lat := body.Coordinates[0], body.Coordinates[1]
	draft := domain.OwnerListing{
		City: *body.City, Price: body.Price, Area: body.Area,
		KitchenArea: body.KitchenArea, Rooms: body.Rooms,
		Level: body.Level, Levels: body.Levels,
		Lng: &lng, Lat: &lat,
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
	created, err := h.listings.CreateManual(c.Context(), middleware.UserID(c), draft)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(OwnerListingDTO(created))
}

func (h *OwnerHandler) Update(c *fiber.Ctx) error {
	id, err := ownerListingID(c)
	if err != nil {
		return err
	}
	var body ownerListingBody
	if err := c.BodyParser(&body); err != nil {
		return apperr.Validation("Не удалось разобрать тело запроса")
	}
	fields := domain.OwnerListingFields{
		City: body.City, Price: body.Price, Area: body.Area,
		KitchenArea: body.KitchenArea, Rooms: body.Rooms,
		Level: body.Level, Levels: body.Levels, Address: body.Address,
		WindowOrientation: body.WindowOrientation, Description: body.Description,
	}
	if body.Coordinates != nil {
		lng, lat := body.Coordinates[0], body.Coordinates[1]
		fields.Lng, fields.Lat = &lng, &lat
	}
	updated, err := h.listings.Update(c.Context(), middleware.UserID(c), id, fields)
	if err != nil {
		return err
	}
	return c.JSON(OwnerListingDTO(updated))
}

type importBody struct {
	URL string `json:"url"`
}

func (h *OwnerHandler) ImportPreview(c *fiber.Ctx) error {
	var body importBody
	if err := c.BodyParser(&body); err != nil {
		return apperr.Validation("Не удалось разобрать тело запроса")
	}
	preview, err := h.imports.Preview(c.Context(), middleware.UserID(c), body.URL)
	if err != nil {
		return err
	}
	return c.JSON(ownerPreviewDTO(preview))
}

// Import создаёт карточку и, если включена автопубликация, сразу отдаёт её
// витрине. Провал публикации не отменяет импорт: карточка остаётся в кабинете
// со статусом failed и кнопкой «Повторить» — терять уже забранные с Циана
// данные из-за недоступного ML нельзя.
func (h *OwnerHandler) Import(c *fiber.Ctx) error {
	var body importBody
	if err := c.BodyParser(&body); err != nil {
		return apperr.Validation("Не удалось разобрать тело запроса")
	}
	userID := middleware.UserID(c)
	created, err := h.imports.Import(c.Context(), userID, body.URL)
	if err != nil {
		return err
	}
	if h.listings.Autopublish() && created.Status == "draft" {
		if published, pubErr := h.listings.Publish(c.Context(), userID, created.ID); pubErr == nil {
			created = published
		} else {
			created, _ = h.listings.Get(c.Context(), userID, created.ID)
		}
	}
	return c.Status(fiber.StatusCreated).JSON(OwnerListingDTO(created))
}

func (h *OwnerHandler) Publish(c *fiber.Ctx) error {
	id, err := ownerListingID(c)
	if err != nil {
		return err
	}
	l, err := h.listings.Publish(c.Context(), middleware.UserID(c), id)
	if err != nil {
		return err
	}
	return c.JSON(OwnerListingDTO(l))
}

func (h *OwnerHandler) Unpublish(c *fiber.Ctx) error {
	id, err := ownerListingID(c)
	if err != nil {
		return err
	}
	l, err := h.listings.Unpublish(c.Context(), middleware.UserID(c), id)
	if err != nil {
		return err
	}
	return c.JSON(OwnerListingDTO(l))
}

func (h *OwnerHandler) Delete(c *fiber.Ctx) error {
	id, err := ownerListingID(c)
	if err != nil {
		return err
	}
	if err := h.listings.Delete(c.Context(), middleware.UserID(c), id); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}
