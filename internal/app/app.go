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
)

func Run(cfg *config.Config, log zerolog.Logger) {
	// 1. Veritabanı Bağlantıları
	pgPool, err := database.NewPostgresConnection(cfg.PostgresURL, log)
	if err != nil {
		log.Fatal().Err(err).Msg("Postgres connection failed")
	}
	defer pgPool.Close()

	redisClient, err := database.NewRedisClient(cfg.RedisURL, log)
	if err != nil {
		log.Fatal().Err(err).Msg("Redis connection failed")
	}

	// 2. Clients
	clients, err := client.NewClients(cfg, log)
	if err != nil {
		log.Fatal().Err(err).Msg("gRPC Clients init failed")
	}
	defer clients.Close()

	// 3. Engine (Beyin)
	processor := engine.NewProcessor(redisClient.Client, clients, log)

	// 4. RabbitMQ Listener (Olay Dinleyici)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	consumer := event.NewConsumer(processor, log)
	if err := consumer.Start(ctx, cfg.RabbitMQURL, &wg); err != nil {
		log.Fatal().Err(err).Msg("RabbitMQ Consumer başlatılamadı")
	}

	log.Info().Msg("✅ Workflow Service Çalışıyor. Olay bekleniyor...")

	// 5. Graceful Shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Warn().Msg("Kapatma sinyali alındı. Servis durduruluyor...")
	cancel()
	wg.Wait()
	log.Info().Msg("Servis güvenle kapatıldı.")
}
