package middleware

import (
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"

	"habitus-backend/internal/apperr"
)

// ErrorHandler is the single place that turns any error returned by a
// handler into the unified {"error":{"code","message"}} envelope from
// frontend/Пайплайн фронт.md §1.
func ErrorHandler(c *fiber.Ctx, err error) error {
	var ae *apperr.Error
	if errors.As(err, &ae) {
		return c.Status(ae.Status).JSON(fiber.Map{
			"error": fiber.Map{"code": ae.Code, "message": ae.Message},
		})
	}

	var fe *fiber.Error
	if errors.As(err, &fe) {
		return c.Status(fe.Code).JSON(fiber.Map{
			"error": fiber.Map{"code": "internal_error", "message": fe.Message},
		})
	}

	log.Error().Err(err).Str("path", c.Path()).Msg("unhandled error")
	return c.Status(500).JSON(fiber.Map{
		"error": fiber.Map{"code": "internal_error", "message": "внутренняя ошибка сервера"},
	})
}
