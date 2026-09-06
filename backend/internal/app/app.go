// Package app assembles the Fiber application: middleware chain + routes.
package app

import (
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/fiber/v2/middleware/requestid"

	"habitus-backend/internal/apidocs"
	"habitus-backend/internal/config"
	httpapi "habitus-backend/internal/http"
	"habitus-backend/internal/http/handlers"
	"habitus-backend/internal/http/middleware"
	"habitus-backend/internal/observability"
	"habitus-backend/internal/service"
)

type Services struct {
	Ready     *service.ReadinessService
	Auth      *service.AuthService
	Chat      *service.ChatService
	Stream    *service.SearchStreamService
	Object    *service.ObjectService
	ObjectAsk *service.ObjectAskService
	GeoLayers *service.GeoLayersService
	Results   *service.ResultsService
	// Личный кабинет продавца: управление карточками, импорт с Циана и
	// хранилище загруженных фотографий.
	OwnerListings *service.OwnerListingService
	OwnerImports  *service.OwnerImportService
	OwnerPhotos   *service.PhotoStore
	// Leads — заявки покупателей продавцам: единственная точка, где гость
	// заводит аккаунт тем же запросом, а не идёт отдельно на регистрацию.
	Leads *service.LeadService
	// Favorites — сохранённые объекты, переживают чат и доступны гостю.
	Favorites *service.FavoriteService
	// Feedback — оценка объекта в выдаче, единственный продакшн-сигнал о
	// качестве подбора.
	Feedback *service.FeedbackService
	// Events может быть nil — телеметрия выключена (так собраны тесты,
	// строящие app.Services{} напрямую).
	Events *service.EventRecorder

	// --- Partner API (B2B) ---
	// Любое из этих полей nil означает «B2B-контур не сконфигурирован»:
	// маршруты /partner/v1 тогда не регистрируются вовсе. Тот же приём
	// fallback'а, что и у остальных полей, — тесты собирают Services{}
	// напрямую и о партнёрах ничего не знают.
	Partners        *service.PartnerService
	PartnerSearch   *service.PartnerSearchService
	PartnerWebhooks *service.PartnerWebhookService
	PartnerIdem     middleware.IdempotencyStore
}

// Границы HTTP-слоя. ReadTimeout не режет SSE (он про чтение запроса, а не
// ответа), поэтому WriteTimeout здесь намеренно НЕ задан: он оборвал бы
// живой поиск на середине потока. Бюджет ответа держит контекст стрима.
const (
	readTimeout         = 15 * time.Second
	idleTimeout         = 75 * time.Second
	bodyLimitDef        = 1 << 20
	rateLimitPerHourDef = 30
	// rateLimitGuestPerHourDef — дефолт гостевого потолка LLM, срабатывает
	// тем же приёмом fallback'а, что и rateLimitPerHourDef.
	rateLimitGuestPerHourDef = 5
	// readyTimeout — сколько ждём зависимости в readiness. Заметно меньше
	// интервала healthcheck'а в compose (10 с), чтобы проба не наслаивалась
	// сама на себя.
	readyTimeout = 3 * time.Second
	// Дефолты лимитов Partner API — тот же приём fallback'а, что у
	// rateLimitPerHourDef: неположительное значение в конфиге означает
	// «не задано», а не «ничего не пропускать».
	partnerRPMDef        = 120
	partnerLLMPerHourDef = 60
)

// uploadBodyLimit — сколько байт нужно на одну загрузку фотографий объявления.
// Ноль, когда кабинет не сконфигурирован (например, в тестах, собирающих
// config.Settings{} напрямую): тогда предел остаётся прежним.
func uploadBodyLimit(cfg config.Settings) int {
	if cfg.OwnerPhotoMaxMB <= 0 || cfg.OwnerPhotoMaxCount <= 0 {
		return 0
	}
	return (cfg.OwnerPhotoMaxMB*cfg.OwnerPhotoMaxCount + 1) << 20
}

