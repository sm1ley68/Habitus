package handlers

import (
	"bufio"
	"context"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/http/middleware"
	"habitus-backend/internal/http/sse"
	"habitus-backend/internal/service"
)

type StreamHandler struct {
	chats  *service.ChatService
	stream *service.SearchStreamService
}

func NewStreamHandler(chats *service.ChatService, stream *service.SearchStreamService) *StreamHandler {
	return &StreamHandler{chats: chats, stream: stream}
}

type streamRequest struct {
	Text string `json:"text"`
}

// PostMessagesStream implements POST /chats/{chat_id}/messages/stream —
// see plan §4 for the full event sequence and concurrency/disconnect design.
func (h *StreamHandler) PostMessagesStream(c *fiber.Ctx) error {
	userID := middleware.UserID(c)
	chatID, err := uuid.Parse(c.Params("chat_id"))
	if err != nil {
		return apperr.ChatNotFound()
	}

	var req streamRequest
	if err := c.BodyParser(&req); err != nil || strings.TrimSpace(req.Text) == "" {
		return apperr.Validation("text is required")
	}

	chat, err := h.chats.GetOwned(c.Context(), userID, chatID)
	if err != nil {
		return err
	}

	if !h.stream.TryLock(chatID) {
		return apperr.StreamInProgress()
	}

	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("X-Accel-Buffering", "no")

	streamCtx, cancel := context.WithTimeout(context.Background(), h.stream.TotalBudget())

	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		defer cancel()
		defer h.stream.Unlock(chatID)
		h.stream.Run(streamCtx, chat, req.Text, sse.New(w))
	})
	return nil
}
