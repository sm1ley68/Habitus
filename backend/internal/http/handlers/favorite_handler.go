package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/http/middleware"
	"habitus-backend/internal/service"
)

type FavoriteHandler struct {
	favorites *service.FavoriteService
}

func NewFavoriteHandler(favorites *service.FavoriteService) *FavoriteHandler {
	return &FavoriteHandler{favorites: favorites}
}

const (
	favoritesDefaultLimit = 20
	favoritesMaxLimit     = 100
)

type favoriteRequest struct {
	ChatID string `json:"chat_id"`
}

// Add implements PUT /favorites/{object_id}. PUT, а не POST: сохранение
// идемпотентно, повторный клик по «сохранить» — то же состояние.
func (h *FavoriteHandler) Add(c *fiber.Ctx) error {
	objectID := c.Params("object_id")
	if objectID == "" {
		return apperr.ObjectNotFound()
	}

	// Тело необязательно: объект можно сохранить и с карты, вне подбора.
	var req favoriteRequest
	_ = c.BodyParser(&req)

	var chatID *uuid.UUID
	if req.ChatID != "" {
		parsed, err := uuid.Parse(req.ChatID)
		if err != nil {
			return apperr.ChatNotFound()
		}
		chatID = &parsed
	}

	if err := h.favorites.Add(c.Context(), middleware.UserID(c), objectID, chatID); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *FavoriteHandler) Remove(c *fiber.Ctx) error {
	objectID := c.Params("object_id")
	if objectID == "" {
		return apperr.ObjectNotFound()
	}
	if err := h.favorites.Remove(c.Context(), middleware.UserID(c), objectID); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// List implements GET /favorites?limit=&offset=.
func (h *FavoriteHandler) List(c *fiber.Ctx) error {
	limit, offset := parseLimitOffset(c, favoritesDefaultLimit)
	if limit > favoritesMaxLimit {
		limit = favoritesMaxLimit
	}

	objects, count, total, err := h.favorites.List(c.Context(), middleware.UserID(c), limit, offset)
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"objects": objects, "count": count, "total": total})
}
