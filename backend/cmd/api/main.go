package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"

	"habitus-backend/internal/app"
	"habitus-backend/internal/cian"
	"habitus-backend/internal/client"
	"habitus-backend/internal/config"
	"habitus-backend/internal/db"
	"habitus-backend/internal/observability"
	"habitus-backend/internal/repository"
	"habitus-backend/internal/service"
)

func main() {
	observability.InitLogger()
	cfg := config.Load()

	if err := db.RunMigrations(cfg.DBDSN, cfg.MigrationsPath); err != nil {
		log.Fatal().Err(err).Msg("migrations failed")
	}

	ctx, stopBackground := context.WithCancel(context.Background())
	defer stopBackground()

	pool, err := db.NewPool(ctx, cfg.DBDSN, cfg.DBMaxConns)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to postgres")
	}
	defer pool.Close()

	mlTimeout := time.Duration(cfg.MLSearchTimeoutS) * time.Second
	mlClient := client.NewMLClient(cfg.MLServiceURL, mlTimeout+10*time.Second)

	warmupTimeout := time.Duration(cfg.MLWarmupTimeoutS) * time.Second
	warmupClient := client.NewMLClient(cfg.MLServiceURL, warmupTimeout)
	warmCtx, cancelWarmup := context.WithTimeout(context.Background(), warmupTimeout)
	log.Info().Dur("timeout", warmupTimeout).Msg("warming up ML service before accepting traffic")
	if err := warmupClient.WarmUp(warmCtx); err != nil {
		cancelWarmup()
		log.Fatal().Err(err).Msg("ML warm-up failed; backend will not accept traffic")
	}
	cancelWarmup()
	log.Info().Msg("ML warm-up completed")

	userRepo := repository.NewUserRepo(pool)
	sessionRepo := repository.NewSessionRepo(pool)
	chatRepo := repository.NewChatRepo(pool)
	messageRepo := repository.NewMessageRepo(pool)
	chatSearchRepo := repository.NewChatSearchRepo(pool)
	listingRepo := repository.NewListingRepo(pool)
	poiRepo := repository.NewPOIRepo(pool)
	evidenceRepo := repository.NewEvidenceRepo(pool)
	metroRepo := repository.NewMetroRepo(pool)

	authService := service.NewAuthService(userRepo, sessionRepo)
	service.StartSessionSweeper(ctx, sessionRepo,
		time.Duration(cfg.SessionSweepMinutes)*time.Minute)
	service.StartGuestSweeper(ctx, userRepo,
		time.Duration(cfg.GuestSweepMinutes)*time.Minute,
		time.Duration(cfg.GuestRetentionDays)*24*time.Hour)
	chatService := service.NewChatService(chatRepo, messageRepo)
	dossierTimeout := time.Duration(cfg.MLDossierTimeoutS) * time.Second
	ownerRepo := repository.NewOwnerListingRepo(pool)
	objectService := service.NewObjectService(chatService, chatSearchRepo, listingRepo,
		ownerRepo, mlClient, dossierTimeout, cfg.DossierTTLHours)
	objectAskTimeout := time.Duration(cfg.MLObjectAskTimeoutS) * time.Second
	objectAskService := service.NewObjectAskService(chatSearchRepo, mlClient, objectAskTimeout)
	geoLayersService := service.NewGeoLayersService(poiRepo, evidenceRepo, listingRepo, metroRepo)
	explainTimeout := time.Duration(cfg.MLExplainTimeoutS) * time.Second
	streamService := service.NewSearchStreamService(chatRepo, messageRepo, chatSearchRepo, listingRepo, mlClient, mlTimeout, explainTimeout)
	resultsService := service.NewResultsService(chatService, chatSearchRepo, listingRepo)

	// Пробы readiness: обе зависимости, без которых шлюз бесполезен.
	readinessService := service.NewReadinessService(3*time.Second, map[string]service.Probe{
		"db": pool.Ping,
		"ml": mlClient.Health,
	})

	ownerListingService := service.NewOwnerListingService(
		ownerRepo,
		client.NewMLClient(cfg.MLServiceURL, time.Duration(cfg.MLOwnerTimeoutS)*time.Second),
		cfg.OwnerAutopublish,
	)

	// Сессия к Циану поднимается лениво, по первому импорту: на старте она не
	// нужна, а её создание ходит в сеть за cookie.
	offerFetcher := service.NewLazyOfferFetcher(cfg.CianProxies, cfg.CianRegion, mlTimeout)
	photoStore := service.NewPhotoStore(cfg.StaticDir,
		int64(cfg.OwnerPhotoMaxMB)<<20, cfg.OwnerPhotoMaxCount)
	ownerImportService := service.NewOwnerImportService(
		ownerRepo, listingRepo, offerFetcher,
		cian.NewRateLimiter(cfg.CianFetchPerMin, nil),
		cian.NewUserQuota(cfg.OwnerImportPerHour, nil),
	)

	leadRepo := repository.NewLeadRepo(pool)
	leadService := service.NewLeadService(ownerRepo, leadRepo)

	// Partner API (B2B). Собирается всегда — рубильник живёт в app.New:
	// сервисы без зарегистрированных маршрутов ничего не стоят, а два места
	// принятия одного решения разъезжаются.
	partnerRepo := repository.NewPartnerRepo(pool)
	partnerSearchRepo := repository.NewPartnerSearchRepo(pool)
	partnerWebhookRepo := repository.NewPartnerWebhookRepo(pool)

	partnerService := service.NewPartnerService(partnerRepo, userRepo)
	partnerWebhookService := service.NewPartnerWebhookService(
		partnerWebhookRepo, partnerRepo,
		time.Duration(cfg.PartnerWebhookTimeoutS)*time.Second,
		cfg.PartnerWebhookAllowInsecure,
	)
	if cfg.PartnerAPIEnabled {
		partnerWebhookService.StartDispatcher(ctx,
			time.Duration(cfg.PartnerWebhookPollSec)*time.Second, cfg.PartnerWebhookBatch)
		service.StartPartnerSweeper(ctx, partnerRepo, partnerSearchRepo,
			time.Duration(cfg.PartnerSweepMinutes)*time.Minute,
			time.Duration(cfg.PartnerIdempotencyTTLHours)*time.Hour,
			time.Duration(cfg.PartnerSearchTTLDays)*24*time.Hour)
	}
	// Заявка партнёру должна доехать в его CRM, а не ждать, пока кто-то
	// откроет кабинет: тот же LeadService, что обслуживает B2C, теперь ещё и
	// зовёт доставку событий.
	leadService = leadService.WithNotifier(partnerWebhookService)

	partnerSearchService := service.NewPartnerSearchService(
		partnerSearchRepo, listingRepo, ownerRepo, mlClient,
		mlTimeout, dossierTimeout, objectAskTimeout, cfg.DossierTTLHours,
	)

	favoriteService := service.NewFavoriteService(repository.NewFavoriteRepo(pool), listingRepo)

	feedbackService := service.NewFeedbackService(chatService, chatSearchRepo,
		repository.NewFeedbackRepo(pool))

	eventRecorder := service.NewEventRecorder(repository.NewEventRepo(pool), 1024)
	eventRecorder.Start(ctx)

	fiberApp := app.New(cfg, app.Services{
		Ready:     readinessService,
		Auth:      authService,
		Chat:      chatService,
		Stream:    streamService,
		Object:    objectService,
		ObjectAsk: objectAskService,
		GeoLayers: geoLayersService,
		Results:   resultsService,

		OwnerListings: ownerListingService,
		OwnerImports:  ownerImportService,
		OwnerPhotos:   photoStore,
		Leads:         leadService,
		Favorites:     favoriteService,
		Feedback:      feedbackService,
		Events:        eventRecorder,

		Partners:        partnerService,
		PartnerSearch:   partnerSearchService,
		PartnerWebhooks: partnerWebhookService,
		PartnerIdem:     partnerRepo,
	})

	go func() {
		log.Info().Str("port", cfg.HTTPPort).Msg("starting HTTP server")
		if err := fiberApp.Listen(":" + cfg.HTTPPort); err != nil {
			log.Fatal().Err(err).Msg("server stopped")
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := fiberApp.ShutdownWithContext(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("shutdown error")
	}
}
