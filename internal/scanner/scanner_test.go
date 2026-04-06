package scanner

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/mbianchidev/sql-not-so-lite/internal/config"
	_ "modernc.org/sqlite"
)

func createTestDB(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}
	defer db.Close()

	// Create a table and insert data to ensure the file is large enough
	_, err = db.Exec("CREATE TABLE test (id INTEGER PRIMARY KEY, data TEXT)")
	if err != nil {
		t.Fatalf("failed to create table: %v", err)
	}
	_, err = db.Exec("INSERT INTO test (data) VALUES (?)", "test data to ensure minimum file size is met for scanning")
	if err != nil {
		t.Fatalf("failed to insert data: %v", err)
	}
}


func TestValidateSQLite(t *testing.T) {
	dir := t.TempDir()

	t.Run("valid SQLite file", func(t *testing.T) {
		dbPath := filepath.Join(dir, "valid.db")
		createTestDB(t, dbPath)

		if !ValidateSQLite(dbPath) {
			t.Error("expected valid SQLite file to be detected")
		}
	})

	t.Run("non-SQLite file", func(t *testing.T) {
		txtPath := filepath.Join(dir, "notadb.db")
		if err := os.WriteFile(txtPath, []byte("this is not a sqlite file at all!"), 0644); err != nil {
			t.Fatal(err)
		}

		if ValidateSQLite(txtPath) {
			t.Error("expected non-SQLite file to be rejected")
		}
	})

	t.Run("nonexistent file", func(t *testing.T) {
		if ValidateSQLite(filepath.Join(dir, "nope.db")) {
			t.Error("expected nonexistent file to be rejected")
		}
	})

	t.Run("too short file", func(t *testing.T) {
		shortPath := filepath.Join(dir, "short.db")
		if err := os.WriteFile(shortPath, []byte("short"), 0644); err != nil {
			t.Fatal(err)
		}
		if ValidateSQLite(shortPath) {
			t.Error("expected short file to be rejected")
		}
	})
}

func TestReadSQLiteHeader(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "header.db")
	createTestDB(t, dbPath)

	version, pageSize, err := ReadSQLiteHeader(dbPath)
	if err != nil {
		t.Fatalf("ReadSQLiteHeader failed: %v", err)
	}

	// Version should start with "3."
	if len(version) < 3 || version[:2] != "3." {
		t.Errorf("expected version starting with '3.', got %q", version)
	}

	// Page size should be a power of 2, typical defaults are 4096
	if pageSize < 512 || pageSize > 65536 {
		t.Errorf("expected page size between 512 and 65536, got %d", pageSize)
	}

	// Check it's a power of 2
	if pageSize&(pageSize-1) != 0 {
		t.Errorf("expected page size to be a power of 2, got %d", pageSize)
	}
}

