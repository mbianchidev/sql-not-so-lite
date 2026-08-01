package daemon

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/mbianchidev/sql-not-so-lite/internal/catalog"
	"github.com/mbianchidev/sql-not-so-lite/internal/config"
	"github.com/mbianchidev/sql-not-so-lite/internal/idle"
	"github.com/mbianchidev/sql-not-so-lite/internal/replicator"
	"github.com/mbianchidev/sql-not-so-lite/internal/server"
	"github.com/mbianchidev/sql-not-so-lite/internal/service"
	"github.com/mbianchidev/sql-not-so-lite/internal/store"
)

type Daemon struct {
	cfg          *config.Config
	manager      *store.Manager
	svc          *service.DatabaseService
	grpcServer   *server.GRPCServer
	httpServer   *server.HTTPServer
	idleTracker  *idle.Tracker
	catalog      *catalog.Catalog
	scanInterval time.Duration
	scanCancel   context.CancelFunc
	scanDone     chan struct{}
	replInterval time.Duration
	replCancel   context.CancelFunc
	replDone     chan struct{}
}

func New(cfg *config.Config) (*Daemon, error) {
	if err := cfg.EnsureDirs(); err != nil {
		return nil, fmt.Errorf("failed to create directories: %w", err)
	}

	manager, err := store.NewManager(cfg.Server.DataDir, cfg.Limits.MaxDatabases)
	if err != nil {
		return nil, fmt.Errorf("failed to create store manager: %w", err)
	}

	svc := service.NewDatabaseService(manager)

	connTimeout, err := time.ParseDuration(cfg.Idle.ConnectionTimeout)
	if err != nil {
		connTimeout = 5 * time.Minute
	}
	checkInterval, err := time.ParseDuration(cfg.Idle.CheckInterval)
	if err != nil {
		checkInterval = 30 * time.Second
	}

	tracker := idle.NewTracker(manager, connTimeout, checkInterval)
	scanInterval := parseScanInterval(cfg.Scanner.ScanInterval)
	replInterval := parseReplicationInterval(cfg.Replicator.SyncInterval)

	cat, err := catalog.Open(cfg.Server.DataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to open catalog: %w", err)
	}

	return &Daemon{
		cfg:          cfg,
		manager:      manager,
		svc:          svc,
		grpcServer:   server.NewGRPCServer(svc, cfg.Server.GRPCPort),
		httpServer:   server.NewHTTPServer(svc, cfg.Server.HTTPPort, cat, cfg),
		idleTracker:  tracker,
		catalog:      cat,
		scanInterval: scanInterval,
		replInterval: replInterval,
	}, nil
}

