package server

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/mbianchidev/sql-not-so-lite/internal/catalog"
	"github.com/mbianchidev/sql-not-so-lite/internal/service"
	"modernc.org/sqlite"
)

var exposureAlias = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

type exposureRequestBody struct {
	Alias           string   `json:"alias"`
	ManagedDatabase string   `json:"managed_database"`
	DiscoveredID    int64    `json:"discovered_id"`
	Tables          []string `json:"tables"`
	Writable        bool     `json:"writable"`
}

func decodeAPIRequest(w http.ResponseWriter, r *http.Request, target any, limit int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: invalid JSON body", service.ErrRecordInput)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("%w: request must contain one JSON value", service.ErrRecordInput)
	}
	return nil
}

func newExposureToken() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("generate API token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func (s *HTTPServer) openExposureDatabase(ctx context.Context, exposure *catalog.APIExposure) (*sql.DB, error) {
	discovered, err := s.catalog.GetDiscovered(exposure.DatabaseID)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(discovered.SourcePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: database file is unavailable", service.ErrRecordNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("inspect database file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: database path must be a regular file", service.ErrRecordInput)
	}
	if own, err := os.Stat(s.catalog.Path()); err != nil {
		return nil, fmt.Errorf("inspect internal catalog: %w", err)
	} else if os.SameFile(own, info) {
		return nil, fmt.Errorf("%w: the internal catalog cannot be exposed", service.ErrRecordInput)
	}
	path, err := filepath.Abs(discovered.SourcePath)
	if err != nil {
		return nil, err
	}
	dsn := url.URL{Scheme: "file", Path: path}
	query := url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout(5000)", "foreign_keys(1)"}}
	if exposure.Writable {
		query.Set("mode", "rw")
	}
	dsn.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		return nil, fmt.Errorf("open API database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("open API database: %w", err)
	}
	return db, nil
}

func closeAPIDatabase(db *sql.DB) {
	if err := db.Close(); err != nil {
		log.Printf("close CRUD database connection: %v", err)
	}
}

func validateExposureTables(ctx context.Context, db *sql.DB, names []string) ([]string, error) {
	if len(names) == 0 || len(names) > 100 {
		return nil, fmt.Errorf("%w: select between 1 and 100 tables or views", service.ErrRecordInput)
	}
	tables := []string{}
	seen := map[string]bool{}
	for _, name := range names {
		table, err := service.GetRecordTable(ctx, db, name)
		if err != nil {
			return nil, err
		}
		key := strings.ToLower(table.Name)
		if seen[key] {
			return nil, fmt.Errorf("%w: duplicate allowed table", service.ErrRecordInput)
		}
		seen[key] = true
		tables = append(tables, table.Name)
	}
	slices.Sort(tables)
	return tables, nil
}

func (s *HTTPServer) resolveExposureSource(ctx context.Context, body exposureRequestBody) (*catalog.APIExposure, error) {
	if (body.ManagedDatabase == "") == (body.DiscoveredID == 0) {
		return nil, fmt.Errorf("%w: choose one managed_database or discovered_id", service.ErrRecordInput)
	}
	exposure := &catalog.APIExposure{Alias: body.Alias, ManagedDatabase: body.ManagedDatabase, DatabaseID: body.DiscoveredID, Writable: body.Writable}
	if body.ManagedDatabase != "" {
		info, err := s.svc.GetDatabaseInfo(ctx, body.ManagedDatabase)
		if err != nil {
			return nil, fmt.Errorf("%w: managed database not found", service.ErrRecordNotFound)
		}
		discovered, err := s.catalog.GetDiscoveredByPath(info.Path)
		if errors.Is(err, sql.ErrNoRows) {
			if err := s.registerCreatedDatabase(info); err != nil {
				return nil, err
			}
			discovered, err = s.catalog.GetDiscoveredByPath(info.Path)
		}
		if err != nil {
			return nil, err
		}
		exposure.DatabaseID = discovered.ID
	}
	return exposure, nil
}

