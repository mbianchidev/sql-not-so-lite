package server

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/mbianchidev/sql-not-so-lite/internal/catalog"
	"github.com/mbianchidev/sql-not-so-lite/internal/config"
	"github.com/mbianchidev/sql-not-so-lite/internal/replicator"
	"github.com/mbianchidev/sql-not-so-lite/internal/scanner"
	"github.com/mbianchidev/sql-not-so-lite/internal/schema"
	"github.com/mbianchidev/sql-not-so-lite/internal/service"
)

//go:embed all:static
var staticFiles embed.FS

type HTTPServer struct {
	svc       *service.DatabaseService
	server    *http.Server
	port      int
	startTime time.Time
	catalog   *catalog.Catalog
	cfg       *config.Config
}

func NewHTTPServer(svc *service.DatabaseService, port int, cat *catalog.Catalog, cfg *config.Config) *HTTPServer {
	return &HTTPServer{
		svc:       svc,
		port:      port,
		startTime: time.Now(),
		catalog:   cat,
		cfg:       cfg,
	}
}

func (s *HTTPServer) Start() error {
	mux := http.NewServeMux()

	// REST API routes
	mux.HandleFunc("/api/databases", s.corsMiddleware(s.handleDatabases))
	mux.HandleFunc("/api/databases/", s.corsMiddleware(s.handleDatabase))
	mux.HandleFunc("/api/health", s.corsMiddleware(s.handleHealth))
	mux.HandleFunc("/api/stats", s.corsMiddleware(s.handleStats))
	mux.HandleFunc("/api/scan", s.corsMiddleware(s.handleScan))
	mux.HandleFunc("/api/discovered", s.corsMiddleware(s.handleDiscovered))
	mux.HandleFunc("/api/discovered/", s.corsMiddleware(s.handleDiscoveredItem))

	// Serve embedded GUI
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Printf("Warning: no embedded GUI files found, GUI will not be available: %v", err)
	} else {
		fileServer := http.FileServer(http.FS(staticFS))
		mux.Handle("/", spaHandler(fileServer, staticFS))
	}

	s.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", s.port),
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	log.Printf("HTTP server listening on :%d", s.port)
	return s.server.ListenAndServe()
}

func (s *HTTPServer) Stop(ctx context.Context) error {
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}

func spaHandler(fileServer http.Handler, staticFS fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/" {
			path = "index.html"
		} else {
			path = strings.TrimPrefix(path, "/")
		}

		if _, err := fs.Stat(staticFS, path); err != nil {
			// File not found — serve index.html for SPA routing
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	})
}

func (s *HTTPServer) corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next(w, r)
	}
}

func (s *HTTPServer) handleDatabases(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		dbs, err := s.svc.ListDatabases(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, dbs)

	case http.MethodPost:
		var req struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		info, err := s.svc.CreateDatabase(r.Context(), req.Name)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, info)

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *HTTPServer) handleDatabase(w http.ResponseWriter, r *http.Request) {
	// Parse path: /api/databases/{name}[/schema|/tables[/{table}]|/query]
	path := strings.TrimPrefix(r.URL.Path, "/api/databases/")
	parts := strings.SplitN(path, "/", 3)
	dbName := parts[0]

	if dbName == "" {
		writeError(w, http.StatusBadRequest, "database name required")
		return
	}

	if len(parts) == 1 {
		s.handleDatabaseCRUD(w, r, dbName)
		return
	}

	switch parts[1] {
	case "schema":
		s.handleSchema(w, r, dbName)
	case "tables":
		tableName := ""
		if len(parts) > 2 {
			tableName = parts[2]
		}
		s.handleTables(w, r, dbName, tableName)
	case "query":
		s.handleQuery(w, r, dbName)
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

func (s *HTTPServer) handleDatabaseCRUD(w http.ResponseWriter, r *http.Request, dbName string) {
	switch r.Method {
	case http.MethodGet:
		info, err := s.svc.GetDatabaseInfo(r.Context(), dbName)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, info)

	case http.MethodDelete:
		if err := s.svc.DropDatabase(r.Context(), dbName); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"success": true})

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *HTTPServer) handleSchema(w http.ResponseWriter, r *http.Request, dbName string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	tables, err := s.svc.GetSchema(r.Context(), dbName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tables)
}

