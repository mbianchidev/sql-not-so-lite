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

func runPeriodicScanner(
	ctx context.Context,
	interval time.Duration,
	scan func(context.Context) (int, error),
) {
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
	}
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