func (s *HTTPServer) handleExposures(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.catalog == nil {
		writeError(w, 503, "catalog unavailable")
		return
	}
	switch r.Method {
	case http.MethodGet:
		exposures, err := s.catalog.ListAPIExposures()
		if err != nil {
			writeCRUDerror(w, err)
			return
		}
		writeJSON(w, 200, exposures)
	case http.MethodPost:
		var body exposureRequestBody
		if err := decodeAPIRequest(w, r, &body, 64*1024); err != nil {
			writeCRUDerror(w, err)
			return
		}
		if !exposureAlias.MatchString(body.Alias) {
			writeError(w, 400, "alias must start with a lowercase letter and contain at most 64 letters, digits, underscores or hyphens")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		exposure, err := s.resolveExposureSource(ctx, body)
		if err != nil {
			writeCRUDerror(w, err)
			return
		}
		db, err := s.openExposureDatabase(ctx, exposure)
		if err != nil {
			writeCRUDerror(w, err)
			return
		}
		defer closeAPIDatabase(db)
		exposure.Tables, err = validateExposureTables(ctx, db, body.Tables)
		if err != nil {
			writeCRUDerror(w, err)
			return
		}
		token, err := newExposureToken()
		if err != nil {
			writeCRUDerror(w, err)
			return
		}
		exposure.TokenHash = tokenHash(token)
		if err := s.catalog.CreateAPIExposure(exposure); err != nil {
			writeCRUDerror(w, err)
			return
		}
		w.Header().Set("Location", "/api/data/"+exposure.Alias)
		writeJSON(w, 201, map[string]any{"exposure": exposure, "token": token})
	default:
		w.Header().Set("Allow", "GET, POST")
		writeError(w, 405, "method not allowed")
	}
}

func (s *HTTPServer) handleExposure(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	exposure, err := s.catalog.GetAPIExposure(r.PathValue("alias"))
	if err != nil {
		writeCRUDerror(w, err)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 200, exposure)
	case http.MethodDelete:
		if err := s.catalog.DeleteAPIExposure(exposure.Alias); err != nil {
			writeCRUDerror(w, err)
			return
		}
		w.WriteHeader(204)
	case http.MethodPut:
		var body struct {
			Tables   []string `json:"tables"`
			Writable bool     `json:"writable"`
		}
		if err := decodeAPIRequest(w, r, &body, 64*1024); err != nil {
			writeCRUDerror(w, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		exposure.Writable = body.Writable
		db, err := s.openExposureDatabase(ctx, exposure)
		if err != nil {
			writeCRUDerror(w, err)
			return
		}
		defer closeAPIDatabase(db)
		exposure.Tables, err = validateExposureTables(ctx, db, body.Tables)
		if err != nil {
			writeCRUDerror(w, err)
			return
		}
		if err := s.catalog.UpdateAPIExposure(exposure); err != nil {
			writeCRUDerror(w, err)
			return
		}
		writeJSON(w, 200, exposure)
	default:
		w.Header().Set("Allow", "GET, PUT, DELETE")
		writeError(w, 405, "method not allowed")
	}
}

func (s *HTTPServer) handleExposureToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	token, err := newExposureToken()
	if err != nil {
		writeCRUDerror(w, err)
		return
	}
	if err := s.catalog.RotateAPIExposureToken(r.PathValue("alias"), tokenHash(token)); err != nil {
		writeCRUDerror(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"token": token})
}

func (s *HTTPServer) handleData(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	exposure, err := s.catalog.GetAPIExposure(r.PathValue("alias"))
	if err != nil {
		writeCRUDerror(w, err)
		return
	}
	header := r.Header.Get("Authorization")
	admin := serverSettings(s.cfg).AuthToken
	if !validBearer(header, exposure.TokenHash) && !(admin != "" && validBearer(header, tokenHash(admin))) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="SQLite CRUD"`)
		writeError(w, 401, "valid API bearer token required")
		return
	}
	table := r.PathValue("table")
	if table != "" && !slices.Contains(exposure.Tables, table) {
		writeError(w, 403, "table is not exposed by this API")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost && r.Method != http.MethodPatch && r.Method != http.MethodDelete {
		w.Header().Set("Allow", "GET, POST, PATCH, DELETE")
		writeError(w, 405, "method not allowed")
		return
	}
	if table == "" && r.Method != http.MethodGet {
		writeError(w, 405, "schema endpoint is read-only")
		return
	}
	if r.Method != http.MethodGet && !exposure.Writable {
		writeError(w, 403, "this API is read-only; writes require explicit opt-in")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	db, err := s.openExposureDatabase(ctx, exposure)
	if err != nil {
		writeCRUDerror(w, err)
		return
	}
	defer closeAPIDatabase(db)
	if table == "" {
		tables := []*service.RecordTable{}
		for _, name := range exposure.Tables {
			meta, err := service.GetRecordTable(ctx, db, name)
			if err != nil {
				writeCRUDerror(w, err)
				return
			}
			meta.ReadOnly = meta.ReadOnly || !exposure.Writable
			tables = append(tables, meta)
		}
		writeJSON(w, 200, map[string]any{"alias": exposure.Alias, "tables": tables})
		return
	}
	if r.Method == http.MethodGet {
		rows, err := service.ListRecords(ctx, db, table, r.URL.Query())
		if err != nil {
			writeCRUDerror(w, err)
			return
		}
		writeJSON(w, 200, rows)
		return
	}
	var values map[string]any
	if r.Method != http.MethodDelete {
		if err := decodeAPIRequest(w, r, &values, 1024*1024); err != nil {
			writeCRUDerror(w, err)
			return
		}
		if values == nil {
			writeError(w, 400, "body must be a JSON object")
			return
		}
	} else {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1))
		if err != nil || len(body) > 0 {
			writeError(w, 400, "DELETE does not accept a request body")
			return
		}
	}
	row, err := service.MutateRecord(ctx, db, table, r.Method, values, r.URL.Query())
	if err != nil {
		writeCRUDerror(w, err)
		return
	}
	switch r.Method {
	case http.MethodPost:
		writeJSON(w, 201, row)
	case http.MethodPatch:
		writeJSON(w, 200, row)
	case http.MethodDelete:
		w.WriteHeader(204)
	}
}

func writeCRUDerror(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, service.ErrRecordInput):
		status = 400
	case errors.Is(err, service.ErrRecordReadOnly):
		status = 403
	case errors.Is(err, service.ErrRecordNotFound), errors.Is(err, sql.ErrNoRows):
		status = 404
	case errors.Is(err, service.ErrRecordConflict):
		status = 409
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		status = 503
	default:
		var sqliteErr *sqlite.Error
		if errors.As(err, &sqliteErr) {
			switch sqliteErr.Code() & 255 {
			case 19:
				status = 409
			case 5, 6:
				status = 503
			}
		}
	}
	if status >= 500 {
		log.Printf("CRUD API request failed: %v", err)
		writeError(w, status, "database operation unavailable; see daemon logs")
		return
	}
	writeError(w, status, err.Error())
}
