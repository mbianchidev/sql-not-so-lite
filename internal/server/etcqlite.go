package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mbianchidev/sql-not-so-lite/internal/catalog"
	"github.com/mbianchidev/sql-not-so-lite/internal/etcqlite"
	"github.com/mbianchidev/sql-not-so-lite/internal/scanner"
)

func (s *HTTPServer) initializeInspections() error {
	if s.catalog == nil {
		return nil
	}
	s.inspectionMu.Lock()
	defer s.inspectionMu.Unlock()
	if s.inspectionReady {
		return nil
	}
	databases, err := s.catalog.ListDiscovered()
	if err != nil {
		return err
	}
	referenced := make(map[string]bool)
	hasInspections := false
	for i := range databases {
		d := &databases[i]
		if d.Inspection == nil {
			continue
		}
		hasInspections = true
		d.Inspection.State = "stale"
		d.Inspection.CheckedAt = time.Time{}
		if s.cfg == nil || !s.cfg.Etcqlite.Enabled || d.Inspection.Endpoint != s.cfg.Etcqlite.Endpoint {
			d.Inspection.State = "disabled"
		}
		if d.Inspection.IncludesData {
			if err := s.removeProjection(d.SourcePath); err != nil {
				return err
			}
			d.Available = false
			d.Inspection.State = "missing"
		} else {
			referenced[filepath.Clean(d.SourcePath)] = true
		}
		if err := s.catalog.UpdateInspection(d); err != nil {
			return err
		}
	}
	if s.cfg != nil && (hasInspections || s.cfg.Etcqlite.Enabled) {
		dir := etcqlite.ProjectionDir(s.cfg.Server.DataDir)
		info, err := os.Lstat(dir)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect projection cache directory: %w", err)
		}
		if err == nil && (!info.IsDir() || info.Mode().Perm()&0o077 != 0) {
			return fmt.Errorf("projection cache must be a private directory, not a symlink")
		}
		files, err := os.ReadDir(dir)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect projection cache: %w", err)
		}
		for _, file := range files {
			path := filepath.Join(dir, file.Name())
			if !file.IsDir() && etcqlite.OwnsProjection(s.cfg.Server.DataDir, path) && !referenced[filepath.Clean(path)] {
				if err := s.removeProjection(path); err != nil {
					return err
				}
			}
		}
	}
	s.inspectionReady = true
	return nil
}

func (s *HTTPServer) removeProjection(path string) error {
	if s.cfg == nil || !etcqlite.OwnsProjection(s.cfg.Server.DataDir, path) {
		return fmt.Errorf("refusing to remove an inspection file outside the private projection cache")
	}
	info, err := os.Lstat(filepath.Dir(path))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("refusing to remove a projection through an unsafe cache directory")
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove private Etcqlite projection: %w", err)
	}
	return nil
}

