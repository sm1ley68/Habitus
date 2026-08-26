// Package http wires routes to handlers. Named `http` to match the plan's
// package layout; callers import it as httpapi to avoid clashing with net/http.
package http

import (
	"github.com/gofiber/fiber/v2"

	"habitus-backend/internal/http/handlers"
	"habitus-backend/internal/http/middleware"
	"habitus-backend/internal/observability"
	"habitus-backend/internal/service"
)

type Handlers struct {
	Health    *handlers.HealthHandler
	Auth      *handlers.AuthHandler
	Chat      *handlers.ChatHandler
	Stream    *handlers.StreamHandler
	Object    *handlers.ObjectHandler
	ObjectAsk *handlers.ObjectAskHandler
	Geo       *handlers.GeoHandler
	Results   *handlers.ResultsHandler
	Owner     *handlers.OwnerHandler
	Lead      *handlers.LeadHandler
	Favorite  *handlers.FavoriteHandler
}

// rateLimitLLM применяется только к двум ручкам, которые реально жгут бюджет
// LLM (messages/stream, ask/stream) — остальной API рейт-лимит не трогает
// (прямое требование брифа Task 8).
func RegisterRoutes(app *fiber.App, h Handlers, authSvc *service.AuthService, rateLimitLLM fiber.Handler) {
	app.Get("/health", h.Health.Live)
	app.Get("/health/ready", h.Health.Ready)
	app.Get("/metrics", observability.MetricsHandler(observability.Default))

	api := app.Group("/api/v1")

	api.Post("/auth/register", h.Auth.Register)
	api.Post("/auth/login", h.Auth.Login)
	// Гостевая сессия: первый поиск без регистрации. Стена перед первым
	// поиском стояла ровно там, где у продукта единственный шанс показать
	// ценность, — поэтому её здесь нет.
	api.Post("/auth/guest", h.Auth.Guest)

	authMw := middleware.Auth(authSvc)

	api.Post("/auth/logout", authMw, h.Auth.Logout)
	api.Get("/me", authMw, h.Auth.Me)

	api.Post("/chats", authMw, h.Chat.Create)
	api.Get("/chats", authMw, h.Chat.List)
	api.Patch("/chats/:chat_id", authMw, h.Chat.Rename)
	api.Delete("/chats/:chat_id", authMw, h.Chat.Delete)
	api.Get("/chats/:chat_id/messages", authMw, h.Chat.Messages)
	api.Post("/chats/:chat_id/messages/stream", authMw, rateLimitLLM, h.Stream.PostMessagesStream)
	api.Get("/chats/:chat_id/results", authMw, h.Results.List)

	api.Get("/objects/:object_id", authMw, h.Object.Get)
	api.Post("/objects/:object_id/ask/stream", authMw, rateLimitLLM, h.ObjectAsk.PostStream)
	// Без RequireRegistered: гостя здесь не отвергают, а регистрируют тем же
	// запросом — решение принимает сам хендлер.
	api.Post("/objects/:object_id/lead", authMw, h.Lead.Send)

	api.Get("/geo/layers", authMw, h.Geo.Layers)
	api.Get("/geo/listings", authMw, h.Geo.Listings)

	// Избранное доступно и гостю: сохранённое переживёт регистрацию — id
	// пользователя при апгрейде не меняется.
	api.Get("/favorites", authMw, h.Favorite.List)
	api.Put("/favorites/:object_id", authMw, h.Favorite.Add)
	api.Delete("/favorites/:object_id", authMw, h.Favorite.Remove)

	// Личный кабинет продавца. Всё за authMw: объявление всегда принадлежит
	// конкретному пользователю, анонимного доступа здесь нет по определению.
	// Кабинет закрыт для гостей: объявление должно принадлежать аккаунту,
	// который переживёт чистку брошенных гостей.
	// Порядок важен: /listings/import объявляется до /listings/:listing_id,
	// иначе Fiber примет import за uuid и вернёт 404.
	ownerGroup := api.Group("/owner", authMw, middleware.RequireRegistered())
	// /leads — до /listings/:listing_id: иначе Fiber примет "leads" за uuid.
	ownerGroup.Get("/leads", h.Lead.List)
	ownerGroup.Get("/listings", h.Owner.List)
	ownerGroup.Post("/listings", h.Owner.Create)
	ownerGroup.Post("/listings/import/preview", h.Owner.ImportPreview)
	ownerGroup.Post("/listings/import", h.Owner.Import)
	ownerGroup.Get("/listings/:listing_id", h.Owner.Get)
	ownerGroup.Patch("/listings/:listing_id", h.Owner.Update)
	ownerGroup.Delete("/listings/:listing_id", h.Owner.Delete)
	ownerGroup.Post("/listings/:listing_id/publish", h.Owner.Publish)
	ownerGroup.Post("/listings/:listing_id/unpublish", h.Owner.Unpublish)
	ownerGroup.Post("/listings/:listing_id/photos", h.Owner.UploadPhotos)
	ownerGroup.Delete("/listings/:listing_id/photos", h.Owner.DeletePhoto)
}
