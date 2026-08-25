package middleware

import (
	"errors"
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/observability"
)

// ErrorHandler is the single place that turns any error returned by a
// handler into the unified {"error":{"code","message"}} envelope from
// frontend/Пайплайн фронт.md §1.
//
// Это же единственное место, где для запроса, завершившегося ошибкой,
// известен ИТОГОВЫЙ статус: fiber зовёт ErrorHandler уже после того, как вся
// цепочка middleware/Next() размоталась, так что «внешний» middleware не
// увидит его через c.Response() (см. observability.HTTPRequestsMiddleware).
// Поэтому habitus_http_requests_total для ошибочных ответов считаем прямо
// здесь, у каждой ветки — где он и вычисляется.
// errorBody собирает тело конверта. Cause/Hint аддитивны и попадают в ответ
// только когда они есть: пустое поле означало бы «причина известна и она
// пустая», а это неправда — её просто нет.
func errorBody(ae *apperr.Error) fiber.Map {
	body := fiber.Map{"code": ae.Code, "message": ae.Message}
	if ae.Cause != "" {
		body["cause"] = ae.Cause
	}
	if ae.Hint != "" {
		body["hint"] = ae.Hint
	}
	return body
}

func ErrorHandler(c *fiber.Ctx, err error) error {
	var ae *apperr.Error
	if errors.As(err, &ae) {
		observability.Default.IncHTTPRequest(c.Route().Path, strconv.Itoa(ae.Status))
		return c.Status(ae.Status).JSON(fiber.Map{"error": errorBody(ae)})
	}

	var fe *fiber.Error
	if errors.As(err, &fe) {
		observability.Default.IncHTTPRequest(c.Route().Path, strconv.Itoa(fe.Code))
		return c.Status(fe.Code).JSON(fiber.Map{
			"error": fiber.Map{"code": "internal_error", "message": fe.Message},
		})
	}

	log.Error().Err(err).Str("path", c.Path()).Msg("unhandled error")
	observability.Default.IncHTTPRequest(c.Route().Path, strconv.Itoa(fiber.StatusInternalServerError))
	return c.Status(500).JSON(fiber.Map{
		"error": fiber.Map{"code": "internal_error", "message": "внутренняя ошибка сервера"},
	})
}
