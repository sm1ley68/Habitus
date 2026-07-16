package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/http/middleware"
	"habitus-backend/internal/service"
)

type ObjectHandler struct {
	objects *service.ObjectService
}

func NewObjectHandler(objects *service.ObjectService) *ObjectHandler {
	return &ObjectHandler{objects: objects}
}

// Get implements GET /objects/{object_id}?chat_id=. ObjectService attaches a
// query-specific dossier from its versioned lazy cache and falls back to an
// honest secondary-only response when exact evidence is unavailable.
func (h *ObjectHandler) Get(c *fiber.Ctx) error {
	chatID, err := uuid.Parse(c.Query("chat_id"))
	if err != nil {
		return apperr.ChatNotFound()
	}
	objectID := c.Params("object_id")

	passport, err := h.objects.GetPassport(c.Context(), middleware.UserID(c), chatID, objectID)
	if err != nil {
		return err
	}
	return c.JSON(passport)
}
