// http_metrics.go — HTTP-обвязка вокруг Metrics: ручка /metrics и middleware,
// который считает успешные (без ошибки) ответы в habitus_http_requests_total.
// Ответы, ушедшие через apperr/fiber.Error (middleware.ErrorHandler),
// считаются там же, где формируется их итоговый статус — см.
// internal/http/middleware/errorenvelope.go. Разделение ровно по тому же
// шву, что уже проведён в приложении: успешный путь у хендлеров, путь ошибки
// у единого ErrorHandler.
package observability

import (
	"strconv"

	"github.com/gofiber/fiber/v2"
)

// HTTPRequestsMiddleware считает завершившиеся без ошибки запросы. err != nil
// здесь не обрабатывается: тот путь заканчивается в ErrorHandler, который
// первым узнаёт итоговый статус (fiber вызывает ErrorHandler уже после того,
// как вся цепочка middleware/Next() размотана, — на этом месте c.Response()
// ещё хранит статус ДО того, как ErrorHandler его проставит).
func HTTPRequestsMiddleware(m *Metrics) fiber.Handler {
	return func(c *fiber.Ctx) error {
		err := c.Next()
		if err == nil && !selfObserving(c.Route().Path) {
			m.IncHTTPRequest(c.Route().Path, strconv.Itoa(c.Response().StatusCode()))
		}
		return err
	}
}

// selfObserving — служебные роуты не считаем: скрейп раз в 15 секунд и пробы
// живости иначе навсегда доминируют в habitus_http_requests_total и прячут
// реальный трафик API.
func selfObserving(route string) bool {
	return route == "/metrics" || route == "/health"
}

// MetricsHandler отдаёт GET /metrics — без авторизации, как /health (см.
// router.go).
func MetricsHandler(m *Metrics) fiber.Handler {
	return func(c *fiber.Ctx) error {
		c.Set(fiber.HeaderContentType, "text/plain; version=0.0.4; charset=utf-8")
		return c.SendString(m.Render())
	}
}
