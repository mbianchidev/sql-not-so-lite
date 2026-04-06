package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/mbianchidev/sql-not-so-lite/internal/service"
)

//go:embed all:static
var staticFiles embed.FS

type HTTPServer struct {
	svc       *service.DatabaseService
	server    *http.Server
	port      int
	startTime time.Time
}

func NewHTTPServer(svc *service.DatabaseService, port int) *HTTPServer {
	return &HTTPServer{
		svc:       svc,
		port:      port,
		startTime: time.Now(),
	}
}

func (s *HTTPServer) Start() error {
	mux := http.NewServeMux()

	// REST API routes
	mux.HandleFunc("/api/databases", s.corsMiddleware(s.handleDatabases))
	mux.HandleFunc("/api/databases/", s.corsMiddleware(s.handleDatabase))
	mux.HandleFunc("/api/health", s.corsMiddleware(s.handleHealth))
	mux.HandleFunc("/api/stats", s.corsMiddleware(s.handleStats))

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

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
