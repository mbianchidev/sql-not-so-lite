package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEtcqliteDisabledDefaultsAndValidation(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Etcqlite.Enabled {
		t.Fatal("Etcqlite must be opt-in")
	}
	if err := cfg.ValidateEtcqlite(); err != nil {
		t.Fatal(err)
	}
	cfg.Etcqlite.Enabled = true
	cfg.Etcqlite.AuthToken = "synthetic-etcd-token"
	if err := cfg.ValidateEtcqlite(); err == nil {
		t.Fatal("inspector enabled without workspace admin authentication")
	}
	cfg.Server.AuthToken = strings.Repeat("synthetic-", 4)
	if err := cfg.ValidateEtcqlite(); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{
		"http://192.0.2.10:2379", "https://192.0.2.10:2379",
		"http://user:password@127.0.0.1:2379", "http://127.0.0.1:2379/admin",
		"http://127.0.0.1:2379?token=secret", "http://127.0.0.1:2379#fragment",
		"file:///fixture/node.db",
	} {
		cfg.Etcqlite.Endpoint = endpoint
		if err := cfg.ValidateEtcqlite(); err == nil {
			t.Errorf("unsafe endpoint accepted: %q", endpoint)
		}
	}
	cfg.Etcqlite.Endpoint = "https://192.0.2.10:2379"
	cfg.Etcqlite.TLSCertFile = "/fixtures/client.pem"
	cfg.Etcqlite.TLSKeyFile = "/fixtures/client.key"
	if err := cfg.ValidateEtcqlite(); err != nil {
		t.Fatalf("mTLS configuration rejected: %v", err)
	}
	cfg.Etcqlite.TLSKeyFile = ""
	if err := cfg.ValidateEtcqlite(); err == nil {
		t.Fatal("incomplete mTLS configuration accepted")
	}
}

func TestEtcqliteTokenIsNeverSavedAndDisabledDoesNotReadCredentials(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SQNSL_AUTH_TOKEN", strings.Repeat("synthetic-admin-", 3))
	token := "synthetic-opaque-etcd-token"
	t.Setenv("SQNSL_ETCQLITE_TOKEN", token)
	cfg := DefaultConfig()
	cfg.Etcqlite.Enabled = true
	cfg.Etcqlite.AuthToken = token
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(ConfigPath())
	if err != nil || strings.Contains(string(data), token) {
		t.Fatalf("Etcqlite credential was persisted: %v", err)
	}
	loaded, err := Load()
	if err != nil || loaded.Etcqlite.AuthToken != token {
		t.Fatalf("Etcqlite environment credential not loaded: %v", err)
	}
	loaded.Etcqlite.Enabled = false
	loaded.Etcqlite.AuthTokenFile = filepath.Join(t.TempDir(), "missing.token")
	if err := loaded.Save(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SQNSL_ETCQLITE_TOKEN", "")
	loaded, err = Load()
	if err != nil || loaded.Etcqlite.AuthToken != "" {
		t.Fatalf("disabled integration touched credentials: %v", err)
	}
}
