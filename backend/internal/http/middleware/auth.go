package middleware

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/service"
)

const SessionCookieName = "habitus_session"
const UserIDLocalsKey = "user_id"

// Auth reads the session cookie (see plan §7 — real cookie-session, chosen
// over Authorization: Bearer specifically because the browser EventSource
// used for SSE can't send custom headers) and stores the authenticated
// user_id in fiber.Locals for downstream handlers.
func Auth(auth *service.AuthService) fiber.Handler {
	return func(c *fiber.Ctx) error {
		token := c.Cookies(SessionCookieName)
		if token == "" {
			return apperr.Unauthorized()
		}
		userID, err := auth.Authenticate(c.Context(), token)
		if err != nil {
			return err
		}
		c.Locals(UserIDLocalsKey, userID)
		return c.Next()
	}
}

func UserID(c *fiber.Ctx) uuid.UUID {
	id, _ := c.Locals(UserIDLocalsKey).(uuid.UUID)
	return id
}
