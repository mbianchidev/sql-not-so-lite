package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type DBEntry struct {
	DB       *sql.DB
	Name     string
	Path     string
	LastUsed time.Time
	mu       sync.Mutex
}

type Manager struct {
	dataDir   string
	databases map[string]*DBEntry
	mu        sync.RWMutex
	maxDBs    int
}

func NewManager(dataDir string, maxDBs int) (*Manager, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	m := &Manager{
		dataDir:   dataDir,
		databases: make(map[string]*DBEntry),
		maxDBs:    maxDBs,
	}

	return m, nil
}

func validateName(name string) error {
	if name == "" {
		return fmt.Errorf("database name cannot be empty")
	}
	if len(name) > 128 {
		return fmt.Errorf("database name too long (max 128 chars)")
	}
	if strings.ContainsAny(name, "/\\:*?\"<>|") {
		return fmt.Errorf("database name contains invalid characters")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("database name cannot be '.' or '..'")
	}
	return nil
}

func (m *Manager) Create(name string) (*DBEntry, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.databases[name]; exists {
		return nil, fmt.Errorf("database %q already exists", name)
	}

	if len(m.databases) >= m.maxDBs {
		return nil, fmt.Errorf("maximum number of databases (%d) reached", m.maxDBs)
	}

	dbPath := filepath.Join(m.dataDir, name+".sqlite")
	if _, err := os.Stat(dbPath); err == nil {
		return nil, fmt.Errorf("database file %q already exists on disk", name)
	}

	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("failed to create database: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	entry := &DBEntry{
		DB:       db,
		Name:     name,
		Path:     dbPath,
		LastUsed: time.Now(),
	}

	m.databases[name] = entry
	return entry, nil
}

func (m *Manager) Get(name string) (*DBEntry, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}

	m.mu.RLock()
	entry, exists := m.databases[name]
	m.mu.RUnlock()

	if exists {
		entry.mu.Lock()
		entry.LastUsed = time.Now()
		entry.mu.Unlock()
		return entry, nil
	}

	// Try to open from disk
	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check after acquiring write lock
	if entry, exists := m.databases[name]; exists {
		entry.mu.Lock()
		entry.LastUsed = time.Now()
		entry.mu.Unlock()
		return entry, nil
	}

	dbPath := filepath.Join(m.dataDir, name+".sqlite")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("database %q not found", name)
	}

	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	entry = &DBEntry{
		DB:       db,
		Name:     name,
		Path:     dbPath,
		LastUsed: time.Now(),
	}

	m.databases[name] = entry
	return entry, nil
}

func (m *Manager) List() []DBEntry {
	m.mu.RLock()
	activeNames := make(map[string]bool)
	for name := range m.databases {
		activeNames[name] = true
	}
	m.mu.RUnlock()

	var result []DBEntry

	entries, err := os.ReadDir(m.dataDir)
	if err != nil {
		return result
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sqlite") {
			continue
		}

		name := strings.TrimSuffix(e.Name(), ".sqlite")
		info, _ := e.Info()
		dbPath := filepath.Join(m.dataDir, e.Name())

		entry := DBEntry{
			Name: name,
			Path: dbPath,
		}

		if info != nil {
			entry.LastUsed = info.ModTime()
		}

		if activeNames[name] {
			m.mu.RLock()
			if active, ok := m.databases[name]; ok {
				entry.DB = active.DB
				entry.LastUsed = active.LastUsed
			}
			m.mu.RUnlock()
		}

		result = append(result, entry)
	}

	return result
}

func (m *Manager) Drop(name string) error {
	if err := validateName(name); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if entry, exists := m.databases[name]; exists {
		if err := entry.DB.Close(); err != nil {
			return fmt.Errorf("failed to close database: %w", err)
		}
		delete(m.databases, name)
	}

	dbPath := filepath.Join(m.dataDir, name+".sqlite")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		os.Remove(dbPath + suffix)
	}

	return nil
}

func (m *Manager) CloseIdle(timeout time.Duration) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	closed := 0
	cutoff := time.Now().Add(-timeout)

	for name, entry := range m.databases {
		entry.mu.Lock()
		if entry.LastUsed.Before(cutoff) {
			entry.DB.Close()
			delete(m.databases, name)
			closed++
		}
		entry.mu.Unlock()
	}

	return closed
}

func (m *Manager) ActiveCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.databases)
}

func (m *Manager) CloseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, entry := range m.databases {
		entry.DB.Close()
		delete(m.databases, name)
	}
}

func (m *Manager) FileSize(name string) (int64, error) {
	dbPath := filepath.Join(m.dataDir, name+".sqlite")
	info, err := os.Stat(dbPath)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}
