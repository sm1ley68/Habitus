package handlers

import (
	"github.com/gofiber/fiber/v2"

	"habitus-backend/internal/service"
)

type HealthHandler struct {
	ready *service.ReadinessService
}

func NewHealthHandler(ready *service.ReadinessService) *HealthHandler {
	return &HealthHandler{ready: ready}
}

// Live — liveness. Зависимости здесь НЕ проверяются намеренно: сигнал
// «перезапусти меня» не должен зависеть от моргнувшего Postgres, иначе одна
// недоступная БД укладывает весь пул контейнеров шлюза.
func (h *HealthHandler) Live(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{"status": "ok"})
}

// Ready — readiness: шлюз бесполезен без Postgres и без ML-сервиса, поэтому
// при мёртвой зависимости отвечает 503 и называет, какая именно легла.
func (h *HealthHandler) Ready(c *fiber.Ctx) error {
	ok, checks := h.ready.Check(c.Context())
	if !ok {
		return c.Status(fiber.StatusServiceUnavailable).
			JSON(fiber.Map{"status": "degraded", "checks": checks})
	}
	return c.JSON(fiber.Map{"status": "ready", "checks": checks})
}
