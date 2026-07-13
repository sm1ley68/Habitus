package handlers

import (
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/domain"
	"habitus-backend/internal/http/middleware"
	"habitus-backend/internal/service"
)

type ChatHandler struct {
	chats *service.ChatService
}

func NewChatHandler(chats *service.ChatService) *ChatHandler {
	return &ChatHandler{chats: chats}
}

type createChatRequest struct {
	City  string `json:"city"`
	Title string `json:"title"`
}

type renameChatRequest struct {
	Title string `json:"title"`
}

func chatDTO(c domain.Chat) fiber.Map {
	return fiber.Map{
		"chat_id":    c.ID,
		"city":       c.City,
		"title":      c.Title,
		"created_at": c.CreatedAt,
	}
}

func chatListItemDTO(c domain.Chat) fiber.Map {
	return fiber.Map{
		"chat_id":    c.ID,
		"city":       c.City,
		"title":      c.Title,
		"updated_at": c.UpdatedAt,
	}
}

func messageDTO(m domain.Message) fiber.Map {
	dto := fiber.Map{
		"message_id": m.ID,
		"role":       m.Role,
		"text":       m.Text,
		"created_at": m.CreatedAt,
	}
	if m.Meta != nil {
		dto["meta"] = m.Meta
	}
	return dto
}

func parseLimitOffset(c *fiber.Ctx, defLimit int) (int, int) {
	limit := defLimit
	offset := 0
	if v, err := strconv.Atoi(c.Query("limit")); err == nil && v > 0 {
		limit = v
	}
	if v, err := strconv.Atoi(c.Query("offset")); err == nil && v >= 0 {
		offset = v
	}
	return limit, offset
}

func (h *ChatHandler) Create(c *fiber.Ctx) error {
	var req createChatRequest
	if err := c.BodyParser(&req); err != nil {
		return apperr.Validation("invalid request body")
	}
	chat, err := h.chats.Create(c.Context(), middleware.UserID(c), req.City, req.Title)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(chatDTO(chat))
}

func (h *ChatHandler) List(c *fiber.Ctx) error {
	limit, offset := parseLimitOffset(c, 20)
	chats, total, err := h.chats.List(c.Context(), middleware.UserID(c), limit, offset)
	if err != nil {
		return err
	}
	items := make([]fiber.Map, len(chats))
	for i, ch := range chats {
		items[i] = chatListItemDTO(ch)
	}
	return c.JSON(fiber.Map{"chats": items, "total": total})
}

func (h *ChatHandler) Rename(c *fiber.Ctx) error {
	chatID, err := uuid.Parse(c.Params("chat_id"))
	if err != nil {
		return apperr.ChatNotFound()
	}
	var req renameChatRequest
	if err := c.BodyParser(&req); err != nil {
		return apperr.Validation("invalid request body")
	}
	chat, err := h.chats.Rename(c.Context(), middleware.UserID(c), chatID, req.Title)
	if err != nil {
		return err
	}
	return c.JSON(chatDTO(chat))
}

func (h *ChatHandler) Delete(c *fiber.Ctx) error {
	chatID, err := uuid.Parse(c.Params("chat_id"))
	if err != nil {
		return apperr.ChatNotFound()
	}
	if err := h.chats.Delete(c.Context(), middleware.UserID(c), chatID); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *ChatHandler) Messages(c *fiber.Ctx) error {
	chatID, err := uuid.Parse(c.Params("chat_id"))
	if err != nil {
		return apperr.ChatNotFound()
	}
	limit, offset := parseLimitOffset(c, 50)
	messages, total, err := h.chats.Messages(c.Context(), middleware.UserID(c), chatID, limit, offset)
	if err != nil {
		return err
	}
	items := make([]fiber.Map, len(messages))
	for i, m := range messages {
		items[i] = messageDTO(m)
	}
	return c.JSON(fiber.Map{"messages": items, "total": total})
}