func TestDetectGitHubRepo(t *testing.T) {
	t.Run("HTTPS remote", func(t *testing.T) {
		dir := t.TempDir()
		gitDir := filepath.Join(dir, "project", ".git")
		if err := os.MkdirAll(gitDir, 0755); err != nil {
			t.Fatal(err)
		}
		gitConfig := `[core]
	repositoryformatversion = 0
[remote "origin"]
	url = https://github.com/testowner/testrepo.git
	fetch = +refs/heads/*:refs/remotes/origin/*
[branch "main"]
	remote = origin
`
		if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(gitConfig), 0644); err != nil {
			t.Fatal(err)
		}

		dbPath := filepath.Join(dir, "project", "data", "test.db")
		if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dbPath, []byte("placeholder"), 0644); err != nil {
			t.Fatal(err)
		}

		repo, url := DetectGitHubRepo(dbPath)
		if repo != "testowner/testrepo" {
			t.Errorf("expected repo 'testowner/testrepo', got %q", repo)
		}
		if url != "https://github.com/testowner/testrepo" {
			t.Errorf("expected url 'https://github.com/testowner/testrepo', got %q", url)
		}
	})

	t.Run("SSH remote", func(t *testing.T) {
		dir := t.TempDir()
		gitDir := filepath.Join(dir, ".git")
		if err := os.MkdirAll(gitDir, 0755); err != nil {
			t.Fatal(err)
		}
		gitConfig := `[remote "origin"]
	url = git@github.com:sshowner/sshrepo.git
`
		if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(gitConfig), 0644); err != nil {
			t.Fatal(err)
		}

		dbPath := filepath.Join(dir, "test.db")
		if err := os.WriteFile(dbPath, []byte("placeholder"), 0644); err != nil {
			t.Fatal(err)
		}

		repo, url := DetectGitHubRepo(dbPath)
		if repo != "sshowner/sshrepo" {
			t.Errorf("expected repo 'sshowner/sshrepo', got %q", repo)
		}
		if url != "https://github.com/sshowner/sshrepo" {
			t.Errorf("expected url 'https://github.com/sshowner/sshrepo', got %q", url)
		}
	})

	t.Run("SSH URL remote", func(t *testing.T) {
		dir := t.TempDir()
		gitDir := filepath.Join(dir, ".git")
		if err := os.MkdirAll(gitDir, 0755); err != nil {
			t.Fatal(err)
		}
		gitConfig := `[remote "origin"]
	url = ssh://git@github.com/sshurl-owner/sshurl-repo.git
`
		if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(gitConfig), 0644); err != nil {
			t.Fatal(err)
		}

		dbPath := filepath.Join(dir, "test.db")
		if err := os.WriteFile(dbPath, []byte("placeholder"), 0644); err != nil {
			t.Fatal(err)
		}

		repo, url := DetectGitHubRepo(dbPath)
		if repo != "sshurl-owner/sshurl-repo" {
			t.Errorf("expected repo 'sshurl-owner/sshurl-repo', got %q", repo)
		}
		if url != "https://github.com/sshurl-owner/sshurl-repo" {
			t.Errorf("expected url 'https://github.com/sshurl-owner/sshurl-repo', got %q", url)
		}
	})

	t.Run("no git directory", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "test.db")
		if err := os.WriteFile(dbPath, []byte("placeholder"), 0644); err != nil {
			t.Fatal(err)
		}

		repo, url := DetectGitHubRepo(dbPath)
		if repo != "" || url != "" {
			t.Errorf("expected empty repo/url, got %q / %q", repo, url)
		}
	})

	t.Run("non-GitHub remote", func(t *testing.T) {
		dir := t.TempDir()
		gitDir := filepath.Join(dir, ".git")
		if err := os.MkdirAll(gitDir, 0755); err != nil {
			t.Fatal(err)
		}
		gitConfig := `[remote "origin"]
	url = https://gitlab.com/some/repo.git
`
		if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(gitConfig), 0644); err != nil {
			t.Fatal(err)
		}

		dbPath := filepath.Join(dir, "test.db")
		if err := os.WriteFile(dbPath, []byte("placeholder"), 0644); err != nil {
			t.Fatal(err)
		}

		repo, url := DetectGitHubRepo(dbPath)
		if repo != "" || url != "" {
			t.Errorf("expected empty repo/url for non-GitHub remote, got %q / %q", repo, url)
		}
	})
}

func TestClassifyPriority(t *testing.T) {
	s := New(config.ScannerConfig{
		PriorityPathsDocker:    []string{"/home/user/.docker", "/home/user/.orbstack"},
		PriorityPathsWorkspace: []string{"/home/user/workspace"},
		PriorityPathsCopilot:   []string{"/home/user/.copilot"},
		PriorityPathsAppData:   []string{"/home/user/Library/Application Support"},
		AppDataDotdirPattern:   "/home/user/.{name}/data",
	}, "")

	tests := []struct {
		path     string
		expected string
	}{
		{"/home/user/.docker/data/test.db", "docker"},
		{"/home/user/.orbstack/machines/data.db", "docker"},
		{"/home/user/workspace/project/app.db", "workspace"},
		{"/home/user/.copilot/worktrees/db.sqlite", "copilot"},
		{"/home/user/Library/Application Support/App/data.db", "app_data"},
		{"/home/user/.myapp/data/store.db", "app_data"},
		{"/home/user/random/place/file.db", "other"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := s.ClassifyPriority(tt.path)
			if got != tt.expected {
				t.Errorf("ClassifyPriority(%q) = %q, want %q", tt.path, got, tt.expected)
			}
		})
	}
}

func TestDetectJournalMode(t *testing.T) {
	t.Run("WAL mode", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "waltest.db")
		createTestDB(t, dbPath)

		// Simulate WAL sidecar file (driver cleans up on close)
		if err := os.WriteFile(dbPath+"-wal", []byte("wal"), 0644); err != nil {
			t.Fatal(err)
		}

		mode := DetectJournalMode(dbPath)
		if mode != "wal" {
			t.Errorf("expected 'wal', got %q", mode)
		}
	})

	t.Run("journal mode", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "journal.db")
		createTestDB(t, dbPath)

		// Simulate journal file
		if err := os.WriteFile(dbPath+"-journal", []byte("journal"), 0644); err != nil {
			t.Fatal(err)
		}

		mode := DetectJournalMode(dbPath)
		if mode != "delete" {
			t.Errorf("expected 'delete', got %q", mode)
		}
	})

	t.Run("unknown mode", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "unknown.db")
		createTestDB(t, dbPath)

		// Remove any sidecar files if created
		os.Remove(dbPath + "-wal")
		os.Remove(dbPath + "-shm")
		os.Remove(dbPath + "-journal")

		mode := DetectJournalMode(dbPath)
		if mode != "unknown" {
			t.Errorf("expected 'unknown', got %q", mode)
		}
	})
}

