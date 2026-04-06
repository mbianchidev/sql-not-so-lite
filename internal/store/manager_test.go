package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setupTestManager(t *testing.T) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	m, err := NewManager(dir, 10)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}
	return m, dir
}

func TestCreateDatabase(t *testing.T) {
	m, dir := setupTestManager(t)
	defer m.CloseAll()

	entry, err := m.Create("testdb")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if entry.Name != "testdb" {
		t.Errorf("expected name 'testdb', got %q", entry.Name)
	}

	expectedPath := filepath.Join(dir, "testdb.sqlite")
	if entry.Path != expectedPath {
		t.Errorf("expected path %q, got %q", expectedPath, entry.Path)
	}

	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Error("database file was not created")
	}
}

func TestCreateDuplicate(t *testing.T) {
	m, _ := setupTestManager(t)
	defer m.CloseAll()

	if _, err := m.Create("testdb"); err != nil {
		t.Fatalf("first Create failed: %v", err)
	}

	if _, err := m.Create("testdb"); err == nil {
		t.Error("expected error creating duplicate database")
	}
}

func TestCreateInvalidName(t *testing.T) {
	m, _ := setupTestManager(t)
	defer m.CloseAll()

	invalidNames := []string{"", "..", ".", "test/db", "test\\db", "test:db"}
	for _, name := range invalidNames {
		if _, err := m.Create(name); err == nil {
			t.Errorf("expected error for invalid name %q", name)
		}
	}
}

func TestGetDatabase(t *testing.T) {
	m, _ := setupTestManager(t)
	defer m.CloseAll()

	if _, err := m.Create("testdb"); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	entry, err := m.Get("testdb")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if entry.Name != "testdb" {
		t.Errorf("expected name 'testdb', got %q", entry.Name)
	}

	if entry.DB == nil {
		t.Error("expected non-nil DB")
	}
}

func TestGetNonExistent(t *testing.T) {
	m, _ := setupTestManager(t)
	defer m.CloseAll()

	if _, err := m.Get("nonexistent"); err == nil {
		t.Error("expected error getting non-existent database")
	}
}

func TestGetLazyLoad(t *testing.T) {
	m, dir := setupTestManager(t)

	// Create a DB, close all connections, then Get should reopen
	if _, err := m.Create("testdb"); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	m.CloseAll()

	// Verify file exists
	if _, err := os.Stat(filepath.Join(dir, "testdb.sqlite")); os.IsNotExist(err) {
		t.Fatal("database file does not exist")
	}

	entry, err := m.Get("testdb")
	if err != nil {
		t.Fatalf("Get after CloseAll failed: %v", err)
	}

	if entry.DB == nil {
		t.Error("expected non-nil DB after lazy load")
	}
}

func TestListDatabases(t *testing.T) {
	m, _ := setupTestManager(t)
	defer m.CloseAll()

	for _, name := range []string{"alpha", "beta", "gamma"} {
		if _, err := m.Create(name); err != nil {
			t.Fatalf("Create %s failed: %v", name, err)
		}
	}

	list := m.List()
	if len(list) != 3 {
		t.Fatalf("expected 3 databases, got %d", len(list))
	}
}

func TestDropDatabase(t *testing.T) {
	m, dir := setupTestManager(t)
	defer m.CloseAll()

	if _, err := m.Create("testdb"); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if err := m.Drop("testdb"); err != nil {
		t.Fatalf("Drop failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "testdb.sqlite")); !os.IsNotExist(err) {
		t.Error("database file should have been removed")
	}

	if m.ActiveCount() != 0 {
		t.Error("expected 0 active databases after drop")
	}
}

func TestCloseIdle(t *testing.T) {
	m, _ := setupTestManager(t)
	defer m.CloseAll()

	if _, err := m.Create("testdb"); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if m.ActiveCount() != 1 {
		t.Fatalf("expected 1 active, got %d", m.ActiveCount())
	}

	closed := m.CloseIdle(0 * time.Second)
	if closed != 1 {
		t.Errorf("expected 1 closed, got %d", closed)
	}

	if m.ActiveCount() != 0 {
		t.Errorf("expected 0 active after close idle, got %d", m.ActiveCount())
	}
}

func TestMaxDatabases(t *testing.T) {
	m, _ := setupTestManager(t)
	defer m.CloseAll()

	// Max is 10
	for i := 0; i < 10; i++ {
		if _, err := m.Create(filepath.Base(t.Name()) + string(rune('a'+i))); err != nil {
			t.Fatalf("Create %d failed: %v", i, err)
		}
	}

	if _, err := m.Create("overflow"); err == nil {
		t.Error("expected error when exceeding max databases")
	}
}
