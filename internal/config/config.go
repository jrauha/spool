package config

import (
	"os"
	"strconv"
)

const (
	DefaultAddr         = ":8080"
	DefaultSpoolHome    = ".spool"
	DefaultCookieSecure = true
)

type Config struct {
	Addr         string
	SpoolHome    string
	DatabaseURL  string
	CookieSecure bool
}

func FromEnv() Config {
	return Config{
		Addr:         envOrDefault("SPOOL_ADDR", DefaultAddr),
		SpoolHome:    envOrDefault("SPOOL_HOME", DefaultSpoolHome),
		DatabaseURL:  os.Getenv("SPOOL_DATABASE_URL"),
		CookieSecure: envBool("SPOOL_COOKIE_SECURE", DefaultCookieSecure),
	}
}

func envBool(key string, fallback bool) bool {
	value, err := strconv.ParseBool(os.Getenv(key))
	if err != nil {
		return fallback
	}
	return value
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
