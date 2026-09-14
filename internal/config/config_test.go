package config

import (
	"os"
	"testing"
)

func TestConfigDefault(t *testing.T) {
	_ = os.Unsetenv("HOST")
	_ = os.Unsetenv("PORT")

	cfg, err := LoadWithArgs([]string{})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if cfg.Host != "127.0.0.1" {
		t.Errorf("expected host 127.0.0.1, got %s", cfg.Host)
	}
	if cfg.Port != "8080" {
		t.Errorf("expected port 8080, got %s", cfg.Port)
	}
	if cfg.Address() != "127.0.0.1:8080" {
		t.Errorf("expected address 127.0.0.1:8080, got %s", cfg.Address())
	}
}

func TestConfigProhibitsGlobalBindingViaEnv(t *testing.T) {
	_ = os.Setenv("HOST", "0.0.0.0")
	defer os.Unsetenv("HOST")

	_, err := LoadWithArgs([]string{})
	if err == nil {
		t.Fatal("expected error when binding to 0.0.0.0 via env, got nil")
	}
}

func TestConfigProhibitsGlobalBindingViaFlag(t *testing.T) {
	_ = os.Unsetenv("HOST")

	_, err := LoadWithArgs([]string{"-host", "0.0.0.0"})
	if err == nil {
		t.Fatal("expected error when binding to 0.0.0.0 via flag, got nil")
	}
}

func TestConfigCustomFlags(t *testing.T) {
	cfg, err := LoadWithArgs([]string{"-host", "127.0.0.5", "-port", "8088"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Host != "127.0.0.5" {
		t.Errorf("expected 127.0.0.5, got %s", cfg.Host)
	}
	if cfg.Port != "8088" {
		t.Errorf("expected 8088, got %s", cfg.Port)
	}
}
