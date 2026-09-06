// partner_webhooks.go — подписки партнёра на события и журнал доставок.
package handlers

import (
	"strings"

	"github.com/gofiber/fiber/v2"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/domain"
	"habitus-backend/internal/http/middleware"
	"habitus-backend/internal/service"
)

type PartnerWebhookHandler struct {
	webhooks *service.PartnerWebhookService
}

func NewPartnerWebhookHandler(webhooks *service.PartnerWebhookService) *PartnerWebhookHandler {
	return &PartnerWebhookHandler{webhooks: webhooks}
}

type webhookBody struct {
	URL    string   `json:"url"`
	Events []string `json:"events"`
}

// List implements GET /partner/v1/webhooks.
func (h *PartnerWebhookHandler) List(c *fiber.Ctx) error {
	items, err := h.webhooks.List(c.Context(), middleware.PartnerIdentity(c).Partner.ID)
	if err != nil {
		return err
	}
	data := make([]fiber.Map, 0, len(items))
	for _, w := range items {
		data = append(data, webhookDTO(w, false))
	}
	return c.JSON(ListResponse(ObjectWebhook, data, Page{Limit: len(data)}, len(data), len(data)))
}

// Create implements POST /partner/v1/webhooks. Секрет подписи возвращается
// вместе с созданной подпиской — и здесь, и в GET: в отличие от ключа API его
// нужно уметь показать повторно, иначе проверять подпись партнёру нечем.
func (h *PartnerWebhookHandler) Create(c *fiber.Ctx) error {
	var body webhookBody
	if err := c.BodyParser(&body); err != nil {
		return apperr.Validation("Не удалось разобрать тело запроса")
	}
	if strings.TrimSpace(body.URL) == "" {
		return apperr.Validation("url обязателен").WithParam("url")
	}
	w, err := h.webhooks.Create(c.Context(), middleware.PartnerIdentity(c).Partner.ID,
		body.URL, body.Events)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(webhookDTO(w, true))
}

// Get implements GET /partner/v1/webhooks/{webhook_id}.
func (h *PartnerWebhookHandler) Get(c *fiber.Ctx) error {
	id, err := partnerUUID(c, "webhook_id", apperr.WebhookNotFound)
	if err != nil {
		return err
	}
	w, err := h.webhooks.Get(c.Context(), middleware.PartnerIdentity(c).Partner.ID, id)
	if err != nil {
		return err
	}
	return c.JSON(webhookDTO(w, true))
}

// Delete implements DELETE /partner/v1/webhooks/{webhook_id}.
func (h *PartnerWebhookHandler) Delete(c *fiber.Ctx) error {
	id, err := partnerUUID(c, "webhook_id", apperr.WebhookNotFound)
	if err != nil {
		return err
	}
	if err := h.webhooks.Delete(c.Context(), middleware.PartnerIdentity(c).Partner.ID, id); err != nil {
		return err
	}
	return c.JSON(fiber.Map{"object": ObjectWebhook, "id": id, "deleted": true})
}

// Test implements POST /partner/v1/webhooks/{webhook_id}/test — проверочное
// событие. Без него первая проверка подписи у партнёра случается на боевой
// заявке, то есть тогда, когда ошибиться дороже всего.
func (h *PartnerWebhookHandler) Test(c *fiber.Ctx) error {
	id, err := partnerUUID(c, "webhook_id", apperr.WebhookNotFound)
	if err != nil {
		return err
	}
	deliveryID, err := h.webhooks.SendTest(c.Context(),
		middleware.PartnerIdentity(c).Partner.ID, id)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{
		"object": ObjectWebhookDelivery, "id": deliveryID,
		"event": service.EventPing, "status": "pending",
	})
}

// Deliveries implements GET /partner/v1/webhooks/{webhook_id}/deliveries —
// журнал: что отправляли, сколько раз и чем кончилось.
func (h *PartnerWebhookHandler) Deliveries(c *fiber.Ctx) error {
	id, err := partnerUUID(c, "webhook_id", apperr.WebhookNotFound)
	if err != nil {
		return err
	}
	page, err := ParsePage(c)
	if err != nil {
		return err
	}
	rows, total, err := h.webhooks.Deliveries(c.Context(),
		middleware.PartnerIdentity(c).Partner.ID, id, page.Limit, page.Offset)
	if err != nil {
		return err
	}
	data := make([]fiber.Map, 0, len(rows))
	for _, d := range rows {
		data = append(data, deliveryDTO(d))
	}
	return c.JSON(ListResponse(ObjectWebhookDelivery, data, page, total, len(rows)))
}

func webhookDTO(w domain.PartnerWebhook, withSecret bool) fiber.Map {
	dto := fiber.Map{
		"object": ObjectWebhook, "id": w.ID, "url": w.URL,
		"events": ownerStrings(w.Events), "active": w.Active, "created_at": w.CreatedAt,
	}
	if withSecret {
		dto["secret"] = w.Secret
	}
	return dto
}

func deliveryDTO(d domain.WebhookDelivery) fiber.Map {
	dto := fiber.Map{
		"object": ObjectWebhookDelivery, "id": d.ID, "event": d.Event,
		"status": d.Status, "attempts": d.Attempts, "created_at": d.CreatedAt,
		"delivered_at": d.DeliveredAt, "payload": d.Payload,
	}
	if d.LastError != "" {
		dto["last_error"] = d.LastError
	}
	if d.Status == "pending" {
		dto["next_attempt_at"] = d.NextAttemptAt
	}
	return dto
}
