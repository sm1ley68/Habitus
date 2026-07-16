// Package app assembles the Fiber application: middleware chain + routes.
package app

import (
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/fiber/v2/middleware/requestid"

	"habitus-backend/internal/config"
	httpapi "habitus-backend/internal/http"
	"habitus-backend/internal/http/handlers"
	"habitus-backend/internal/http/middleware"
	"habitus-backend/internal/service"
)

type Services struct {
	Auth      *service.AuthService
	Chat      *service.ChatService
	Stream    *service.SearchStreamService
	Object    *service.ObjectService
	ObjectAsk *service.ObjectAskService
	GeoLayers *service.GeoLayersService
}

func New(cfg config.Settings, svc Services) *fiber.App {
	app := fiber.New(fiber.Config{
		ErrorHandler: middleware.ErrorHandler,
	})

	app.Use(requestid.New())
	app.Use(recover.New())
	app.Use(cors.New(cors.Config{
		AllowOrigins:     cfg.CORSAllowedOrigin,
		AllowCredentials: true,
		AllowHeaders:     "Content-Type",
		AllowMethods:     "GET,POST,PATCH,DELETE,OPTIONS",
	}))

	app.Static("/static", cfg.StaticDir)

	httpapi.RegisterRoutes(app, httpapi.Handlers{
		Auth:      handlers.NewAuthHandler(svc.Auth, cfg.SessionCookieSecure),
		Chat:      handlers.NewChatHandler(svc.Chat),
		Stream:    handlers.NewStreamHandler(svc.Chat, svc.Stream),
		Object:    handlers.NewObjectHandler(svc.Object),
		ObjectAsk: handlers.NewObjectAskHandler(svc.Object, svc.ObjectAsk),
		Geo:       handlers.NewGeoHandler(svc.GeoLayers),
	}, svc.Auth)

	return app
}