func New(cfg config.Settings, svc Services) *fiber.App {
	bodyLimit := cfg.BodyLimitBytes
	if bodyLimit <= 0 {
		bodyLimit = bodyLimitDef
	}
	// Предел тела определяется загрузкой фотографий, а не JSON: самый жирный
	// JSON-запрос шлюза — сотни килобайт, а один снимок с телефона легко
	// перекрывает дефолтный мегабайт. Берём максимум из явного BODY_LIMIT_BYTES
	// и того, что нужно на полную пачку фото объявления (+1 МБ на границы
	// multipart и служебные поля формы).
	if photoLimit := uploadBodyLimit(cfg); photoLimit > bodyLimit {
		bodyLimit = photoLimit
	}
	app := fiber.New(fiber.Config{
		ErrorHandler: middleware.ErrorHandler,
		BodyLimit:    bodyLimit,
		ReadTimeout:  readTimeout,
		IdleTimeout:  idleTimeout,
		// Адрес клиента берётся из заголовка ТОЛЬКО когда запрос пришёл от
		// доверенного прокси. Иначе allowlist ключа обходится подделкой
		// X-Forwarded-For, а без ProxyHeader за балансировщиком он проверял
		// бы адрес самого балансировщика — то есть не проверял бы ничего.
		ProxyHeader:             cfg.ProxyHeader,
		EnableTrustedProxyCheck: len(cfg.TrustedProxies) > 0,
		TrustedProxies:          cfg.TrustedProxies,
	})

	app.Use(requestid.New())
	app.Use(recover.New())
	app.Use(cors.New(cors.Config{
		AllowOrigins:     cfg.CORSAllowedOrigin,
		AllowCredentials: true,
		AllowHeaders:     "Content-Type",
		AllowMethods:     "GET,POST,PUT,PATCH,DELETE,OPTIONS",
	}))
	// habitus_http_requests_total (Task 8): считает завершившиеся без ошибки
	// ответы; ошибочные (429/404/500/…) считает middleware.ErrorHandler — там
	// известен итоговый статус, см. комментарий в observability/http_metrics.go.
	app.Use(observability.HTTPRequestsMiddleware(observability.Default))

	app.Static("/static", cfg.StaticDir)

	// Документация для разработчиков. Отдаётся тем же процессом, что и API:
	// страница, которую можно задеплоить отдельно от кода, однажды разойдётся
	// с ним — и разработчик партнёра узнает об этом на боевой интеграции.
	docsHandler := func(c *fiber.Ctx) error {
		c.Set(fiber.HeaderContentType, fiber.MIMETextHTMLCharsetUTF8)
		return c.Send(apidocs.DocsHTML())
	}
	app.Get("/docs", docsHandler)
	app.Get("/docs/", docsHandler)

	// RateLimitLLMPerHour <= 0 — конфиг не задан (например, тест собирает
	// config.Settings{} напрямую, минуя config.Load()) — тот же приём
	// fallback'а, что у bodyLimit выше.
	rateLimitPerHour := cfg.RateLimitLLMPerHour
	if rateLimitPerHour <= 0 {
		rateLimitPerHour = rateLimitPerHourDef
	}
	// Гостевой потолок: тот же приём fallback'а. Ноль/отрицательное значение
	// в конфиге означает «конфиг не задан» (например, тест собирает
	// config.Settings{} напрямую) — берём дефолт, а не «ничего не пропускать».
	guestPerHour := cfg.RateLimitLLMGuestPerHour
	if guestPerHour <= 0 {
		guestPerHour = rateLimitGuestPerHourDef
	}
	rateLimiter := middleware.NewRateLimiter(rateLimitPerHour, time.Hour)
	guestLimiter := middleware.NewRateLimiter(guestPerHour, time.Hour)

	// svc.Ready == nil — конфиг не задан (например, app_test.go собирает
	// app.Services{} напрямую, минуя main.go) — тот же приём fallback'а, что у
	// bodyLimit и rateLimitPerHour выше: пустой сервис вместо паники на nil.
	ready := svc.Ready
	if ready == nil {
		ready = service.NewReadinessService(readyTimeout, nil)
	}

	httpapi.RegisterRoutes(app, httpapi.Handlers{
		Health:    handlers.NewHealthHandler(ready),
		Auth:      handlers.NewAuthHandler(svc.Auth, cfg.SessionCookieSecure, svc.Events),
		Chat:      handlers.NewChatHandler(svc.Chat),
		Stream:    handlers.NewStreamHandler(svc.Chat, svc.Stream, svc.Events),
		Object:    handlers.NewObjectHandler(svc.Object, svc.Events),
		ObjectAsk: handlers.NewObjectAskHandler(svc.Object, svc.ObjectAsk),
		Geo:       handlers.NewGeoHandler(svc.GeoLayers),
		Results:   handlers.NewResultsHandler(svc.Results),
		Owner:     handlers.NewOwnerHandler(svc.OwnerListings, svc.OwnerImports, svc.OwnerPhotos),
		Lead:      handlers.NewLeadHandler(svc.Leads, svc.Auth, cfg.SessionCookieSecure, svc.Events),
		Favorite:  handlers.NewFavoriteHandler(svc.Favorites, svc.Events),
		Feedback:  handlers.NewFeedbackHandler(svc.Feedback, svc.Events),
	}, svc.Auth, middleware.RateLimitLLM(rateLimiter, guestLimiter))

	registerPartnerAPI(app, cfg, svc, ready)

	return app
}

// registerPartnerAPI поднимает B2B-контур, когда он сконфигурирован. Молча
// пропустить его при неполной проводке — сознательный выбор: тесты собирают
// app.Services{} напрямую, и падать на nil из-за контура, которого они не
// касаются, приложение не должно.
func registerPartnerAPI(app *fiber.App, cfg config.Settings, svc Services,
	ready *service.ReadinessService) {
	if !cfg.PartnerAPIEnabled || svc.Partners == nil || svc.PartnerSearch == nil ||
		svc.PartnerIdem == nil {
		return
	}
	rpm := cfg.PartnerRateLimitPerMin
	if rpm <= 0 {
		rpm = partnerRPMDef
	}
	llm := cfg.PartnerLLMPerHour
	if llm <= 0 {
		llm = partnerLLMPerHourDef
	}
	quotas := middleware.NewPartnerQuotas(rpm, llm)

	docsURL := strings.TrimRight(cfg.PublicBaseURL, "/") + "/docs"
	httpapi.RegisterPartnerRoutes(app, httpapi.PartnerHandlers{
		Meta: handlers.NewPartnerMetaHandler(quotas, ready, apidocs.OpenAPI(),
			httpapi.PartnerAPIVersion),
		Search: handlers.NewPartnerSearchHandler(svc.PartnerSearch),
		Geo:    handlers.NewPartnerGeoHandler(svc.GeoLayers),
		Inventory: handlers.NewPartnerInventoryHandler(svc.OwnerListings, svc.Leads,
			svc.PartnerWebhooks),
		Webhooks: handlers.NewPartnerWebhookHandler(svc.PartnerWebhooks),
	}, httpapi.PartnerDeps{
		Auth: svc.Partners, Quotas: quotas, Idempotency: svc.PartnerIdem,
		DocsURL: docsURL,
	})
}
