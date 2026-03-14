// sentiric-workflow-service/internal/app/app.go
package app

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/sentiric/sentiric-workflow-service/internal/client"
	"github.com/sentiric/sentiric-workflow-service/internal/config"
	"github.com/sentiric/sentiric-workflow-service/internal/database"
	"github.com/sentiric/sentiric-workflow-service/internal/engine"
	"github.com/sentiric/sentiric-workflow-service/internal/event"
	"github.com/sentiric/sentiric-workflow-service/internal/repository"
)

func Run(cfg *config.Config, log zerolog.Logger) {
	pgPool, err := database.NewPostgresConnection(cfg.PostgresURL, log)
	if err != nil {
		log.Fatal().Err(err).Msg("Postgres connection failed")
	}
	defer pgPool.Close()

	redisClient, err := database.NewRedisClient(cfg.RedisURL, log)
	if err != nil {
		log.Fatal().Err(err).Msg("Redis connection failed")
	}

	clients, err := client.NewClients(cfg, log)
	if err != nil {
		log.Fatal().Err(err).Msg("gRPC Clients init failed")
	}
	defer clients.Close()

	// [DÜZELTME]: Repository artık Redis client'ı da alıyor.
	repo := repository.NewWorkflowRepository(pgPool, redisClient.Client, log)

	// Processor'a repo iletiliyor
	processor := engine.NewProcessor(redisClient.Client, repo, clients, cfg.RabbitMQURL, log)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	consumer := event.NewConsumer(processor, repo, log)

	if err := consumer.Start(ctx, cfg.RabbitMQURL, &wg); err != nil {
		log.Fatal().Err(err).Msg("RabbitMQ Consumer başlatılamadı")
	}

	log.Info().Msg("✅ Workflow Service Çalışıyor (DB Integrated). Olay bekleniyor...")

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Warn().Msg("Kapatma sinyali alındı. Servis durduruluyor...")
	cancel()
	wg.Wait()
	log.Info().Msg("Servis güvenle kapatıldı.")
}
