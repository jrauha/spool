package config

import "os"

const (
	DefaultAddr      = ":8080"
	DefaultSpoolHome = ".spool"
)

type Config struct {
	Addr      string
	SpoolHome string
}

func FromEnv() Config {
	return Config{
		Addr:      envOrDefault("SPOOL_ADDR", DefaultAddr),
		SpoolHome: envOrDefault("SPOOL_HOME", DefaultSpoolHome),
	}
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
