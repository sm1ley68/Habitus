package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"habitus-backend/internal/domain"
	"habitus-backend/internal/http/middleware"
	"habitus-backend/internal/service"
)

// recordEvent пишет шаг воронки из контекста запроса: только здесь известен
// признак гостя. Рекордер может быть nil (телеметрия выключена в тестах) —
// Record это переживает.
func recordEvent(c *fiber.Ctx, rec *service.EventRecorder, kind string,
	chatID *uuid.UUID, externalID string, props map[string]any) {
	rec.Record(domain.ProductEvent{
		UserID:     middleware.UserID(c),
		IsGuest:    middleware.IsGuest(c),
		Kind:       kind,
		ChatID:     chatID,
		ExternalID: externalID,
		Props:      props,
	})
}
