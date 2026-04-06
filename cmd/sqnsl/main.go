package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/mbianchidev/sql-not-so-lite/internal/catalog"
	"github.com/mbianchidev/sql-not-so-lite/internal/config"
	"github.com/mbianchidev/sql-not-so-lite/internal/daemon"
	"github.com/mbianchidev/sql-not-so-lite/internal/replicator"
	"github.com/mbianchidev/sql-not-so-lite/internal/scanner"
	"github.com/mbianchidev/sql-not-so-lite/internal/schema"
	"github.com/mbianchidev/sql-not-so-lite/internal/server"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "sqnsl",
	Short: "sql-not-so-lite — lightweight SQLite-as-a-service daemon",
	Long:  "Manages multiple SQLite databases as files, provides a gRPC API for applications and a web GUI for debugging.",
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		background, _ := cmd.Flags().GetBool("daemon")

		if background {
			exe, err := os.Executable()
			if err != nil {
				return fmt.Errorf("failed to get executable path: %w", err)
			}
			proc := exec.Command(exe, "start")
			proc.Stdout = nil
			proc.Stderr = nil
			proc.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			if err := proc.Start(); err != nil {
				return fmt.Errorf("failed to start background daemon: %w", err)
			}
			fmt.Printf("Daemon started in background (PID %d)\n", proc.Process.Pid)
			return nil
		}

		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		d, err := daemon.New(cfg)
		if err != nil {
			return fmt.Errorf("failed to create daemon: %w", err)
		}

		return d.Run()
	},
}

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the running daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		pid, err := daemon.ReadPIDFile()
		if err != nil {
			return err
		}

		process, err := os.FindProcess(pid)
		if err != nil {
			return fmt.Errorf("failed to find process %d: %w", pid, err)
		}

		if err := process.Signal(syscall.SIGTERM); err != nil {
			return fmt.Errorf("failed to stop daemon (PID %d): %w", pid, err)
		}

		fmt.Printf("Sent stop signal to daemon (PID %d)\n", pid)
		return nil
	},
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show daemon status",
	RunE: func(cmd *cobra.Command, args []string) error {
		pid, err := daemon.ReadPIDFile()
		if err != nil {
			fmt.Println("Daemon is not running")
			return nil
		}

		cfg, err := config.Load()
		if err != nil {
			cfg = config.DefaultConfig()
		}

		fmt.Printf("Daemon is running (PID %d)\n", pid)
		fmt.Printf("  gRPC port: %d\n", cfg.Server.GRPCPort)
		fmt.Printf("  HTTP port: %d\n", cfg.Server.HTTPPort)
		fmt.Printf("  Data dir:  %s\n", cfg.Server.DataDir)
		return nil
	},
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all databases",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		entries, err := os.ReadDir(cfg.Server.DataDir)
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Println("No databases found (data dir does not exist)")
				return nil
			}
			return fmt.Errorf("failed to read data dir: %w", err)
		}

		count := 0
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".sqlite" {
				continue
			}
			name := e.Name()[:len(e.Name())-len(".sqlite")]
			info, _ := e.Info()
			size := int64(0)
			if info != nil {
				size = info.Size()
			}
			fmt.Printf("  %s (%s)\n", name, formatSize(size))
			count++
		}

		if count == 0 {
			fmt.Println("No databases found")
		} else {
			fmt.Printf("\n%d database(s)\n", count)
		}
		return nil
	},
}

