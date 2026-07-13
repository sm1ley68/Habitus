package handlers

import (
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

// Layers implements GET /geo/layers?city=&layers=a,b,c — unknown layer names
// are silently dropped per frontend/Пайплайн фронт.md §5, not an error.
func (h *GeoHandler) Layers(c *fiber.Ctx) error {
	raw := c.Query("layers")
	var requested []string
	if raw != "" {
		requested = strings.Split(raw, ",")
	}

	layers, err := h.layers.Layers(c.Context(), requested)
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"city": c.Query("city"), "layers": layers})
}
