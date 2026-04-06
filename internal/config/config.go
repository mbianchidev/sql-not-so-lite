package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Server     ServerConfig     `toml:"server"`
	Idle       IdleConfig       `toml:"idle"`
	Limits     LimitsConfig     `toml:"limits"`
	Log        LogConfig        `toml:"logging"`
	Scanner    ScannerConfig    `toml:"scanner"`
	Replicator ReplicatorConfig `toml:"replicator"`
}

type ServerConfig struct {
	GRPCPort int    `toml:"grpc_port"`
	HTTPPort int    `toml:"http_port"`
	DataDir  string `toml:"data_dir"`
}

type IdleConfig struct {
	ConnectionTimeout string `toml:"connection_timeout"`
	CheckInterval     string `toml:"check_interval"`
}

type LimitsConfig struct {
	MaxDatabases int   `toml:"max_databases"`
	MaxQuerySize int64 `toml:"max_query_size"`
	MaxResultRows int  `toml:"max_result_rows"`
}

type LogConfig struct {
	Level string `toml:"level"`
	File  string `toml:"file"`
}

type ScannerConfig struct {
	ScanRoot               string   `toml:"scan_root"`
	FileExtensions         []string `toml:"file_extensions"`
	ExcludePatterns        []string `toml:"exclude_patterns"`
	PriorityPathsDocker    []string `toml:"priority_paths_docker"`
	PriorityPathsWorkspace []string `toml:"priority_paths_workspace"`
	PriorityPathsCopilot   []string `toml:"priority_paths_copilot"`
	PriorityPathsAppData   []string `toml:"priority_paths_app_data"`
	AppDataDotdirPattern   string   `toml:"app_data_dotdir_pattern"`
	ScanInterval           string   `toml:"scan_interval"`
}

type ReplicatorConfig struct {
	Enabled           bool   `toml:"enabled"`
	SyncInterval      string `toml:"sync_interval"`
	SnapshotRetention int    `toml:"snapshot_retention"`
	ReplicaDir        string `toml:"replica_dir"`
	SnapshotDir       string `toml:"snapshot_dir"`
}

func DefaultConfig() *Config {
	homeDir, _ := os.UserHomeDir()
	baseDir := filepath.Join(homeDir, ".sql-not-so-lite")

	return &Config{
		Server: ServerConfig{
			GRPCPort: 50051,
			HTTPPort: 8080,
			DataDir:  filepath.Join(baseDir, "databases"),
		},
		Idle: IdleConfig{
			ConnectionTimeout: "5m",
			CheckInterval:     "30s",
		},
		Limits: LimitsConfig{
			MaxDatabases:  100,
			MaxQuerySize:  10 * 1024 * 1024, // 10MB
			MaxResultRows: 100000,
		},
		Log: LogConfig{
			Level: "info",
			File:  filepath.Join(baseDir, "sqnsl.log"),
		},
		Scanner: ScannerConfig{
			ScanRoot:               homeDir,
			FileExtensions:         []string{".sqlite", ".db", ".sqlite3", ".sqlitedb"},
			ExcludePatterns:        []string{"node_modules", ".git/objects", "*.tmp"},
			PriorityPathsDocker:    []string{filepath.Join(homeDir, ".orbstack"), filepath.Join(homeDir, ".docker"), "/var/lib/docker/volumes"},
			PriorityPathsWorkspace: []string{filepath.Join(homeDir, "workspace")},
			PriorityPathsCopilot:   []string{filepath.Join(homeDir, ".copilot")},
			PriorityPathsAppData:   []string{filepath.Join(homeDir, "Library", "Application Support")},
			AppDataDotdirPattern:   filepath.Join(homeDir, ".{repo-name}", "data"),
			ScanInterval:           "1h",
		},
		Replicator: ReplicatorConfig{
			Enabled:           true,
			SyncInterval:      "5s",
			SnapshotRetention: 10,
			ReplicaDir:        filepath.Join(baseDir, "replicas"),
			SnapshotDir:       filepath.Join(baseDir, "snapshots"),
		},
	}
}

func ConfigPath() string {
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".sql-not-so-lite", "config.toml")
}

func Load() (*Config, error) {
	cfg := DefaultConfig()
	path := ConfigPath()

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return cfg, nil
	}

	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return nil, fmt.Errorf("failed to load config from %s: %w", path, err)
	}

	if cfg.Server.DataDir == "" {
		cfg.Server.DataDir = DefaultConfig().Server.DataDir
	}

	return cfg, nil
}

func (c *Config) EnsureDirs() error {
	if err := os.MkdirAll(c.Server.DataDir, 0755); err != nil {
		return fmt.Errorf("failed to create data dir %s: %w", c.Server.DataDir, err)
	}

	logDir := filepath.Dir(c.Log.File)
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("failed to create log dir %s: %w", logDir, err)
	}

	if err := os.MkdirAll(c.Replicator.ReplicaDir, 0755); err != nil {
		return fmt.Errorf("failed to create replica dir %s: %w", c.Replicator.ReplicaDir, err)
	}
	if err := os.MkdirAll(c.Replicator.SnapshotDir, 0755); err != nil {
		return fmt.Errorf("failed to create snapshot dir %s: %w", c.Replicator.SnapshotDir, err)
	}

	return nil
}

func (c *Config) Save() error {
	path := ConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	encoder := toml.NewEncoder(f)
	return encoder.Encode(c)
}
