package http

import (
	"github.com/gofiber/fiber/v2"

	"habitus-backend/internal/http/handlers"
	"habitus-backend/internal/http/middleware"
	"habitus-backend/internal/service"
)

// PartnerAPIVersion — версия контракта в пути. Ломающее изменение поедет
// как /partner/v2, а не как правка v1: у интеграции нет возможности
// «обновиться вместе с нами», её релизный цикл живёт отдельно.
const PartnerAPIVersion = "2026-09-06"

type PartnerHandlers struct {
	Meta      *handlers.PartnerMetaHandler
	Search    *handlers.PartnerSearchHandler
	Geo       *handlers.PartnerGeoHandler
	Inventory *handlers.PartnerInventoryHandler
	Webhooks  *handlers.PartnerWebhookHandler
}

// PartnerDeps — сквозные зависимости группы: аутентификация по ключу, лимиты
// и хранилище идемпотентности.
type PartnerDeps struct {
	Auth        *service.PartnerService
	Quotas      *middleware.PartnerQuotas
	Idempotency middleware.IdempotencyStore
	DocsURL     string
}

// RegisterPartnerRoutes поднимает публичный B2B-контур на /partner/v1.
//
// Отдельная группа, а не расширение /api/v1: у контуров разная
// аутентификация (ключ против cookie-сессии), разный конверт ошибки, разные
// лимиты и разные обещания по совместимости. Смешать их — значит однажды
// сломать интеграцию партнёра правкой, сделанной ради фронта.
func RegisterPartnerRoutes(app *fiber.App, h PartnerHandlers, deps PartnerDeps) {
	v1 := app.Group("/partner/v1", middleware.PartnerErrors(deps.DocsURL))

	// Публичное: контракт и состояние сервиса. Без ключа намеренно —
	// разработчик должен уметь прочитать спеку до того, как получит доступ,
	// а «мой ключ протух» отличить от «у них лежит поиск» — не имея ключа.
	v1.Get("/openapi.json", h.Meta.OpenAPI)
	v1.Get("/status", h.Meta.Status)

	auth := middleware.PartnerAuth(deps.Auth)
	rate := deps.Quotas.PartnerRateLimit()
	// llm — второй, более скупой потолок для ручек, за каждой из которых стоит
	// вызов модели. Ставится после общего: сначала грубый фильтр, потом дорогой.
	llm := deps.Quotas.PartnerLLMLimit()
	idem := middleware.Idempotency(deps.Idempotency)

	api := v1.Group("", auth, rate)

	api.Get("/me", h.Meta.Me)

	search := middleware.RequireScope(service.ScopeSearchRead)
	api.Post("/search", search, llm, idem, h.Search.Create)
	api.Get("/searches/:search_id", search, h.Search.Get)
	api.Get("/searches/:search_id/results", search, h.Search.Results)
	// Досье считается лениво и стоит вызова модели — отсюда квота LLM. Сам
	// объект вне подбора (/properties/:id) её не тратит: это чтение витрины.
	api.Get("/searches/:search_id/properties/:object_id", search, llm, h.Search.Dossier)
	api.Post("/searches/:search_id/properties/:object_id/ask", search, llm, idem, h.Search.Ask)
	api.Get("/properties/:object_id", search, h.Search.Object)

	geo := middleware.RequireScope(service.ScopeGeoRead)
	api.Get("/geo/layers", geo, h.Geo.Layers)
	api.Get("/geo/listings", geo, h.Geo.Listings)

	// Порядок важен: статические сегменты объявляются до :listing_id, иначе
	// fiber примет их за идентификатор.
	listingsRead := middleware.RequireScope(service.ScopeListingsRead)
	listingsWrite := middleware.RequireScope(service.ScopeListingsWrite)
	api.Get("/listings", listingsRead, h.Inventory.ListListings)
	api.Post("/listings", listingsWrite, idem, h.Inventory.CreateListing)
	api.Get("/listings/:listing_id", listingsRead, h.Inventory.GetListing)
	api.Patch("/listings/:listing_id", listingsWrite, h.Inventory.UpdateListing)
	api.Delete("/listings/:listing_id", listingsWrite, h.Inventory.DeleteListing)
	api.Post("/listings/:listing_id/publish", listingsWrite, h.Inventory.PublishListing)
	api.Post("/listings/:listing_id/unpublish", listingsWrite, h.Inventory.UnpublishListing)

	api.Get("/leads", middleware.RequireScope(service.ScopeLeadsRead), h.Inventory.ListLeads)

	hooks := middleware.RequireScope(service.ScopeWebhooksWrite)
	api.Get("/webhooks", hooks, h.Webhooks.List)
	api.Post("/webhooks", hooks, idem, h.Webhooks.Create)
	api.Get("/webhooks/:webhook_id", hooks, h.Webhooks.Get)
	api.Delete("/webhooks/:webhook_id", hooks, h.Webhooks.Delete)
	api.Post("/webhooks/:webhook_id/test", hooks, h.Webhooks.Test)
	api.Get("/webhooks/:webhook_id/deliveries", hooks, h.Webhooks.Deliveries)
}
