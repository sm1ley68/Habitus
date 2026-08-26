package handlers

import (
	"time"

	"github.com/gofiber/fiber/v2"

	"habitus-backend/internal/http/middleware"
)

// setSessionCookie — единственное место, где задаются параметры сессионной
// куки. Ставит её и вход, и регистрация, и заявка гостя: разъехавшиеся
// SameSite или Path у разных ручек означали бы, что часть сессий молча теряется.
func setSessionCookie(c *fiber.Ctx, token string, expiresAt time.Time, secure bool) {
	c.Cookie(&fiber.Cookie{
		Name:     middleware.SessionCookieName,
		Value:    token,
		Expires:  expiresAt,
		HTTPOnly: true,
		Secure:   secure,
		SameSite: "Lax",
		Path:     "/",
	})
}
