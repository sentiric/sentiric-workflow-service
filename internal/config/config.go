package config

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	Env         string
	LogLevel    string
	LogFormat   string
	PostgresURL string
	RedisURL    string
	RabbitMQURL string

	// Hedef Servisler (Komut göndermek için)
	MediaServiceURL string
	AgentServiceURL string
	B2buaServiceURL string

	// Security
	CertPath string
	KeyPath  string
	CaPath   string
}

func Load() (*Config, error) {
	godotenv.Load()

	return &Config{
		Env:         getEnv("ENV", "production"),
		LogLevel:    getEnv("LOG_LEVEL", "info"),
		LogFormat:   getEnv("LOG_FORMAT", "json"),
		PostgresURL: getEnvOrFail("POSTGRES_URL"),
		RedisURL:    getEnvOrFail("REDIS_URL"),
		RabbitMQURL: getEnvOrFail("RABBITMQ_URL"),

		MediaServiceURL: getEnv("MEDIA_SERVICE_TARGET_GRPC_URL", "media-service:13031"),
		AgentServiceURL: getEnv("AGENT_SERVICE_TARGET_GRPC_URL", "agent-service:12031"),
		B2buaServiceURL: getEnv("B2BUA_SERVICE_TARGET_GRPC_URL", "b2bua-service:13081"),

		CertPath: getEnvOrFail("WORKFLOW_SERVICE_CERT_PATH"),
		KeyPath:  getEnvOrFail("WORKFLOW_SERVICE_KEY_PATH"),
		CaPath:   getEnvOrFail("GRPC_TLS_CA_PATH"),
	}, nil
}

func getEnv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func getEnvOrFail(key string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	fmt.Printf("FATAL: %s environment variable missing\n", key)
	os.Exit(1)
	return ""
}
