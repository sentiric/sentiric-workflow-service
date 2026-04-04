// Dosya: cmd/workflow-service/main.go
package main

import (
	"os"

	"github.com/sentiric/sentiric-workflow-service/internal/app"
	"github.com/sentiric/sentiric-workflow-service/internal/config"
	"github.com/sentiric/sentiric-workflow-service/internal/logger"
)

func main() {
	cfg, err := config.Load()
	// [ARCH-COMPLIANCE] ARCH-005 İhlal Düzeltimi: log.Fatalf kullanılamaz.
	// Config hatası varsa geçici bir bootstrap logger ile SUTS formatında Fatal basılır.
	if err != nil {
		bootstrapLog := logger.New("workflow-service", "unknown", "production", "info")
		bootstrapLog.Fatal().Str("event", "CONFIG_LOAD_FAIL").Err(err).Msg("Configuration yüklenemedi")
		os.Exit(1)
	}

	l := logger.New("workflow-service", cfg.ServiceVersion, cfg.Env, cfg.LogLevel)

	l.Info().Str("event", "SERVICE_STARTING").Msg("💠 Sentiric Workflow Service (The Cortex) başlatılıyor...")

	app.Run(cfg, l)
}
