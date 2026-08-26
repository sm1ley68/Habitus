package middleware

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/service"
)

const SessionCookieName = "habitus_session"
const UserIDLocalsKey = "user_id"
const IsGuestLocalsKey = "is_guest"

// Auth reads the session cookie (see plan §7 — real cookie-session, chosen
// over Authorization: Bearer specifically because the browser EventSource
// used for SSE can't send custom headers) and stores the authenticated
// user_id in fiber.Locals for downstream handlers.
//
// Признак гостя кладётся рядом и берётся тем же запросом, что и user_id:
// RequireRegistered и рейт-лимит спрашивают его на каждом запросе, и второй
// поход в БД ради одного булева поля тут не оправдан.
func Auth(auth *service.AuthService) fiber.Handler {
	return func(c *fiber.Ctx) error {
		token := c.Cookies(SessionCookieName)
		if token == "" {
			return apperr.Unauthorized()
		}
		userID, isGuest, err := auth.AuthenticateSession(c.Context(), token)
		if err != nil {
			return err
		}
		c.Locals(UserIDLocalsKey, userID)
		c.Locals(IsGuestLocalsKey, isGuest)
		return c.Next()
	}
}

func UserID(c *fiber.Ctx) uuid.UUID {
	id, _ := c.Locals(UserIDLocalsKey).(uuid.UUID)
	return id
}

// IsGuest — аккаунт без учётных данных. Отсутствие значения трактуется как
// «не гость»: ручки вне authMw про гостей ничего не знают и не должны
// внезапно отказывать.
func IsGuest(c *fiber.Ctx) bool {
	v, _ := c.Locals(IsGuestLocalsKey).(bool)
	return v
}
