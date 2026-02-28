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

	// [DÜZELTME]: Değişkeni log içinde kullanarak hatayı giderdik.
	log.Info().Msgf("⚙️ Workflow Processor hazırlandı. (Engine Address: %p)", processor)

	// 4. RabbitMQ Listener (Placeholder)
	log.Info().Msg("🐰 RabbitMQ Listener başlatılıyor (Placeholder)...")

	// Geliştirme aşamasında olduğumuz için şimdilik sonsuz döngüde bekletiyoruz.
	log.Info().Msg("✅ Workflow Service Çalışıyor. Olay bekleniyor...")

	// Block forever
	select {}
}
