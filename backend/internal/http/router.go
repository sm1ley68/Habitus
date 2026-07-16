// Package http wires routes to handlers. Named `http` to match the plan's
// package layout; callers import it as httpapi to avoid clashing with net/http.
package http

import (
	"github.com/gofiber/fiber/v2"

	"habitus-backend/internal/http/handlers"
	"habitus-backend/internal/http/middleware"
	"habitus-backend/internal/service"
)

type Handlers struct {
	Auth      *handlers.AuthHandler
	Chat      *handlers.ChatHandler
	Stream    *handlers.StreamHandler
	Object    *handlers.ObjectHandler
	ObjectAsk *handlers.ObjectAskHandler
	Geo       *handlers.GeoHandler
}

func RegisterRoutes(app *fiber.App, h Handlers, authSvc *service.AuthService) {
	app.Get("/health", handlers.Health)

	api := app.Group("/api/v1")

	api.Post("/auth/register", h.Auth.Register)
	api.Post("/auth/login", h.Auth.Login)

	authMw := middleware.Auth(authSvc)

	api.Post("/auth/logout", authMw, h.Auth.Logout)
	api.Get("/me", authMw, h.Auth.Me)

	api.Post("/chats", authMw, h.Chat.Create)
	api.Get("/chats", authMw, h.Chat.List)
	api.Patch("/chats/:chat_id", authMw, h.Chat.Rename)
	api.Delete("/chats/:chat_id", authMw, h.Chat.Delete)
	api.Get("/chats/:chat_id/messages", authMw, h.Chat.Messages)
	api.Post("/chats/:chat_id/messages/stream", authMw, h.Stream.PostMessagesStream)

	api.Get("/objects/:object_id", authMw, h.Object.Get)
	api.Post("/objects/:object_id/ask/stream", authMw, h.ObjectAsk.PostStream)

	api.Get("/geo/layers", authMw, h.Geo.Layers)
}
