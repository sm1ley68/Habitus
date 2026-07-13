package handlers

import (
	"time"

	"github.com/gofiber/fiber/v2"

	"habitus-backend/internal/apperr"
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

func (h *AuthHandler) Register(c *fiber.Ctx) error {
	var req registerRequest
	if err := c.BodyParser(&req); err != nil || req.Email == "" || req.Password == "" {
		return apperr.Validation("email и password обязательны")
	}
	u, token, expiresAt, err := h.auth.Register(c.Context(), req.Email, req.Password, req.Name)
	if err != nil {
		return err
	}
	h.setSessionCookie(c, token, expiresAt)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"id": u.ID, "email": u.Email, "name": u.Name,
	})
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
	return c.JSON(fiber.Map{"id": u.ID, "email": u.Email, "name": u.Name})
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
	return c.JSON(fiber.Map{"id": u.ID, "email": u.Email, "name": u.Name})
}
