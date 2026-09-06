// partner_geo.go — гео-слои и объявления под вьюпортом для Partner API.
// Данные те же, что у карты во фронте; отличие — конверт и явные ошибки
// вместо тихих умолчаний: интеграция должна узнать о кривом bbox сразу.
package handlers

import (
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/service"
)

type PartnerGeoHandler struct {
	layers *service.GeoLayersService
}

func NewPartnerGeoHandler(layers *service.GeoLayersService) *PartnerGeoHandler {
	return &PartnerGeoHandler{layers: layers}
}

// parsePartnerBbox отличается от parseBbox во фронтовой ручке: там кривой
// параметр молча даёт пустой слой, здесь — 400. У карты пустой слой это
// нормальный кадр, у интеграции — молча потерянные данные.
func parsePartnerBbox(raw string) (*[4]float64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) != 4 {
		return nil, apperr.Validation(
			"bbox — четыре числа через запятую: minLng,minLat,maxLng,maxLat").WithParam("bbox")
	}
	var box [4]float64
	for i, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return nil, apperr.Validation("bbox содержит не число: " + p).WithParam("bbox")
		}
		box[i] = v
	}
	if box[0] > box[2] || box[1] > box[3] {
		return nil, apperr.Validation(
			"Углы bbox переставлены: сначала юго-западный, потом северо-восточный").
			WithParam("bbox")
	}
	return &box, nil
}

// Layers implements GET /partner/v1/geo/layers.
func (h *PartnerGeoHandler) Layers(c *fiber.Ctx) error {
	city := strings.TrimSpace(c.Query("city"))
	if city == "" {
		city = "msk"
	}
	bbox, err := parsePartnerBbox(c.Query("bbox"))
	if err != nil {
		return err
	}
	var requested []string
	if raw := strings.TrimSpace(c.Query("layers")); raw != "" {
		requested = strings.Split(raw, ",")
	}
	// Неизвестный слой — ошибка, а не тихий пропуск: опечатка в имени слоя
	// иначе выглядит как «данных по нему нет», и искать её будут долго.
	for _, name := range requested {
		if !service.AllowedLayers[strings.TrimSpace(name)] {
			return apperr.Validation("Неизвестный слой: " + strings.TrimSpace(name)).
				WithParam("layers")
		}
	}

	layers, truncated, err := h.layers.Layers(c.Context(), city, requested, bbox)
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{
		"object": ObjectGeoLayers, "city": city,
		"layers": layers,
		// truncated честно называет слои, обрезанные по потолку точек: без
		// него клиент считает неполный слой полным.
		"truncated": truncated,
	})
}

// Listings implements GET /partner/v1/geo/listings — объявления в границах
// вьюпорта. Без bbox отдаётся пустая коллекция, а не весь город.
func (h *PartnerGeoHandler) Listings(c *fiber.Ctx) error {
	city := strings.TrimSpace(c.Query("city"))
	if city == "" {
		city = "msk"
	}
	bbox, err := parsePartnerBbox(c.Query("bbox"))
	if err != nil {
		return err
	}
	if bbox == nil {
		return apperr.Validation("bbox обязателен: весь город одним ответом не отдаётся").
			WithParam("bbox")
	}
	fc, err := h.layers.Listings(c.Context(), city, bbox)
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"object": "geo_listings", "city": city, "listings": fc})
}