var guiCmd = &cobra.Command{
	Use:   "gui",
	Short: "Open web GUI in browser",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			cfg = config.DefaultConfig()
		}

		url := fmt.Sprintf("http://localhost:%d", cfg.Server.HTTPPort)

		var openCmd *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			openCmd = exec.Command("open", url)
		case "linux":
			openCmd = exec.Command("xdg-open", url)
		case "windows":
			openCmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
		default:
			fmt.Printf("Open %s in your browser\n", url)
			return nil
		}

		if err := openCmd.Start(); err != nil {
			fmt.Printf("Could not open browser. Visit %s manually.\n", url)
		} else {
			fmt.Printf("Opening %s ...\n", url)
		}
		return nil
	},
}

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Show or manage configuration",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		fmt.Printf("Config file: %s\n\n", config.ConfigPath())
		fmt.Printf("[server]\n")
		fmt.Printf("  grpc_port = %d\n", cfg.Server.GRPCPort)
		fmt.Printf("  http_port = %d\n", cfg.Server.HTTPPort)
		fmt.Printf("  data_dir  = %q\n", cfg.Server.DataDir)
		fmt.Printf("\n[idle]\n")
		fmt.Printf("  connection_timeout = %q\n", cfg.Idle.ConnectionTimeout)
		fmt.Printf("  check_interval     = %q\n", cfg.Idle.CheckInterval)
		fmt.Printf("\n[limits]\n")
		fmt.Printf("  max_databases  = %d\n", cfg.Limits.MaxDatabases)
		fmt.Printf("  max_query_size = %d\n", cfg.Limits.MaxQuerySize)
		fmt.Printf("  max_result_rows = %d\n", cfg.Limits.MaxResultRows)
		fmt.Printf("\n[logging]\n")
		fmt.Printf("  level = %q\n", cfg.Log.Level)
		fmt.Printf("  file  = %q\n", cfg.Log.File)
		return nil
	},
}

var configInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Create default config file",
	RunE: func(cmd *cobra.Command, args []string) error {
		path := config.ConfigPath()
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("config file already exists: %s", path)
		}

		cfg := config.DefaultConfig()
		if err := cfg.Save(); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		fmt.Printf("Config file created: %s\n", path)
		return nil
	},
}

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install as a system service (launchd on macOS, systemd on Linux)",
	RunE: func(cmd *cobra.Command, args []string) error {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("failed to get executable path: %w", err)
		}

		switch runtime.GOOS {
		case "darwin":
			return installLaunchd(exe)
		case "linux":
			return installSystemd(exe)
		default:
			return fmt.Errorf("service installation is not supported on %s", runtime.GOOS)
		}
	},
}

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove the system service",
	RunE: func(cmd *cobra.Command, args []string) error {
		switch runtime.GOOS {
		case "darwin":
			return uninstallLaunchd()
		case "linux":
			return uninstallSystemd()
		default:
			return fmt.Errorf("service uninstallation is not supported on %s", runtime.GOOS)
		}
	},
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("sql-not-so-lite %s\n", server.Version)
	},
}

var scanCmd = &cobra.Command{
	Use:   "scan [path...]",
	Short: "Scan for SQLite databases",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		cat, err := catalog.Open(cfg.Server.DataDir)
		if err != nil {
			return fmt.Errorf("failed to open catalog: %w", err)
		}
		defer cat.Close()

		roots := []string{cfg.Scanner.ScanRoot}
		if len(args) > 0 {
			roots = args
		}

		var allFiles []scanner.DiscoveredFile
		for _, root := range roots {
			scanCfg := cfg.Scanner
			scanCfg.ScanRoot = root
			s := scanner.New(scanCfg, cfg.Server.DataDir)
			files, err := s.Scan()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: scan of %s failed: %v\n", root, err)
				continue
			}
			allFiles = append(allFiles, files...)
		}

		if len(allFiles) == 0 {
			fmt.Println("No SQLite databases found.")
			return nil
		}

		fmt.Printf("%-30s %-10s %-10s %-12s %-10s %s\n",
			"NAME", "SIZE", "PAGE", "JOURNAL", "PRIORITY", "PATH")
		fmt.Println(repeatChar('-', 100))

		for _, f := range allFiles {
			_, err := cat.UpsertDiscovered(&catalog.DiscoveredDB{
				Name:          f.Name,
				SourcePath:    f.Path,
				SQLiteVersion: f.SQLiteVersion,
				PageSize:      f.PageSize,
				JournalMode:   f.JournalMode,
				SizeBytes:     f.SizeBytes,
				LastModified:  f.LastModified,
				GitHubRepo:    f.GitHubRepo,
				GitHubURL:     f.GitHubURL,
				Priority:      f.Priority,
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to upsert %s: %v\n", f.Name, err)
			}

			fmt.Printf("%-30s %-10s %-10d %-12s %-10s %s\n",
				truncate(f.Name, 30), formatSize(f.SizeBytes), f.PageSize,
				f.JournalMode, f.Priority, f.Path)
		}

		fmt.Printf("\n%d database(s) found and cataloged.\n", len(allFiles))
		return nil
	},
}

