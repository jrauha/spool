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
}

func TestFromEnvUsesOverrides(t *testing.T) {
	t.Setenv("SPOOL_ADDR", ":9090")
	t.Setenv("SPOOL_HOME", "/var/lib/spool")

	cfg := FromEnv()
	if cfg.Addr != ":9090" {
		t.Fatalf("Addr = %q, want :9090", cfg.Addr)
	}
	if cfg.SpoolHome != "/var/lib/spool" {
		t.Fatalf("SpoolHome = %q, want /var/lib/spool", cfg.SpoolHome)
	}
}
