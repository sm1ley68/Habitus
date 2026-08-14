package handlers

import (
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"

	"habitus-backend/internal/service"
)

type GeoHandler struct {
	layers *service.GeoLayersService
}

func NewGeoHandler(layers *service.GeoLayersService) *GeoHandler {
	return &GeoHandler{layers: layers}
}

// parseBbox разбирает "minLon,minLat,maxLon,maxLat" (EPSG:4326, порядок [lng,lat]).
// Неполный или неразбираемый bbox — это nil, а не ошибка: evidence-слой тогда
// вернётся пустым, как и любой слой без данных.
func parseBbox(raw string) *[4]float64 {
	parts := strings.Split(raw, ",")
	if len(parts) != 4 {
		return nil
	}
	var box [4]float64
	for i, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return nil
		}
		box[i] = v
	}
	return &box
}

// Layers implements GET /geo/layers?city=&layers=a,b,c&bbox=… — unknown layer
// names are silently dropped per frontend/Пайплайн фронт.md §5, not an error.
func (h *GeoHandler) Layers(c *fiber.Ctx) error {
	raw := c.Query("layers")
	var requested []string
	if raw != "" {
		requested = strings.Split(raw, ",")
	}
	city := c.Query("city")
	if city == "" {
		city = "msk"
	}

	layers, truncated, err := h.layers.Layers(c.Context(), city, requested, parseBbox(c.Query("bbox")))
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"city": city, "layers": layers, "truncated": truncated})
}