var discoveredCmd = &cobra.Command{
	Use:   "discovered",
	Short: "List discovered databases",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		cat, err := catalog.Open(cfg.Server.DataDir)
		if err != nil {
			return fmt.Errorf("failed to open catalog: %w", err)
		}
		defer cat.Close()

		dbs, err := cat.ListDiscovered()
		if err != nil {
			return fmt.Errorf("failed to list discovered databases: %w", err)
		}

		if len(dbs) == 0 {
			fmt.Println("No discovered databases. Run 'sqnsl scan' first.")
			return nil
		}

		fmt.Printf("%-30s %-40s %-12s %-10s %-20s %s\n",
			"NAME", "PATH", "STATUS", "PRIORITY", "GITHUB REPO", "SIZE")
		fmt.Println(repeatChar('-', 130))

		for _, d := range dbs {
			fmt.Printf("%-30s %-40s %-12s %-10s %-20s %s\n",
				truncate(d.Name, 30), truncate(d.SourcePath, 40),
				d.Status, d.Priority,
				truncate(d.GitHubRepo, 20), formatSize(d.SizeBytes))
		}

		fmt.Printf("\n%d database(s)\n", len(dbs))
		return nil
	},
}

var replicateCmd = &cobra.Command{
	Use:   "replicate <name>",
	Short: "Start replicating a discovered database",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
		if err := cfg.EnsureDirs(); err != nil {
			return fmt.Errorf("failed to create directories: %w", err)
		}

		cat, err := catalog.Open(cfg.Server.DataDir)
		if err != nil {
			return fmt.Errorf("failed to open catalog: %w", err)
		}
		defer cat.Close()

		db, err := cat.GetDiscoveredByName(name)
		if err != nil {
			return fmt.Errorf("database %q not found: %w", name, err)
		}

		replicaPath := filepath.Join(cfg.Replicator.ReplicaDir, db.Name+".sqlite")

		fmt.Printf("Starting initial sync: %s → %s\n", db.SourcePath, replicaPath)
		_, err = replicator.InitialSync(db.SourcePath, replicaPath)
		if err != nil {
			return fmt.Errorf("initial sync failed: %w", err)
		}

		// Extract and store schema as v0
		srcDB, err := sql.Open("sqlite", db.SourcePath+"?_pragma=busy_timeout(5000)&_pragma=query_only(1)")
		if err != nil {
			return fmt.Errorf("failed to open source for schema: %w", err)
		}
		srcDB.SetMaxOpenConns(1)
		rawSchema, err := schema.ExtractSchema(srcDB)
		srcDB.Close()
		if err != nil {
			return fmt.Errorf("failed to extract schema: %w", err)
		}
		normalized := schema.NormalizeSchema(rawSchema)
		hash := schema.HashSchema(normalized)

		_, err = cat.InsertSchemaVersion(&catalog.SchemaVersion{
			DatabaseID: db.ID,
			Version:    0,
			SchemaSQL:  rawSchema,
			SchemaHash: hash,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to store schema version: %v\n", err)
		}

		// Create initial snapshot record
		snapshotPath := filepath.Join(cfg.Replicator.SnapshotDir, db.Name+"_v1.sqlite")
		snapshotSize, err := replicator.CreateSnapshot(db.SourcePath, snapshotPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to create snapshot: %v\n", err)
		} else {
			_, err = cat.InsertSnapshot(&catalog.Snapshot{
				DatabaseID:    db.ID,
				Version:       1,
				SchemaVersion: 0,
				SnapshotPath:  snapshotPath,
				SizeBytes:     snapshotSize,
				Trigger:       "initial",
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to record snapshot: %v\n", err)
			}
		}

		// Set replication state
		err = cat.SetReplicationState(&catalog.ReplicationState{
			DatabaseID:  db.ID,
			ReplicaName: db.Name,
			LastSync:    time.Now(),
			SyncMode:    "full",
		})
		if err != nil {
			return fmt.Errorf("failed to set replication state: %w", err)
		}

		// Update status to replicating
		if err := cat.UpdateStatus(db.ID, "replicating", ""); err != nil {
			return fmt.Errorf("failed to update status: %w", err)
		}

		fmt.Printf("Replication started for %q\n", name)
		fmt.Printf("  Replica: %s\n", replicaPath)
		fmt.Printf("  Schema hash: %s\n", hash[:12])
		return nil
	},
}

var replicateStopCmd = &cobra.Command{
	Use:   "stop <name>",
	Short: "Stop replicating a database",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		cat, err := catalog.Open(cfg.Server.DataDir)
		if err != nil {
			return fmt.Errorf("failed to open catalog: %w", err)
		}
		defer cat.Close()

		db, err := cat.GetDiscoveredByName(name)
		if err != nil {
			return fmt.Errorf("database %q not found: %w", name, err)
		}

		if err := cat.UpdateStatus(db.ID, "paused", ""); err != nil {
			return fmt.Errorf("failed to update status: %w", err)
		}

		fmt.Printf("Replication paused for %q\n", name)
		return nil
	},
}