func (d *Daemon) Run() error {
	if err := d.writePIDFile(); err != nil {
		log.Printf("Warning: failed to write PID file: %v", err)
	}
	defer d.removePIDFile()

	log.Printf("sql-not-so-lite %s starting...", server.Version)
	log.Printf("  data dir:  %s", d.cfg.Server.DataDir)
	log.Printf("  gRPC port: %d", d.cfg.Server.GRPCPort)
	log.Printf("  HTTP port: %d", d.cfg.Server.HTTPPort)
	log.Printf("  catalog:   %s/catalog.sqlite", d.cfg.Server.DataDir)
	log.Printf("  scan interval: %s", d.scanInterval)
	if d.cfg.Replicator.Enabled {
		log.Printf("  replication interval: %s", d.replInterval)
	}

	d.idleTracker.Start()
	scanCtx, scanCancel := context.WithCancel(context.Background())
	d.scanCancel = scanCancel
	d.scanDone = make(chan struct{})
	go func() {
		defer close(d.scanDone)
		runPeriodicScanner(scanCtx, d.scanInterval, func(ctx context.Context) (int, error) {
			result, err := d.httpServer.Scan(ctx, nil)
			if err != nil {
				return 0, err
			}
			return result.Scanned, nil
		})
	}()
	if d.cfg.Replicator.Enabled {
		replCtx, replCancel := context.WithCancel(context.Background())
		d.replCancel = replCancel
		d.replDone = make(chan struct{})
		go func() {
			defer close(d.replDone)
			runPeriodicReplicator(replCtx, d.replInterval, d.syncReplications)
		}()
	}

	errCh := make(chan error, 2)

	go func() {
		if err := d.grpcServer.Start(); err != nil {
			errCh <- fmt.Errorf("gRPC server error: %w", err)
		}
	}()

	go func() {
		if err := d.httpServer.Start(); err != nil {
			errCh <- fmt.Errorf("HTTP server error: %w", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		log.Printf("Received signal %v, shutting down...", sig)
	case err := <-errCh:
		log.Printf("Server error: %v, shutting down...", err)
	}

	return d.Shutdown()
}

func (d *Daemon) Shutdown() error {
	log.Println("Shutting down...")

	if d.scanCancel != nil {
		d.scanCancel()
		<-d.scanDone
	}
	if d.replCancel != nil {
		d.replCancel()
		<-d.replDone
	}
	d.idleTracker.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	d.grpcServer.Stop()

	if err := d.httpServer.Stop(ctx); err != nil {
		log.Printf("HTTP server shutdown error: %v", err)
	}

	if d.catalog != nil {
		d.catalog.Close()
	}

	d.manager.CloseAll()

	log.Println("Shutdown complete")
	return nil
}

func parseScanInterval(value string) time.Duration {
	interval, err := time.ParseDuration(value)
	if err == nil && interval > 0 {
		return interval
	}

	fallback, _ := time.ParseDuration(config.DefaultScanInterval)
	log.Printf("Warning: invalid scanner.scan_interval %q; using %s", value, config.DefaultScanInterval)
	return fallback
}

func parseReplicationInterval(value string) time.Duration {
	interval, err := time.ParseDuration(value)
	if err == nil && interval > 0 {
		return interval
	}
	fallback, _ := time.ParseDuration(config.DefaultReplicationInterval)
	log.Printf(
		"Warning: invalid replicator.sync_interval %q; using %s",
		value,
		config.DefaultReplicationInterval,
	)
	return fallback
}

func runPeriodicScanner(
	ctx context.Context,
	interval time.Duration,
	scan func(context.Context) (int, error),
) {
	run := func() {
		count, err := scan(ctx)
		switch {
		case errors.Is(err, server.ErrScanInProgress):
			log.Printf("Scheduled discovery scan skipped: %v", err)
		case err != nil:
			log.Printf("Scheduled discovery scan failed: %v", err)
		default:
			log.Printf("Scheduled discovery scan complete: %d database(s) found", count)
		}
	}
	run()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if ctx.Err() != nil {
				return
			}
			run()
		}
	}
}

func runPeriodicReplicator(
	ctx context.Context,
	interval time.Duration,
	syncDatabases func(context.Context) (int, error),
) {
	run := func() {
		count, err := syncDatabases(ctx)
		if err != nil {
			log.Printf("Scheduled replication sync completed with errors: %v", err)
			return
		}
		if count > 0 {
			log.Printf("Scheduled replication sync complete: %d database(s)", count)
		}
	}
	run()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if ctx.Err() != nil {
				return
			}
			run()
		}
	}
}

