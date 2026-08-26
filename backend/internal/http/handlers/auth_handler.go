package handlers

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/domain"
	"habitus-backend/internal/http/middleware"
	"habitus-backend/internal/service"
)

type AuthHandler struct {
	auth         *service.AuthService
	cookieSecure bool
}

func NewAuthHandler(auth *service.AuthService, cookieSecure bool) *AuthHandler {
	return &AuthHandler{auth: auth, cookieSecure: cookieSecure}
}

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *AuthHandler) setSessionCookie(c *fiber.Ctx, token string, expiresAt time.Time) {
	c.Cookie(&fiber.Cookie{
		Name:     middleware.SessionCookieName,
		Value:    token,
		Expires:  expiresAt,
		HTTPOnly: true,
		Secure:   h.cookieSecure,
		SameSite: "Lax",
		Path:     "/",
	})
}

// userResponseBody — общая форма ответа auth-ручек. is_guest отдаётся всегда,
// а не только гостю: фронту нужно знать тип аккаунта на каждом входе, чтобы
// решать, показывать ли призыв зарегистрироваться.
func userResponseBody(id any, email, name string, isGuest bool) fiber.Map {
	return fiber.Map{"id": id, "email": email, "name": name, "is_guest": isGuest}
}

func guestResponseBody(id any, name string) fiber.Map {
	return userResponseBody(id, "", name, true)
}

// Guest implements POST /auth/guest — сессия без регистрации под первый поиск.
// Если сессия уже есть и она живая, новый гость НЕ заводится: иначе перезагрузка
// вкладки плодила бы пользователей и теряла историю поиска.
func (h *AuthHandler) Guest(c *fiber.Ctx) error {
	if token := c.Cookies(middleware.SessionCookieName); token != "" {
		if u, err := h.auth.SessionUser(c.Context(), token); err == nil {
			return c.JSON(userResponseBody(u.ID, u.Email, u.Name, u.IsGuest))
		}
	}
	u, token, expiresAt, err := h.auth.Guest(c.Context())
	if err != nil {
		return err
	}
	h.setSessionCookie(c, token, expiresAt)
	return c.Status(fiber.StatusCreated).JSON(guestResponseBody(u.ID, u.Name))
}

func (h *AuthHandler) Register(c *fiber.Ctx) error {
	var req registerRequest
	if err := c.BodyParser(&req); err != nil || req.Email == "" || req.Password == "" {
		return apperr.Validation("email и password обязательны")
	}

	// Регистрация из-под гостевой сессии — это АПГРЕЙД той же строки users,
	// а не новый пользователь: иначе всё, что человек успел найти и сохранить
	// до регистрации, осталось бы на брошенном аккаунте.
	var guestID uuid.UUID
	if token := c.Cookies(middleware.SessionCookieName); token != "" {
		if u, err := h.auth.SessionUser(c.Context(), token); err == nil && u.IsGuest {
			guestID = u.ID
		}
	}

	var (
		u         domain.User
		token     string
		expiresAt time.Time
		err       error
	)
	if guestID != uuid.Nil {
		u, token, expiresAt, err = h.auth.UpgradeGuest(c.Context(), guestID, req.Email, req.Password, req.Name)
	} else {
		u, token, expiresAt, err = h.auth.Register(c.Context(), req.Email, req.Password, req.Name)
	}
	if err != nil {
		return err
	}
	h.setSessionCookie(c, token, expiresAt)
	return c.Status(fiber.StatusCreated).JSON(userResponseBody(u.ID, u.Email, u.Name, false))
}

func (h *AuthHandler) Login(c *fiber.Ctx) error {
	var req loginRequest
	if err := c.BodyParser(&req); err != nil || req.Email == "" || req.Password == "" {
		return apperr.Validation("email и password обязательны")
	}
	u, token, expiresAt, err := h.auth.Login(c.Context(), req.Email, req.Password)
	if err != nil {
		return err
	}
	h.setSessionCookie(c, token, expiresAt)
	return c.JSON(userResponseBody(u.ID, u.Email, u.Name, u.IsGuest))
}

func (h *AuthHandler) Logout(c *fiber.Ctx) error {
	token := c.Cookies(middleware.SessionCookieName)
	if token != "" {
		_ = h.auth.Logout(c.Context(), token)
	}
	c.Cookie(&fiber.Cookie{
		Name: middleware.SessionCookieName, Value: "", Expires: time.Unix(0, 0),
		HTTPOnly: true, Secure: h.cookieSecure, SameSite: "Lax", Path: "/",
	})
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *AuthHandler) Me(c *fiber.Ctx) error {
	userID := middleware.UserID(c)
	u, err := h.auth.GetUser(c.Context(), userID)
	if err != nil {
		return apperr.Unauthorized()
	}
	return c.JSON(userResponseBody(u.ID, u.Email, u.Name, u.IsGuest))
}
