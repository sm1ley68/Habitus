package handlers

import (
	"bufio"
	"context"
	"strings"
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/http/middleware"
	"habitus-backend/internal/http/sse"
	"habitus-backend/internal/service"
)

type ObjectAskHandler struct {
	objects *service.ObjectService
	ask     *service.ObjectAskService
}

func NewObjectAskHandler(objects *service.ObjectService,
	ask *service.ObjectAskService) *ObjectAskHandler {
	return &ObjectAskHandler{objects: objects, ask: ask}
}

type objectAskRequest struct {
	Text   string `json:"text"`
	ChatID string `json:"chat_id"`
}

func (h *ObjectAskHandler) PostStream(c *fiber.Ctx) error {
	var req objectAskRequest
	if err := c.BodyParser(&req); err != nil {
		return apperr.Validation("invalid request body")
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		return apperr.Validation("text is required")
	}
	if utf8.RuneCountInString(text) > 2000 {
		return apperr.Validation("text must not exceed 2000 characters")
	}
	chatID, err := uuid.Parse(req.ChatID)
	if err != nil {
		return apperr.ChatNotFound()
	}
	objectID := c.Params("object_id")
	if objectID == "" {
		return apperr.ObjectNotFound()
	}
	if !h.ask.TryLock(chatID, objectID) {
		return apperr.ObjectStreamInProgress()
	}
	passport, err := h.objects.GetPassport(c.Context(), middleware.UserID(c), chatID, objectID)
	if err != nil {
		h.ask.Unlock(chatID, objectID)
		return err
	}

	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("X-Accel-Buffering", "no")
	streamCtx, cancel := context.WithTimeout(context.Background(), h.ask.TotalBudget())
	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		defer cancel()
		defer h.ask.Unlock(chatID, objectID)
		h.ask.Run(streamCtx, chatID, objectID, text, passport, sse.New(w))
	})
	return nil
}
