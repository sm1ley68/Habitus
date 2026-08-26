package middleware

import (
	"github.com/gofiber/fiber/v2"

	"habitus-backend/internal/apperr"
)

// RequireRegistered закрывает ручки, где анонимный пользователь бессмыслен:
// кабинет продавца — объявление должно кому-то принадлежать. Ставится ПОСЛЕ
// Auth: тот кладёт признак гостя в Locals.
//
// На заявке этого middleware НЕТ намеренно: там гостю не отказывают, а заводят
// аккаунт тем же запросом (см. LeadHandler.Send) — отдельный редирект на
// регистрацию терял бы заполненную форму.
func RequireRegistered() fiber.Handler {
	return func(c *fiber.Ctx) error {
		if IsGuest(c) {
			return apperr.GuestForbidden(
				"Зарегистрируйтесь, чтобы продолжить — сохранённые поиски останутся при вас")
		}
		return c.Next()
	}
}
