// partner_meta.go — служебные ручки Partner API: кто я, живо ли, спецификация.
package handlers

import (
	"github.com/gofiber/fiber/v2"

	"habitus-backend/internal/http/middleware"
	"habitus-backend/internal/service"
)

type PartnerMetaHandler struct {
	quotas  *middleware.PartnerQuotas
	ready   *service.ReadinessService
	spec    []byte
	version string
}

func NewPartnerMetaHandler(quotas *middleware.PartnerQuotas, ready *service.ReadinessService,
	spec []byte, version string) *PartnerMetaHandler {
	return &PartnerMetaHandler{quotas: quotas, ready: ready, spec: spec, version: version}
}

// Me implements GET /partner/v1/me — первая ручка, которую дёргает любая
// интеграция: она отвечает и «ключ рабочий», и «что мне разрешено», и «в
// какие лимиты я упрусь». Без неё эти три вопроса выясняются методом
// подстановки в бою.
func (h *PartnerMetaHandler) Me(c *fiber.Ctx) error {
	identity := middleware.PartnerIdentity(c)
	p := identity.Partner
	rpm, llm := h.quotas.DefaultRPM, h.quotas.DefaultLLM
	if p.RateLimitPerMin != nil && *p.RateLimitPerMin > 0 {
		rpm = *p.RateLimitPerMin
	}
	if p.LLMPerHour != nil && *p.LLMPerHour > 0 {
		llm = *p.LLMPerHour
	}
	return c.JSON(fiber.Map{
		"object": ObjectPartner,
		"id":     p.ID,
		"slug":   p.Slug,
		"name":   p.Name,
		"status": p.Status,
		"key": fiber.Map{
			"id":           identity.Key.ID,
			"prefix":       identity.Key.Prefix,
			"name":         nullableString(identity.Key.Name),
			"environment":  identity.Key.Environment,
			"scopes":       ownerStrings(identity.Key.Scopes),
			"expires_at":   identity.Key.ExpiresAt,
			"last_used_at": identity.Key.LastUsedAt,
			"created_at":   identity.Key.CreatedAt,
		},
		"limits": fiber.Map{
			"requests_per_minute":   rpm,
			"llm_requests_per_hour": llm,
		},
		"api_version": h.version,
	})
}

// Status implements GET /partner/v1/status — публичная проба без ключа.
// Партнёру нужно уметь отличить «мой ключ протух» от «у них лежит поиск», не
// имея при этом валидного ключа.
func (h *PartnerMetaHandler) Status(c *fiber.Ctx) error {
	checks := map[string]string{}
	healthy := true
	if h.ready != nil {
		healthy, checks = h.ready.Check(c.Context())
	}
	status := "operational"
	code := fiber.StatusOK
	if !healthy {
		status = "degraded"
		code = fiber.StatusServiceUnavailable
	}
	return c.Status(code).JSON(fiber.Map{
		"object": "status", "status": status,
		"api_version": h.version, "checks": checks,
	})
}

// OpenAPI implements GET /partner/v1/openapi.json — машиночитаемый контракт.
// Спека вшита в бинарь, а не читается с диска: спецификация, которая может
// разъехаться с задеплоенным кодом, хуже, чем её отсутствие.
func (h *PartnerMetaHandler) OpenAPI(c *fiber.Ctx) error {
	c.Set(fiber.HeaderContentType, "application/json; charset=utf-8")
	c.Set(fiber.HeaderCacheControl, "public, max-age=300")
	return c.Send(h.spec)
}