func (s *HTTPServer) handleTables(w http.ResponseWriter, r *http.Request, dbName, tableName string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if tableName == "" {
		tables, err := s.svc.GetSchema(r.Context(), dbName)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		names := make([]string, len(tables))
		for i, t := range tables {
			names[i] = t.Name
		}
		writeJSON(w, http.StatusOK, map[string][]string{"tables": names})
		return
	}

	limit := 100
	offset := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil {
			limit = v
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if v, err := strconv.Atoi(o); err == nil {
			offset = v
		}
	}

	escapedTable := strings.ReplaceAll(tableName, "\"", "\"\"")
	sql := fmt.Sprintf("SELECT * FROM \"%s\" LIMIT %d OFFSET %d", escapedTable, limit, offset)
	result, err := s.svc.Query(r.Context(), dbName, sql, nil, 0, 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *HTTPServer) handleQuery(w http.ResponseWriter, r *http.Request, dbName string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		SQL    string   `json:"sql"`
		Params []string `json:"params,omitempty"`
		Limit  int      `json:"limit,omitempty"`
		Offset int      `json:"offset,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	sqlUpper := strings.TrimSpace(strings.ToUpper(req.SQL))
	isQuery := strings.HasPrefix(sqlUpper, "SELECT") ||
		strings.HasPrefix(sqlUpper, "PRAGMA") ||
		strings.HasPrefix(sqlUpper, "EXPLAIN") ||
		strings.HasPrefix(sqlUpper, "WITH")

	if isQuery {
		result, err := s.svc.Query(r.Context(), dbName, req.SQL, req.Params, req.Limit, req.Offset)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	} else {
		result, err := s.svc.Execute(r.Context(), dbName, req.SQL, req.Params)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func (s *HTTPServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"version": Version,
	})
}

func (s *HTTPServer) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"version":          Version,
		"uptime_seconds":   int64(time.Since(s.startTime).Seconds()),
		"active_databases": s.svc.ActiveCount(),
		"memory_alloc":     memStats.Alloc,
		"memory_sys":       memStats.Sys,
		"goroutines":       runtime.NumGoroutine(),
	})
}

// --------------- Discovery / Replication handlers ---------------

func (s *HTTPServer) handleScan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	sc := scanner.New(s.cfg.Scanner, s.cfg.Server.DataDir)
	files, err := sc.Scan()
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("scan failed: %v", err))
		return
	}

	var results []map[string]interface{}
	for _, f := range files {
		// Check if this DB already exists to preserve its status
		existing, _ := s.catalog.GetDiscoveredByPath(f.Path)
		status := "discovered"
		if existing != nil && existing.Status != "discovered" {
			status = existing.Status
		}

		d := &catalog.DiscoveredDB{
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
			Status:        status,
		}
		id, err := s.catalog.UpsertDiscovered(d)
		if err != nil {
			log.Printf("scan: failed to upsert %s: %v", f.Path, err)
			continue
		}
		results = append(results, map[string]interface{}{
			"id":             id,
			"name":           f.Name,
			"source_path":    f.Path,
			"size_bytes":     f.SizeBytes,
			"sqlite_version": f.SQLiteVersion,
			"priority":       f.Priority,
			"github_repo":    f.GitHubRepo,
		})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"scanned": len(results),
		"files":   results,
	})
}

func (s *HTTPServer) handleDiscovered(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	dbs, err := s.catalog.ListDiscovered()
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to list discovered: %v", err))
		return
	}

	var result []map[string]interface{}
	for _, d := range dbs {
		result = append(result, discoveredToJSON(&d))
	}
	if result == nil {
		result = []map[string]interface{}{}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *HTTPServer) handleDiscoveredItem(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/discovered/")
	parts := strings.SplitN(path, "/", 2)

	if parts[0] == "" {
		writeError(w, http.StatusBadRequest, "database id required")
		return
	}

	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid database id")
		return
	}

	if len(parts) == 1 || parts[1] == "" {
		s.handleDiscoveredGet(w, r, id)
		return
	}

	switch parts[1] {
	case "replicate":
		s.handleReplicate(w, r, id)
	case "restore":
		s.handleRestore(w, r, id)
	case "snapshots":
		s.handleSnapshots(w, r, id)
	case "versions":
		s.handleSchemaVersions(w, r, id)
	case "transitions":
		s.handleSchemaTransitions(w, r, id)
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

func (s *HTTPServer) handleDiscoveredGet(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	d, err := s.catalog.GetDiscovered(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "database not found")
		return
	}

	result := discoveredToJSON(d)

	// Include replication state if available
	rs, err := s.catalog.GetReplicationState(id)
	if err == nil {
		result["replication"] = map[string]interface{}{
			"replica_name":    rs.ReplicaName,
			"last_frame":      rs.LastFrame,
			"page_size":       rs.PageSize,
			"last_sync":       rs.LastSync,
			"sync_mode":       rs.SyncMode,
			"base_snapshot_id": rs.BaseSnapshotID.Int64,
		}
	}

	writeJSON(w, http.StatusOK, result)
}

func (s *HTTPServer) handleReplicate(w http.ResponseWriter, r *http.Request, id int64) {
	switch r.Method {
	case http.MethodPost:
		s.handleStartReplication(w, r, id)
	case http.MethodDelete:
		s.handleStopReplication(w, r, id)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *HTTPServer) handleStartReplication(w http.ResponseWriter, _ *http.Request, id int64) {
	d, err := s.catalog.GetDiscovered(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "database not found")
		return
	}

	replicaPath := filepath.Join(s.cfg.Replicator.ReplicaDir, d.Name+".sqlite")

	// Create snapshot directory for this DB
	snapshotDir := filepath.Join(s.cfg.Replicator.SnapshotDir, d.Name)
	if err := os.MkdirAll(snapshotDir, 0755); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create snapshot dir: %v", err))
		return
	}

	// Perform initial sync (creates replica via VACUUM INTO)
	_, err = replicator.InitialSync(d.SourcePath, replicaPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("initial sync failed: %v", err))
		return
	}

	// Create a snapshot from the replica for consistency
	snapshotPath := filepath.Join(snapshotDir, "v1.sqlite")
	snapshotSize, err := replicator.CreateSnapshot(replicaPath, snapshotPath)
	if err != nil {
		// Clean up replica on failure
		os.Remove(replicaPath)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("snapshot creation failed: %v", err))
		return
	}

	// Extract schema from the replica for consistency
	replicaDB, err := replicator.OpenReadOnly(replicaPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to open replica for schema: %v", err))
		return
	}
	schemaSQL, err := schema.ExtractSchema(replicaDB)
	replicaDB.Close()
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("schema extraction failed: %v", err))
		return
	}

	normalizedSchema := schema.NormalizeSchema(schemaSQL)
	schemaHash := schema.HashSchema(normalizedSchema)

	// Store schema version 0
	_, err = s.catalog.InsertSchemaVersion(&catalog.SchemaVersion{
		DatabaseID: id,
		Version:    0,
		SchemaSQL:  schemaSQL,
		SchemaHash: schemaHash,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to store schema: %v", err))
		return
	}

	// Record initial snapshot
	snapID, err := s.catalog.InsertSnapshot(&catalog.Snapshot{
		DatabaseID:    id,
		Version:       1,
		SchemaVersion: 0,
		SnapshotPath:  snapshotPath,
		SizeBytes:     snapshotSize,
		Trigger:       "initial",
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to record snapshot: %v", err))
		return
	}

	// Set replication state
	err = s.catalog.SetReplicationState(&catalog.ReplicationState{
		DatabaseID:     id,
		ReplicaName:    d.Name,
		BaseSnapshotID: sql.NullInt64{Int64: snapID, Valid: true},
		LastSync:       time.Now(),
		SyncMode:       "full",
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to set replication state: %v", err))
		return
	}

	// Update status to replicating
	if err := s.catalog.UpdateStatus(id, "replicating", ""); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to update status: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":       "replicating",
		"replica_path": replicaPath,
		"snapshot_id":  snapID,
		"schema_hash":  schemaHash,
	})
}

func (s *HTTPServer) handleStopReplication(w http.ResponseWriter, _ *http.Request, id int64) {
	_, err := s.catalog.GetDiscovered(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "database not found")
		return
	}

	if err := s.catalog.UpdateStatus(id, "paused", ""); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to update status: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "paused",
	})
}

func (s *HTTPServer) handleRestore(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	d, err := s.catalog.GetDiscovered(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "database not found")
		return
	}

	// Parse optional version from request body
	var req struct {
		Version *int `json:"version"`
	}
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&req)
	}

	snapshots, err := s.catalog.ListSnapshots(id)
	if err != nil || len(snapshots) == 0 {
		writeError(w, http.StatusNotFound, "no snapshots available")
		return
	}

	// Find the right snapshot
	var target *catalog.Snapshot
	if req.Version != nil {
		for i := range snapshots {
			if snapshots[i].Version == *req.Version {
				target = &snapshots[i]
				break
			}
		}
		if target == nil {
			writeError(w, http.StatusNotFound, fmt.Sprintf("snapshot version %d not found", *req.Version))
			return
		}
	} else {
		// Use latest (list is ordered DESC by version)
		target = &snapshots[0]
	}

	replicaPath := filepath.Join(s.cfg.Replicator.ReplicaDir, d.Name+".sqlite")
	if err := replicator.RestoreSnapshot(target.SnapshotPath, replicaPath); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("restore failed: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"restored_version": target.Version,
		"snapshot_path":    target.SnapshotPath,
		"replica_path":     replicaPath,
	})
}

func (s *HTTPServer) handleSnapshots(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	snaps, err := s.catalog.ListSnapshots(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to list snapshots: %v", err))
		return
	}

	var result []map[string]interface{}
	for _, snap := range snaps {
		result = append(result, map[string]interface{}{
			"id":             snap.ID,
			"version":        snap.Version,
			"schema_version": snap.SchemaVersion,
			"snapshot_path":  snap.SnapshotPath,
			"created_at":     snap.CreatedAt,
			"size_bytes":     snap.SizeBytes,
			"trigger":        snap.Trigger,
		})
	}
	if result == nil {
		result = []map[string]interface{}{}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *HTTPServer) handleSchemaVersions(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	versions, err := s.catalog.ListSchemaVersions(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to list schema versions: %v", err))
		return
	}

	var result []map[string]interface{}
	for _, v := range versions {
		result = append(result, map[string]interface{}{
			"id":          v.ID,
			"version":     v.Version,
			"schema_sql":  v.SchemaSQL,
			"schema_hash": v.SchemaHash,
			"detected_at": v.DetectedAt,
		})
	}
	if result == nil {
		result = []map[string]interface{}{}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *HTTPServer) handleSchemaTransitions(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	transitions, err := s.catalog.ListTransitions(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to list transitions: %v", err))
		return
	}

	var result []map[string]interface{}
	for _, t := range transitions {
		result = append(result, map[string]interface{}{
			"id":           t.ID,
			"from_version": t.FromVersion,
			"to_version":   t.ToVersion,
			"detected_ddl": t.DetectedDDL,
			"summary":      t.Summary,
			"detected_at":  t.DetectedAt,
		})
	}
	if result == nil {
		result = []map[string]interface{}{}
	}
	writeJSON(w, http.StatusOK, result)
}

func discoveredToJSON(d *catalog.DiscoveredDB) map[string]interface{} {
	return map[string]interface{}{
		"id":             d.ID,
		"name":           d.Name,
		"source_path":    d.SourcePath,
		"status":         d.Status,
		"priority":       d.Priority,
		"sqlite_version": d.SQLiteVersion,
		"page_size":      d.PageSize,
		"journal_mode":   d.JournalMode,
		"size_bytes":     d.SizeBytes,
		"last_modified":  d.LastModified,
		"first_seen":     d.FirstSeen,
		"last_scanned":   d.LastScanned,
		"github_repo":    d.GitHubRepo,
		"github_url":     d.GitHubURL,
		"error_message":  d.ErrorMessage,
	}
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