func (s *HTTPServer) closeSensitiveInspections() error {
	if s.catalog == nil {
		return nil
	}
	s.inspectionMu.Lock()
	defer s.inspectionMu.Unlock()
	databases, err := s.catalog.ListDiscovered()
	if err != nil {
		return err
	}
	var failures []error
	for i := range databases {
		d := &databases[i]
		if d.Inspection == nil || !d.Inspection.IncludesData {
			continue
		}
		s.sensitiveConsent.Delete(d.ID)
		if err := s.removeProjection(d.SourcePath); err != nil {
			failures = append(failures, err)
			continue
		}
		d.Available = false
		d.Inspection.State = "missing"
		if err := s.catalog.UpdateInspection(d); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (s *HTTPServer) configuredInspection() (*catalog.DiscoveredDB, error) {
	databases, err := s.catalog.ListDiscovered()
	if err != nil {
		return nil, err
	}
	for i := range databases {
		d := &databases[i]
		if d.Inspection != nil && d.Inspection.Endpoint == s.cfg.Etcqlite.Endpoint {
			return d, nil
		}
	}
	return nil, nil
}

func (s *HTTPServer) handleEtcqlite(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.cfg == nil || !s.cfg.Etcqlite.Enabled {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "state": "disabled"})
		return
	}
	if s.catalog == nil {
		writeError(w, http.StatusServiceUnavailable, "inspection catalog unavailable")
		return
	}
	if s.inspectionInitErr != nil {
		writeInspectionError(w, s.inspectionInitErr)
		return
	}
	s.inspectionMu.Lock()
	defer s.inspectionMu.Unlock()
	d, err := s.configuredInspection()
	if err != nil {
		writeInspectionError(w, err)
		return
	}
	result := map[string]any{"enabled": true, "endpoint": s.cfg.Etcqlite.Endpoint, "state": "awaiting-consent"}
	if d == nil {
		writeJSON(w, http.StatusOK, result)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	client, discoveryErr := etcqlite.NewClient(s.cfg.Etcqlite)
	if discoveryErr == nil {
		defer client.Close()
		var discovery *etcqlite.Discovery
		discovery, discoveryErr = client.Discover(ctx)
		if discoveryErr == nil {
			if discovery.ETag != d.Inspection.ETag {
				discoveryErr = etcqlite.ErrIdentityChanged
			} else {
				d.Inspection.SourceRevision = strconv.FormatInt(discovery.Revision, 10)
				d.Inspection.State = "ready"
				if d.Inspection.SourceRevision != d.Inspection.Revision || discovery.SchemaVersion != d.Inspection.SchemaVersion {
					d.Inspection.State = "stale"
				}
				d.ErrorMessage = ""
				d.Status = "discovered"
			}
		}
	}
	d.Inspection.CheckedAt = time.Now().UTC()
	if discoveryErr != nil {
		d.Inspection.State = "disconnected"
		if errors.Is(discoveryErr, etcqlite.ErrIdentityChanged) {
			d.Inspection.State = "stale"
		}
		_, d.ErrorMessage = inspectionErrorResponse(discoveryErr)
		d.Status = "error"
		log.Printf("Etcqlite discovery unavailable: %v", discoveryErr)
	}
	if _, err := os.Lstat(d.SourcePath); errors.Is(err, os.ErrNotExist) {
		d.Available = false
		if discoveryErr == nil {
			d.Inspection.State = "missing"
		}
	} else if err != nil {
		writeInspectionError(w, err)
		return
	}
	if err := s.catalog.UpdateInspection(d); err != nil {
		writeInspectionError(w, err)
		return
	}
	result["state"] = d.Inspection.State
	result["database"] = s.discoveredJSON(d)
	if d.ErrorMessage != "" {
		result["error"] = d.ErrorMessage
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *HTTPServer) handleEtcqliteProjection(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.cfg == nil || !s.cfg.Etcqlite.Enabled {
		writeError(w, http.StatusNotFound, "Etcqlite inspection is disabled")
		return
	}
	if s.catalog == nil {
		writeError(w, http.StatusServiceUnavailable, "inspection catalog unavailable")
		return
	}
	if s.inspectionInitErr != nil {
		writeInspectionError(w, s.inspectionInitErr)
		return
	}
	var body struct {
		AckReadOnlyInspection bool `json:"ack_read_only_inspection"`
		IncludeData           bool `json:"include_data"`
		AckSensitiveData      bool `json:"ack_sensitive_data"`
	}
	if err := decodeAPIRequest(w, r, &body, 8*1024); err != nil {
		writeCRUDerror(w, err)
		return
	}
	if !body.AckReadOnlyInspection {
		writeError(w, http.StatusBadRequest, "ack_read_only_inspection must explicitly consent to read-only inspection")
		return
	}
	if body.IncludeData != body.AckSensitiveData {
		writeError(w, http.StatusBadRequest, etcqlite.ErrConsent.Error())
		return
	}
	s.inspectionMu.Lock()
	defer s.inspectionMu.Unlock()
	current, err := s.configuredInspection()
	if err != nil {
		writeInspectionError(w, err)
		return
	}
	if current != nil && current.Inspection.IncludesData {
		s.sensitiveConsent.Delete(current.ID)
		if err := s.removeProjection(current.SourcePath); err != nil {
			writeInspectionError(w, err)
			return
		}
		current.Available = false
		current.Inspection.State = "missing"
		if err := s.catalog.UpdateInspection(current); err != nil {
			writeInspectionError(w, err)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	projection, discovery, fetchErr := s.fetchProjection(ctx, current, body.IncludeData)
	if fetchErr != nil {
		if current != nil {
			current.Inspection.State = "disconnected"
			if errors.Is(fetchErr, etcqlite.ErrIdentityChanged) || errors.Is(fetchErr, etcqlite.ErrInvalidProjection) {
				current.Inspection.State = "stale"
			}
			current.Inspection.CheckedAt = time.Now().UTC()
			current.Status = "error"
			_, current.ErrorMessage = inspectionErrorResponse(fetchErr)
			if err := s.catalog.UpdateInspection(current); err != nil {
				writeInspectionError(w, errors.Join(fetchErr, err))
				return
			}
		}
		writeInspectionError(w, fetchErr)
		return
	}
	version, pageSize, err := scanner.ReadSQLiteHeader(projection.Path)
	if err != nil {
		if removeErr := s.removeProjection(projection.Path); removeErr != nil {
			err = errors.Join(err, removeErr)
		}
		writeInspectionError(w, err)
		return
	}
	now := time.Now().UTC()
	d := &catalog.DiscoveredDB{
		Name: "Etcqlite " + projection.Metadata.MemberID, SourcePath: projection.Path,
		SQLiteVersion: version, PageSize: pageSize, JournalMode: scanner.DetectJournalMode(projection.Path),
		SizeBytes: projection.Size, LastModified: now, Status: "discovered", Available: true,
		Inspection: &catalog.EtcqliteInspection{
			Endpoint: s.cfg.Etcqlite.Endpoint, ClusterID: projection.Metadata.ClusterID, MemberID: projection.Metadata.MemberID,
			ETag: discovery.ETag, Revision: strconv.FormatInt(projection.Metadata.Revision, 10),
			CompactRevision: strconv.FormatInt(projection.Metadata.CompactRevision, 10),
			SourceRevision:  strconv.FormatInt(discovery.Revision, 10),
			SchemaVersion:   projection.Metadata.SourceSchema, EngineVersion: discovery.EngineVersion,
			APIVersion: discovery.APIVersion, Build: discovery.Build, Dirty: discovery.Dirty, ConfigurationID: discovery.ConfigurationID,
			IncludesData: body.IncludeData, State: "ready", FetchedAt: now, CheckedAt: now,
		},
	}
	if d.Inspection.Revision != d.Inspection.SourceRevision {
		d.Inspection.State = "stale"
	}
	status := http.StatusCreated
	if current == nil {
		d.ID, err = s.catalog.UpsertDiscovered(d)
	} else {
		d.ID = current.ID
		d.Name = current.Name
		d.Favorite = current.Favorite
		err = s.catalog.UpdateInspection(d)
		status = http.StatusOK
	}
	if err != nil {
		if removeErr := s.removeProjection(projection.Path); removeErr != nil {
			err = errors.Join(err, removeErr)
		}
		writeInspectionError(w, err)
		return
	}
	if body.IncludeData {
		s.sensitiveConsent.Store(d.ID, true)
	}
	if current != nil && current.SourcePath != projection.Path {
		if err := s.removeProjection(current.SourcePath); err != nil {
			writeInspectionError(w, err)
			return
		}
	}
	writeJSON(w, status, s.discoveredJSON(d))
}

func (s *HTTPServer) fetchProjection(ctx context.Context, current *catalog.DiscoveredDB, includeData bool) (*etcqlite.Projection, *etcqlite.Discovery, error) {
	client, err := etcqlite.NewClient(s.cfg.Etcqlite)
	if err != nil {
		return nil, nil, err
	}
	defer client.Close()
	discovery, err := client.Discover(ctx)
	if err != nil {
		return nil, nil, err
	}
	if current != nil && discovery.ETag != current.Inspection.ETag {
		return nil, nil, etcqlite.ErrIdentityChanged
	}
	projection, err := client.Projection(ctx, discovery, includeData, includeData, etcqlite.ProjectionDir(s.cfg.Server.DataDir))
	return projection, discovery, err
}

func (s *HTTPServer) openEtcqliteProjection(ctx context.Context, d *catalog.DiscoveredDB) (*sql.DB, error) {
	if s.inspectionInitErr != nil {
		return nil, fmt.Errorf("Etcqlite inspection cache unavailable; see daemon logs")
	}
	if s.cfg == nil || !s.cfg.Etcqlite.Enabled || d.Inspection.Endpoint != s.cfg.Etcqlite.Endpoint {
		return nil, fmt.Errorf("Etcqlite inspection is disabled or its configured endpoint changed")
	}
	if !etcqlite.OwnsProjection(s.cfg.Server.DataDir, d.SourcePath) {
		return nil, etcqlite.ErrProtected
	}
	if d.Inspection.IncludesData {
		if _, consented := s.sensitiveConsent.Load(d.ID); !consented {
			return nil, fmt.Errorf("sensitive-data consent expired; fetch a new projection with explicit consent")
		}
	}
	revision, err := strconv.ParseInt(d.Inspection.Revision, 10, 64)
	if err != nil {
		return nil, etcqlite.ErrInvalidProjection
	}
	compactRevision, err := strconv.ParseInt(d.Inspection.CompactRevision, 10, 64)
	if err != nil {
		return nil, etcqlite.ErrInvalidProjection
	}
	db, err := etcqlite.OpenProjection(ctx, d.SourcePath, etcqlite.Metadata{
		ClusterID: d.Inspection.ClusterID, MemberID: d.Inspection.MemberID,
		Revision: revision, CompactRevision: compactRevision, SourceSchema: d.Inspection.SchemaVersion,
		IncludesData: d.Inspection.IncludesData,
	})
	if err != nil {
		log.Printf("open cached Etcqlite projection: %v", err)
		if errors.Is(err, etcqlite.ErrInvalidProjection) {
			return nil, err
		}
		return nil, fmt.Errorf("cached Etcqlite projection unavailable or unsafe; fetch a new projection")
	}
	return db, nil
}

func (s *HTTPServer) discoveredJSON(d *catalog.DiscoveredDB) map[string]interface{} {
	replicaDir := ""
	if s.cfg != nil {
		replicaDir = s.cfg.Replicator.ReplicaDir
	}
	result := discoveredToJSON(d, replicaDir)
	if d.Inspection == nil {
		return result
	}
	inspection := *d.Inspection
	if inspection.State == "ready" && time.Since(inspection.CheckedAt) > 30*time.Second {
		inspection.State = "stale"
	}
	if s.cfg == nil || !s.cfg.Etcqlite.Enabled || inspection.Endpoint != s.cfg.Etcqlite.Endpoint {
		inspection.State = "disabled"
		result["available"] = false
	}
	if _, err := os.Lstat(d.SourcePath); err != nil {
		result["available"] = false
		if inspection.State == "ready" || inspection.State == "stale" {
			inspection.State = "missing"
		}
	}
	if inspection.IncludesData {
		if _, consented := s.sensitiveConsent.Load(d.ID); !consented {
			result["available"] = false
		}
	}
	result["kind"] = "etcqlite"
	result["inspection"] = inspection
	result["source_path"] = inspection.Endpoint
	result["is_replica"] = false
	return result
}

func (s *HTTPServer) rejectProtectedOperation(w http.ResponseWriter, r *http.Request, id int64) bool {
	d, err := s.catalog.GetDiscovered(id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "database not found")
		return true
	}
	if err != nil {
		writeInspectionError(w, err)
		return true
	}
	if d.Inspection != nil {
		writeError(w, http.StatusForbidden, etcqlite.ErrProtected.Error())
		return true
	}
	err = etcqlite.CheckOrdinaryFile(r.Context(), d.SourcePath)
	if errors.Is(err, etcqlite.ErrProtected) {
		writeError(w, http.StatusForbidden, err.Error())
		return true
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		writeDiscoveredOpenError(w, err)
		return true
	}
	return false
}

func writeInspectionError(w http.ResponseWriter, err error) {
	log.Printf("Etcqlite inspection failed: %v", err)
	status, message := inspectionErrorResponse(err)
	writeError(w, status, message)
}

func inspectionErrorResponse(err error) (int, string) {
	status := http.StatusServiceUnavailable
	message := "Etcqlite inspection unavailable; see daemon logs and check the configured endpoint and credentials"
	var responseError *etcqlite.HTTPStatusError
	switch {
	case errors.Is(err, etcqlite.ErrIdentityChanged):
		status = http.StatusPreconditionFailed
		message = etcqlite.ErrIdentityChanged.Error()
	case errors.Is(err, etcqlite.ErrInvalidProjection):
		status = http.StatusBadGateway
		message = etcqlite.ErrInvalidProjection.Error()
	case errors.As(err, &responseError):
		message = responseError.Error()
		if responseError.Status == http.StatusPreconditionRequired {
			status = http.StatusBadGateway
		}
	}
	return status, strings.TrimSpace(message)
}