var restoreCmd = &cobra.Command{
	Use:   "restore <name>",
	Short: "Restore a database from its replica",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		cat, err := catalog.Open(cfg.Server.DataDir)
		if err != nil {
			return fmt.Errorf("failed to open catalog: %w", err)
		}
		defer cat.Close()

		db, err := cat.GetDiscoveredByName(name)
		if err != nil {
			return fmt.Errorf("database %q not found: %w", name, err)
		}

		snapshots, err := cat.ListSnapshots(db.ID)
		if err != nil || len(snapshots) == 0 {
			return fmt.Errorf("no snapshots available for %q", name)
		}

		// Pick snapshot: specific version or latest
		ver, _ := cmd.Flags().GetInt("version")
		var snap catalog.Snapshot
		if ver > 0 {
			found := false
			for _, s := range snapshots {
				if s.Version == ver {
					snap = s
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("snapshot version %d not found for %q", ver, name)
			}
		} else {
			snap = snapshots[0] // latest (sorted DESC)
		}

		targetPath := db.SourcePath
		if to, _ := cmd.Flags().GetString("to"); to != "" {
			targetPath = to
		}

		fmt.Printf("Restoring %q from snapshot v%d → %s\n", name, snap.Version, targetPath)
		if err := replicator.RestoreSnapshot(snap.SnapshotPath, targetPath); err != nil {
			return fmt.Errorf("restore failed: %w", err)
		}

		fmt.Printf("Restore complete.\n")
		return nil
	},
}

var versionsCmd = &cobra.Command{
	Use:   "versions <name>",
	Short: "List schema versions for a database",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		cat, err := catalog.Open(cfg.Server.DataDir)
		if err != nil {
			return fmt.Errorf("failed to open catalog: %w", err)
		}
		defer cat.Close()

		db, err := cat.GetDiscoveredByName(name)
		if err != nil {
			return fmt.Errorf("database %q not found: %w", name, err)
		}

		versions, err := cat.ListSchemaVersions(db.ID)
		if err != nil {
			return fmt.Errorf("failed to list schema versions: %w", err)
		}

		if len(versions) == 0 {
			fmt.Printf("No schema versions recorded for %q\n", name)
			return nil
		}

		fmt.Printf("%-10s %-14s %s\n", "VERSION", "HASH", "DETECTED AT")
		fmt.Println(repeatChar('-', 50))

		for _, v := range versions {
			hash := v.SchemaHash
			if len(hash) > 12 {
				hash = hash[:12]
			}
			fmt.Printf("%-10d %-14s %s\n", v.Version, hash, v.DetectedAt.Format(time.RFC3339))
		}
		return nil
	},
}

var transitionsCmd = &cobra.Command{
	Use:   "transitions <name>",
	Short: "Show schema transition history",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		cat, err := catalog.Open(cfg.Server.DataDir)
		if err != nil {
			return fmt.Errorf("failed to open catalog: %w", err)
		}
		defer cat.Close()

		db, err := cat.GetDiscoveredByName(name)
		if err != nil {
			return fmt.Errorf("database %q not found: %w", name, err)
		}

		transitions, err := cat.ListTransitions(db.ID)
		if err != nil {
			return fmt.Errorf("failed to list transitions: %w", err)
		}

		if len(transitions) == 0 {
			fmt.Printf("No schema transitions recorded for %q\n", name)
			return nil
		}

		fmt.Printf("%-15s %-40s %s\n", "TRANSITION", "SUMMARY", "DETECTED AT")
		fmt.Println(repeatChar('-', 75))

		for _, t := range transitions {
			label := fmt.Sprintf("v%d → v%d", t.FromVersion, t.ToVersion)
			summary := t.Summary
			if summary == "" {
				summary = "(no summary)"
			}
			fmt.Printf("%-15s %-40s %s\n", label, truncate(summary, 40), t.DetectedAt.Format(time.RFC3339))
		}
		return nil
	},
}

