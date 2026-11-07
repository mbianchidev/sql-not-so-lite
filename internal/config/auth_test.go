package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNetworkAuthenticationConfiguration(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Server.BindAddress != "127.0.0.1" {
		t.Fatal("default bind must be loopback")
	}
	if err := cfg.ValidateNetwork(); err != nil {
		t.Fatal(err)
	}
	cfg.Server.BindAddress = "0.0.0.0"
	if err := cfg.ValidateNetwork(); err == nil {
		t.Fatal("external bind accepted without authentication")
	}
	cfg.Server.AuthToken = strings.Repeat("fixture-", 5)
	if err := cfg.ValidateNetwork(); err != nil {
		t.Fatal(err)
	}
	cfg.Server.AuthToken = "weak"
	if err := cfg.ValidateNetwork(); err == nil {
		t.Fatal("weak admin token accepted")
	}
}

func TestAuthTokenEnvironmentAndFileAreNotSaved(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	token := strings.Repeat("synthetic-", 4)
	tokenFile := filepath.Join(home, "admin.token")
	if err := os.WriteFile(tokenFile, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.Server.AuthTokenFile = tokenFile
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil || cfg.Server.AuthToken != token {
		t.Fatalf("token file: %v", err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), token) {
		t.Fatal("secret saved in config")
	}
	override := strings.Repeat("override-", 5)
	t.Setenv("SQNSL_AUTH_TOKEN", override)
	cfg, err = Load()
	if err != nil || cfg.Server.AuthToken != override {
		t.Fatalf("environment override: %v", err)
	}
}
