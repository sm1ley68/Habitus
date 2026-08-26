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
	events  *service.EventRecorder
}

func NewObjectHandler(objects *service.ObjectService, events *service.EventRecorder) *ObjectHandler {
	return &ObjectHandler{objects: objects, events: events}
}

// Get implements GET /objects/{object_id}?chat_id=. ObjectService attaches a
// query-specific dossier from its versioned lazy cache and falls back to an
// honest secondary-only response when exact evidence is unavailable.
func (h *ObjectHandler) Get(c *fiber.Ctx) error {
	// chat_id необязателен: без него объект открывается «с карты», вне подбора.
	// Переданный, но битый chat_id — по-прежнему ошибка, а не тихий фолбэк.
	var chatID uuid.UUID
	if raw := c.Query("chat_id"); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			return apperr.ChatNotFound()
		}
		chatID = parsed
	}
	objectID := c.Params("object_id")

	passport, err := h.objects.GetPassport(c.Context(), middleware.UserID(c), chatID, objectID)
	if err != nil {
		return err
	}

	var chatIDPtr *uuid.UUID
	if chatID != uuid.Nil {
		chatIDPtr = &chatID
	}
	// contact.kind в свойствах — по нему видно, у скольких открытых объектов
	// вообще был путь к продавцу: без этого падение конверсии в заявку не
	// отличить от «заявку было некуда отправить».
	recordEvent(c, h.events, service.EventPassportOpened, chatIDPtr, objectID,
		map[string]any{"contact_kind": passport.Contact.Kind})
	return c.JSON(passport)
}