func init() {
	startCmd.Flags().BoolP("daemon", "d", false, "Run in background")
	configCmd.AddCommand(configInitCmd)

	restoreCmd.Flags().IntP("version", "v", 0, "Specific snapshot version to restore")
	restoreCmd.Flags().String("to", "", "Alternate restore target path")

	replicateCmd.AddCommand(replicateStopCmd)

	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(stopCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(guiCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(installCmd)
	rootCmd.AddCommand(uninstallCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(scanCmd)
	rootCmd.AddCommand(discoveredCmd)
	rootCmd.AddCommand(replicateCmd)
	rootCmd.AddCommand(restoreCmd)
	rootCmd.AddCommand(versionsCmd)
	rootCmd.AddCommand(transitionsCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func formatSize(bytes int64) string {
	if bytes < 1024 {
		return strconv.FormatInt(bytes, 10) + " B"
	}
	if bytes < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

func repeatChar(ch byte, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = ch
	}
	return string(b)
}

func installLaunchd(exe string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	plistDir := filepath.Join(homeDir, "Library", "LaunchAgents")
	if err := os.MkdirAll(plistDir, 0755); err != nil {
		return fmt.Errorf("failed to create LaunchAgents directory: %w", err)
	}

	logPath := filepath.Join(homeDir, ".sql-not-so-lite", "sqnsl.log")
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.sqnsl</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>start</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>%s</string>
    <key>StandardErrorPath</key>
    <string>%s</string>
    <key>ProcessType</key>
    <string>Background</string>
    <key>LowPriorityIO</key>
    <true/>
</dict>
</plist>`, exe, logPath, logPath)

	plistPath := filepath.Join(plistDir, "com.sqnsl.plist")
	if err := os.WriteFile(plistPath, []byte(plist), 0644); err != nil {
		return fmt.Errorf("failed to write plist: %w", err)
	}

	if err := exec.Command("launchctl", "load", plistPath).Run(); err != nil {
		log.Printf("Warning: launchctl load failed: %v", err)
	}

	fmt.Printf("Service installed: %s\n", plistPath)
	fmt.Println("The daemon will start automatically on login.")
	return nil
}

func uninstallLaunchd() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	plistPath := filepath.Join(homeDir, "Library", "LaunchAgents", "com.sqnsl.plist")
	_ = exec.Command("launchctl", "unload", plistPath).Run()
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove plist: %w", err)
	}

	fmt.Println("Service uninstalled.")
	return nil
}

func installSystemd(exe string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	unitDir := filepath.Join(homeDir, ".config", "systemd", "user")
	if err := os.MkdirAll(unitDir, 0755); err != nil {
		return fmt.Errorf("failed to create systemd user dir: %w", err)
	}

	unit := fmt.Sprintf(`[Unit]
Description=sql-not-so-lite SQLite daemon
After=network.target

[Service]
Type=simple
ExecStart=%s start
Restart=on-failure
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=default.target
`, exe)

	unitPath := filepath.Join(unitDir, "sqnsl.service")
	if err := os.WriteFile(unitPath, []byte(unit), 0644); err != nil {
		return fmt.Errorf("failed to write service file: %w", err)
	}

	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	_ = exec.Command("systemctl", "--user", "enable", "sqnsl.service").Run()

	fmt.Printf("Service installed: %s\n", unitPath)
	fmt.Println("Run 'systemctl --user start sqnsl' to start the daemon.")
	return nil
}

func uninstallSystemd() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	_ = exec.Command("systemctl", "--user", "stop", "sqnsl.service").Run()
	_ = exec.Command("systemctl", "--user", "disable", "sqnsl.service").Run()

	unitPath := filepath.Join(homeDir, ".config", "systemd", "user", "sqnsl.service")
	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove service file: %w", err)
	}

	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()

	fmt.Println("Service uninstalled.")
	return nil
}
