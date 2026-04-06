package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Server ServerConfig `toml:"server"`
	Idle   IdleConfig   `toml:"idle"`
	Limits LimitsConfig `toml:"limits"`
	Log    LogConfig    `toml:"logging"`
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
