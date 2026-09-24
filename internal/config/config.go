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
	SetupToken   string
	PublicURL    string
	SMTPAddr     string
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string
}

func FromEnv() Config {
	return Config{
		Addr:         envOrDefault("SPOOL_ADDR", DefaultAddr),
		SpoolHome:    envOrDefault("SPOOL_HOME", DefaultSpoolHome),
		DatabaseURL:  os.Getenv("SPOOL_DATABASE_URL"),
		CookieSecure: envBool("SPOOL_COOKIE_SECURE", DefaultCookieSecure),
		SetupToken:   os.Getenv("SPOOL_SETUP_TOKEN"),
		PublicURL:    os.Getenv("SPOOL_PUBLIC_URL"),
		SMTPAddr:     os.Getenv("SPOOL_SMTP_ADDR"),
		SMTPUsername: os.Getenv("SPOOL_SMTP_USERNAME"),
		SMTPPassword: os.Getenv("SPOOL_SMTP_PASSWORD"),
		SMTPFrom:     os.Getenv("SPOOL_SMTP_FROM"),
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
