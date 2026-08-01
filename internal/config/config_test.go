package config

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestLoadAppliesEnvironmentOverridesWithoutConfigFile(t *testing.T) {
	runtimeHome := t.TempDir()
	scanRoot := t.TempDir()
	dataDir := filepath.Join(t.TempDir(), "data")
	t.Setenv("HOME", runtimeHome)
	t.Setenv("SQNSL_DATA_DIR", dataDir)
	t.Setenv("SQNSL_SCAN_ROOT", scanRoot)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.Server.DataDir != dataDir {
		t.Fatalf("data dir = %q, want %q", cfg.Server.DataDir, dataDir)
	}
	if cfg.Scanner.ScanRoot != scanRoot {
		t.Fatalf("scan root = %q, want %q", cfg.Scanner.ScanRoot, scanRoot)
	}

	appSupport := filepath.Join(scanRoot, "Library", "Application Support")
	if !slices.Contains(cfg.Scanner.PriorityPathsAppData, appSupport) {
		t.Fatalf("app data paths %v do not include %q", cfg.Scanner.PriorityPathsAppData, appSupport)
	}
	projects := filepath.Join(scanRoot, "projects")
	if !slices.Contains(cfg.Scanner.PriorityPathsWorkspace, projects) {
		t.Fatalf("workspace paths %v do not include %q", cfg.Scanner.PriorityPathsWorkspace, projects)
	}
}

func TestDefaultScanInterval(t *testing.T) {
	if got := DefaultConfig().Scanner.ScanInterval; got != DefaultScanInterval {
		t.Fatalf("scan interval = %q, want %q", got, DefaultScanInterval)
	}
}
