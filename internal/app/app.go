package app

import (
	"github.com/rs/zerolog"
	"github.com/sentiric/sentiric-workflow-service/internal/client"
	"github.com/sentiric/sentiric-workflow-service/internal/config"
	"github.com/sentiric/sentiric-workflow-service/internal/database"
	"github.com/sentiric/sentiric-workflow-service/internal/engine"
)

func Run(cfg *config.Config, log zerolog.Logger) {
	// 1. Veritabanı Bağlantıları
	// Postgres (Kuralları okumak için)
	pgPool, err := database.NewPostgresConnection(cfg.PostgresURL, log)
	if err != nil {
		log.Fatal().Err(err).Msg("Postgres connection failed")
	}
	defer pgPool.Close()

	// Redis (Durum/State tutmak için)
	redisClient, err := database.NewRedisClient(cfg.RedisURL, log)
	if err != nil {
		log.Fatal().Err(err).Msg("Redis connection failed")
	}

	// 2. Clients (Diğer servislere emir vermek için)
	clients, err := client.NewClients(cfg, log)
	if err != nil {
		log.Fatal().Err(err).Msg("gRPC Clients init failed")
	}
	defer clients.Close()

	// 3. Engine (Beyin)
	// Not: Processor artık Postgres pool'a da ihtiyaç duyabilir, şimdilik Redis ve Client ile başlatıyoruz.
	// İleride veritabanından akış okumak için pgPool'u da engine'e vereceğiz.
	processor := engine.NewProcessor(redisClient.Client, clients, log)

	// 4. RabbitMQ Listener (Placeholder)
	log.Info().Msg("🐰 RabbitMQ Listener başlatılıyor (Placeholder)...")

	// Mock bir test (Sistemin ayakta olduğunu görmek için)
	// Gerçekte bu metod RabbitMQ'dan gelen event ile tetiklenecek.
	log.Info().Msg("⚙️ Motor hazır. Olay bekleniyor.")

	// Block forever
	select {}
}
