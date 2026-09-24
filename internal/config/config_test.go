package config

import "testing"

func TestFromEnvUsesDefaults(t *testing.T) {
	t.Setenv("SPOOL_ADDR", "")
	t.Setenv("SPOOL_HOME", "")

	cfg := FromEnv()
	if cfg.Addr != DefaultAddr {
		t.Fatalf("Addr = %q, want %q", cfg.Addr, DefaultAddr)
	}
	if cfg.SpoolHome != DefaultSpoolHome {
		t.Fatalf("SpoolHome = %q, want %q", cfg.SpoolHome, DefaultSpoolHome)
	}
	if cfg.DatabaseURL != "" {
		t.Fatalf("DatabaseURL = %q, want empty", cfg.DatabaseURL)
	}
	if !cfg.CookieSecure {
		t.Fatal("CookieSecure = false, want true")
	}
}

func TestFromEnvUsesOverrides(t *testing.T) {
	t.Setenv("SPOOL_ADDR", ":9090")
	t.Setenv("SPOOL_HOME", "/var/lib/spool")
	t.Setenv("SPOOL_DATABASE_URL", "postgres://spool:spool@localhost/spool?sslmode=disable")
	t.Setenv("SPOOL_COOKIE_SECURE", "false")
	t.Setenv("SPOOL_PUBLIC_URL", "https://spool.example")
	t.Setenv("SPOOL_SMTP_ADDR", "smtp.example:587")
	t.Setenv("SPOOL_SMTP_USERNAME", "spool")
	t.Setenv("SPOOL_SMTP_PASSWORD", "secret")
	t.Setenv("SPOOL_SMTP_FROM", "Spool <spool@example.com>")

	cfg := FromEnv()
	if cfg.Addr != ":9090" {
		t.Fatalf("Addr = %q, want :9090", cfg.Addr)
	}
	if cfg.SpoolHome != "/var/lib/spool" {
		t.Fatalf("SpoolHome = %q, want /var/lib/spool", cfg.SpoolHome)
	}
	if cfg.DatabaseURL != "postgres://spool:spool@localhost/spool?sslmode=disable" {
		t.Fatalf("DatabaseURL = %q, want override", cfg.DatabaseURL)
	}
	if cfg.CookieSecure {
		t.Fatal("CookieSecure = true, want false")
	}
	if cfg.PublicURL != "https://spool.example" || cfg.SMTPAddr != "smtp.example:587" {
		t.Fatalf("password reset config = (%q, %q)", cfg.PublicURL, cfg.SMTPAddr)
	}
	if cfg.SMTPUsername != "spool" || cfg.SMTPPassword != "secret" || cfg.SMTPFrom != "Spool <spool@example.com>" {
		t.Fatal("SMTP credentials were not loaded")
	}
}
