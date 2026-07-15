package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"

	"habitus-backend/internal/app"
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

	ctx := context.Background()
	pool, err := db.NewPool(ctx, cfg.DBDSN)
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

	authService := service.NewAuthService(userRepo, sessionRepo)
	chatService := service.NewChatService(chatRepo, messageRepo)
	objectService := service.NewObjectService(chatService, chatSearchRepo, listingRepo)
	geoLayersService := service.NewGeoLayersService(poiRepo)
	streamService := service.NewSearchStreamService(chatRepo, messageRepo, chatSearchRepo, listingRepo, mlClient, mlTimeout)

	fiberApp := app.New(cfg, app.Services{
		Auth:      authService,
		Chat:      chatService,
		Stream:    streamService,
		Object:    objectService,
		GeoLayers: geoLayersService,
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
