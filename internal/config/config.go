// Dosya: internal/config/config.go
package config

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	ServiceVersion string // [ARCH-COMPLIANCE] Version takibi
	Env            string
	LogLevel       string
	LogFormat      string
	PostgresURL    string
	RedisURL       string
	RabbitMQURL    string

	MediaServiceURL string
	AgentServiceURL string
	B2buaServiceURL string

	CertPath string
	KeyPath  string
	CaPath   string
}

func Load() (*Config, error) {
	_ = godotenv.Load()

	// Yardımcı fonksiyonlarla hataları topla
	pgURL, err := getEnvOrFail("POSTGRES_URL")
	if err != nil {
		return nil, err
	}
	rdURL, err := getEnvOrFail("REDIS_URL")
	if err != nil {
		return nil, err
	}
	rmURL, err := getEnvOrFail("RABBITMQ_URL")
	if err != nil {
		return nil, err
	}
	cert, err := getEnvOrFail("WORKFLOW_SERVICE_CERT_PATH")
	if err != nil {
		return nil, err
	}
	key, err := getEnvOrFail("WORKFLOW_SERVICE_KEY_PATH")
	if err != nil {
		return nil, err
	}
	ca, err := getEnvOrFail("GRPC_TLS_CA_PATH")
	if err != nil {
		return nil, err
	}

	return &Config{
		// versiyınun nasıl takip edileceği konusunda bir standart belirlenebilir, ci ile otomatik inject edilebilir , semver kullanılabilir
		ServiceVersion: getEnv("SERVICE_VERSION", "1.0.8"), // Dockerfile env inject
		Env:            getEnv("ENV", "production"),
		LogLevel:       getEnv("LOG_LEVEL", "info"),
		LogFormat:      getEnv("LOG_FORMAT", "json"),
		PostgresURL:    pgURL,
		RedisURL:       rdURL,
		RabbitMQURL:    rmURL,

		MediaServiceURL: getEnv("MEDIA_SERVICE_TARGET_GRPC_URL", "media-service:13031"),
		AgentServiceURL: getEnv("AGENT_SERVICE_TARGET_GRPC_URL", "agent-service:12031"),
		B2buaServiceURL: getEnv("B2BUA_SERVICE_TARGET_GRPC_URL", "b2bua-service:13081"),

		CertPath: cert,
		KeyPath:  key,
		CaPath:   ca,
	}, nil
}

func getEnv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

// [ARCH-COMPLIANCE] ARCH-005 İhlali (fmt.Printf ile text loglama) düzeltildi.
func getEnvOrFail(key string) (string, error) {
	if v, ok := os.LookupEnv(key); ok {
		return v, nil
	}
	return "", fmt.Errorf("%s environment variable missing", key)
}