func (d *Daemon) syncReplications(ctx context.Context) (int, error) {
	databases, err := d.catalog.ListDiscoveredByStatus("replicating")
	if err != nil {
		return 0, err
	}

	synced := 0
	var syncErrors []error
	for i := range databases {
		if err := ctx.Err(); err != nil {
			return synced, err
		}
		current, err := d.catalog.GetDiscovered(databases[i].ID)
		if err != nil {
			syncErrors = append(syncErrors, fmt.Errorf("database %d: %w", databases[i].ID, err))
			continue
		}
		if current.Status != "replicating" {
			continue
		}
		state, err := d.catalog.GetReplicationState(current.ID)
		if err != nil {
			syncErr := fmt.Errorf("load replication state for %q: %w", current.Name, err)
			syncErrors = append(syncErrors, syncErr)
			if statusErr := d.catalog.UpdateStatus(current.ID, "error", syncErr.Error()); statusErr != nil {
				syncErrors = append(syncErrors, statusErr)
			}
			continue
		}
		replicaName := state.ReplicaName
		if filepath.Ext(replicaName) == "" {
			replicaName += ".sqlite"
		}
		replicaPath := filepath.Join(d.cfg.Replicator.ReplicaDir, replicaName)
		sourceChanged, err := replicationSourceChanged(current.SourcePath, state.LastSync)
		if err != nil {
			syncErr := fmt.Errorf("check replication source %q: %w", current.Name, err)
			syncErrors = append(syncErrors, syncErr)
			if statusErr := d.catalog.UpdateStatus(current.ID, "replicating", syncErr.Error()); statusErr != nil {
				syncErrors = append(syncErrors, statusErr)
			}
			continue
		}
		if !sourceChanged {
			continue
		}
		changed, err := replicator.DifferentialSyncContext(ctx, current.SourcePath, replicaPath)
		if err != nil {
			syncErr := fmt.Errorf("sync replication for %q: %w", current.Name, err)
			syncErrors = append(syncErrors, syncErr)
			if statusErr := d.catalog.UpdateStatus(current.ID, "replicating", syncErr.Error()); statusErr != nil {
				syncErrors = append(syncErrors, statusErr)
			}
			continue
		}
		state.LastSync = time.Now()
		state.SyncMode = "differential"
		if err := d.catalog.SetReplicationState(state); err != nil {
			syncErrors = append(syncErrors, fmt.Errorf("update replication state for %q: %w", current.Name, err))
			continue
		}
		if err := d.catalog.UpdateStatus(current.ID, "replicating", ""); err != nil {
			syncErrors = append(syncErrors, fmt.Errorf("clear replication error for %q: %w", current.Name, err))
			continue
		}
		log.Printf("Replication sync %q: %d changed table(s)", current.Name, len(changed))
		synced++
	}
	return synced, errors.Join(syncErrors...)
}

func replicationSourceChanged(sourcePath string, lastSync time.Time) (bool, error) {
	if lastSync.IsZero() {
		return true, nil
	}
	for _, path := range []string{sourcePath, sourcePath + "-wal"} {
		info, err := os.Stat(path)
		switch {
		case err == nil && info.ModTime().After(lastSync):
			return true, nil
		case err == nil:
		case errors.Is(err, os.ErrNotExist) && path != sourcePath:
		case err != nil:
			return false, err
		}
	}
	return false, nil
}

func (d *Daemon) Catalog() *catalog.Catalog { return d.catalog }

func (d *Daemon) pidFilePath() string {
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".sql-not-so-lite", "sqnsl.pid")
}

func (d *Daemon) writePIDFile() error {
	return os.WriteFile(d.pidFilePath(), []byte(strconv.Itoa(os.Getpid())), 0644)
}

func (d *Daemon) removePIDFile() {
	os.Remove(d.pidFilePath())
}

func ReadPIDFile() (int, error) {
	homeDir, _ := os.UserHomeDir()
	path := filepath.Join(homeDir, ".sql-not-so-lite", "sqnsl.pid")

	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("daemon not running (no PID file)")
	}

	pid, err := strconv.Atoi(string(data))
	if err != nil {
		return 0, fmt.Errorf("invalid PID file")
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return 0, fmt.Errorf("daemon not running (process not found)")
	}

	if err := process.Signal(syscall.Signal(0)); err != nil {
		return 0, fmt.Errorf("daemon not running (process %d not alive)", pid)
	}

	return pid, nil
}
