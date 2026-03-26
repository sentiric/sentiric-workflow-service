package main

import (
	"log"

	"github.com/sentiric/sentiric-workflow-service/internal/app"
	"github.com/sentiric/sentiric-workflow-service/internal/config"
	"github.com/sentiric/sentiric-workflow-service/internal/logger"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Config hatası: %v", err)
	}

	l := logger.New("workflow-service", cfg.Env, cfg.LogLevel)
	// [ARCH-COMPLIANCE] SUTS v4.0: Mandatory event key added.
	l.Info().Str("event", "SERVICE_STARTING").Msg("💠 Sentiric Workflow Service (The Cortex) başlatılıyor...")

	app.Run(cfg, l)
}
