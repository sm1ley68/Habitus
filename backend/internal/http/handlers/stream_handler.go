package handlers

import (
	"bufio"
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/client"
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
	Text  string              `json:"text"`
	Point *streamPointRequest `json:"point"`
}

type streamPointRequest struct {
	Lon     *float64 `json:"lon"`
	Lat     *float64 `json:"lat"`
	Minutes *int     `json:"minutes"`
	Mode    *string  `json:"mode"`
}

func normalizePoint(p *streamPointRequest) (*client.PointConstraint, error) {
	if p == nil {
		return nil, nil
	}
	if p.Lon == nil || p.Lat == nil {
		return nil, fmt.Errorf("point.lon and point.lat are required")
	}
	if *p.Lon < -180 || *p.Lon > 180 {
		return nil, fmt.Errorf("point.lon must be between -180 and 180")
	}
	if *p.Lat < -90 || *p.Lat > 90 {
		return nil, fmt.Errorf("point.lat must be between -90 and 90")
	}

	minutes := 15
	if p.Minutes != nil {
		minutes = *p.Minutes
	}
	if minutes < 1 || minutes > 60 {
		return nil, fmt.Errorf("point.minutes must be between 1 and 60")
	}

	mode := "foot-walking"
	if p.Mode != nil {
		mode = *p.Mode
	}
	switch mode {
	case "foot-walking", "cycling-regular", "driving-car":
	default:
		return nil, fmt.Errorf("point.mode is invalid")
	}

	return &client.PointConstraint{
		Lon: *p.Lon, Lat: *p.Lat, Minutes: minutes, Mode: mode,
	}, nil
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
	point, err := normalizePoint(req.Point)
	if err != nil {
		return apperr.Validation(err.Error())
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
		h.stream.Run(streamCtx, chat, text, point, sse.New(w))
	})
	return nil
}