func TestScan(t *testing.T) {
	root := t.TempDir()

	// Create directory structures simulating different priority tiers
	dockerDir := filepath.Join(root, ".docker", "data")
	workspaceDir := filepath.Join(root, "workspace", "project")
	copilotDir := filepath.Join(root, ".copilot", "worktrees")
	otherDir := filepath.Join(root, "misc")
	ownDataDir := filepath.Join(root, "own-data")

	for _, d := range []string{dockerDir, workspaceDir, copilotDir, otherDir, ownDataDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	// Create valid SQLite databases
	createTestDB(t, filepath.Join(dockerDir, "containers.db"))
	createTestDB(t, filepath.Join(workspaceDir, "app.sqlite"))
	createTestDB(t, filepath.Join(copilotDir, "session.sqlite3"))
	createTestDB(t, filepath.Join(otherDir, "misc.db"))

	// Create a file in our own data dir (should be skipped)
	createTestDB(t, filepath.Join(ownDataDir, "internal.db"))

	// Create a non-SQLite .db file (should be filtered out)
	if err := os.WriteFile(filepath.Join(otherDir, "notadb.db"), []byte("this is definitely not a sqlite database file with enough padding to be big"), 0644); err != nil {
		t.Fatal(err)
	}
	// Pad the non-SQLite file to be above 4096 bytes
	f, err := os.OpenFile(filepath.Join(otherDir, "notadb.db"), os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	padding := make([]byte, 4096)
	f.Write(padding)
	f.Close()

	// Create a too-small file (should be skipped)
	if err := os.WriteFile(filepath.Join(otherDir, "tiny.db"), []byte("small"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := config.ScannerConfig{
		ScanRoot:               root,
		FileExtensions:         []string{".sqlite", ".db", ".sqlite3", ".sqlitedb"},
		ExcludePatterns:        []string{},
		PriorityPathsDocker:    []string{filepath.Join(root, ".docker")},
		PriorityPathsWorkspace: []string{filepath.Join(root, "workspace")},
		PriorityPathsCopilot:   []string{filepath.Join(root, ".copilot")},
		PriorityPathsAppData:   []string{},
		AppDataDotdirPattern:   "",
		ScanInterval:           "1h",
	}

	s := New(cfg, ownDataDir)
	results, err := s.Scan()
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	// Should find exactly 4 files (docker, workspace, copilot, other)
	if len(results) != 4 {
		t.Errorf("expected 4 results, got %d", len(results))
		for _, r := range results {
			t.Logf("  found: %s (priority: %s)", r.Path, r.Priority)
		}
	}

	// Verify sort order: docker, workspace, copilot, other
	expectedOrder := []string{"docker", "workspace", "copilot", "other"}
	for i, expected := range expectedOrder {
		if i < len(results) && results[i].Priority != expected {
			t.Errorf("result[%d] priority = %q, want %q", i, results[i].Priority, expected)
		}
	}

	// Verify each result has valid metadata
	for _, r := range results {
		if r.Name == "" {
			t.Error("expected non-empty Name")
		}
		if r.SizeBytes < 4096 {
			t.Errorf("expected size >= 4096, got %d for %s", r.SizeBytes, r.Path)
		}
		if r.SQLiteVersion == "" {
			t.Errorf("expected non-empty SQLiteVersion for %s", r.Path)
		}
		if r.PageSize == 0 {
			t.Errorf("expected non-zero PageSize for %s", r.Path)
		}
	}

	// Verify non-SQLite and too-small files are excluded
	for _, r := range results {
		if r.Name == "notadb" {
			t.Error("non-SQLite file should have been filtered out")
		}
		if r.Name == "tiny" {
			t.Error("too-small file should have been filtered out")
		}
		if r.Name == "internal" {
			t.Error("own data dir file should have been filtered out")
		}
	}
}

func TestScanExcludePatterns(t *testing.T) {
	root := t.TempDir()

	// Create directories
	normalDir := filepath.Join(root, "normal")
	nodeModDir := filepath.Join(root, "project", "node_modules", "pkg")

	for _, d := range []string{normalDir, nodeModDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	// Create SQLite files
	createTestDB(t, filepath.Join(normalDir, "keep.db"))
	createTestDB(t, filepath.Join(nodeModDir, "skip.db"))

	cfg := config.ScannerConfig{
		ScanRoot:        root,
		FileExtensions:  []string{".db"},
		ExcludePatterns: []string{"node_modules"},
		ScanInterval:    "1h",
	}

	s := New(cfg, "")
	results, err := s.Scan()
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
		for _, r := range results {
			t.Logf("  found: %s", r.Path)
		}
	}

	if len(results) > 0 && results[0].Name != "keep" {
		t.Errorf("expected 'keep', got %q", results[0].Name)
	}
}
