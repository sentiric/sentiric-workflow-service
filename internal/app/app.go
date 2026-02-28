package app

import (
	"github.com/rs/zerolog"
	"github.com/sentiric/sentiric-workflow-service/internal/client"
	"github.com/sentiric/sentiric-workflow-service/internal/config"
	"github.com/sentiric/sentiric-workflow-service/internal/database"
	"github.com/sentiric/sentiric-workflow-service/internal/engine"
)

func Run(cfg *config.Config, log zerolog.Logger) {
	// 1. Redis
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

	// 3. Engine
	processor := engine.NewProcessor(redisClient.Client, clients, log)

	// 4. RabbitMQ Listener (Placeholder)
	// Buraya RabbitMQ consumer eklenecek. Şimdilik dummy log.
	log.Info().Msg("🐰 RabbitMQ Listener başlatılıyor (Placeholder)...")

	// Motoru test etmek için dummy start (Geliştirme aşaması)
	// processor.StartWorkflow(...)

	log.Info().Msg("✅ Workflow Service Çalışıyor. (Press Ctrl+C to stop)")

	// Block forever
	select {}
}
