package logger

import (
	"os"
	"time"

	"github.com/rs/zerolog"
)

func New(serviceName string, env string, level string) zerolog.Logger {
	l, _ := zerolog.ParseLevel(level)

	zerolog.TimeFieldFormat = time.RFC3339Nano
	zerolog.TimestampFieldName = "ts"
	zerolog.LevelFieldName = "severity"
	zerolog.MessageFieldName = "message"

	// JSON Logger (Production)
	logger := zerolog.New(os.Stderr).
		With().
		Timestamp().
		Str("schema_v", "1.0.0").
		Str("resource.service.name", serviceName).
		Str("resource.service.env", env).
		Logger()

	return logger.Level(l)
}
