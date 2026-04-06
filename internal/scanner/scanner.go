package scanner

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mbianchidev/sql-not-so-lite/internal/config"
)

type DiscoveredFile struct {
	Path          string
	Name          string
	SizeBytes     int64
	LastModified  time.Time
	SQLiteVersion string
	PageSize      int
	JournalMode   string
	GitHubRepo    string
	GitHubURL     string
	Priority      string
}

type Scanner struct {
	cfg        config.ScannerConfig
	ownDataDir string
}

var priorityOrder = map[string]int{
	"docker":    0,
	"workspace": 1,
	"copilot":   2,
	"app_data":  3,
	"other":     4,
}

func New(cfg config.ScannerConfig, ownDataDir string) *Scanner {
	return &Scanner{cfg: cfg, ownDataDir: ownDataDir}
}

func (s *Scanner) Scan() ([]DiscoveredFile, error) {
	var results []DiscoveredFile

	err := filepath.WalkDir(s.cfg.ScanRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Skip unreadable directories gracefully
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		if d.IsDir() {
			// Skip our own data directory
			if s.ownDataDir != "" && path == s.ownDataDir {
				return fs.SkipDir
			}
			// Skip directories matching exclude patterns
			for _, pat := range s.cfg.ExcludePatterns {
				if matchesExcludePattern(path, pat) {
					return fs.SkipDir
				}
			}
			return nil
		}

		// Skip WAL/SHM sidecar files
		if strings.HasSuffix(path, "-wal") || strings.HasSuffix(path, "-shm") {
			return nil
		}

		// Check file extension
		if !hasMatchingExtension(path, s.cfg.FileExtensions) {
			return nil
		}

		// Check exclude patterns for files
		for _, pat := range s.cfg.ExcludePatterns {
			if matchesExcludePattern(path, pat) {
				return nil
			}
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		// Skip files smaller than 4096 bytes
		if info.Size() < 4096 {
			return nil
		}

		// Validate SQLite magic bytes
		if !ValidateSQLite(path) {
			return nil
		}

		version, pageSize, headerErr := ReadSQLiteHeader(path)
		if headerErr != nil {
			return nil
		}

		repo, url := DetectGitHubRepo(path)
		priority := s.ClassifyPriority(path)
		journalMode := DetectJournalMode(path)

		ext := filepath.Ext(path)
		name := strings.TrimSuffix(filepath.Base(path), ext)

		results = append(results, DiscoveredFile{
			Path:          path,
			Name:          name,
			SizeBytes:     info.Size(),
			LastModified:  info.ModTime(),
			SQLiteVersion: version,
			PageSize:      pageSize,
			JournalMode:   journalMode,
			GitHubRepo:    repo,
			GitHubURL:     url,
			Priority:      priority,
		})

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan failed: %w", err)
	}

	sort.Slice(results, func(i, j int) bool {
		pi := priorityOrder[results[i].Priority]
		pj := priorityOrder[results[j].Priority]
		if pi != pj {
			return pi < pj
		}
		return results[i].Name < results[j].Name
	})

	return results, nil
}

func ValidateSQLite(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	header := make([]byte, 16)
	n, err := f.Read(header)
	if err != nil || n < 16 {
		return false
	}

	return string(header) == "SQLite format 3\000"
}

func ReadSQLiteHeader(path string) (version string, pageSize int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()

	header := make([]byte, 100)
	n, err := f.Read(header)
	if err != nil || n < 100 {
		return "", 0, fmt.Errorf("failed to read SQLite header: short read (%d bytes)", n)
	}

	// Page size: bytes 16-17, big-endian uint16
	ps := binary.BigEndian.Uint16(header[16:18])
	if ps == 1 {
		pageSize = 65536
	} else {
		pageSize = int(ps)
	}

	// SQLite version: bytes 96-99, big-endian uint32
	ver := binary.BigEndian.Uint32(header[96:100])
	major := ver / 1000000
	minor := (ver % 1000000) / 1000
	patch := ver % 1000
	version = fmt.Sprintf("%d.%d.%d", major, minor, patch)

	return version, pageSize, nil
}

func DetectGitHubRepo(filePath string) (repo, url string) {
	dir := filepath.Dir(filePath)

	for {
		gitConfig := filepath.Join(dir, ".git", "config")
		if _, err := os.Stat(gitConfig); err == nil {
			r, u := parseGitConfig(gitConfig)
			if r != "" {
				return r, u
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", ""
}

func parseGitConfig(path string) (repo, url string) {
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	inOrigin := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line == `[remote "origin"]` {
			inOrigin = true
			continue
		}

		// New section starts
		if strings.HasPrefix(line, "[") {
			inOrigin = false
			continue
		}

		if inOrigin && strings.HasPrefix(line, "url") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) != 2 {
				continue
			}
			rawURL := strings.TrimSpace(parts[1])
			return extractGitHubRepo(rawURL)
		}
	}

	return "", ""
}

func extractGitHubRepo(rawURL string) (repo, ghURL string) {
	rawURL = strings.TrimSuffix(rawURL, ".git")

	// HTTPS: https://github.com/owner/repo
	if strings.HasPrefix(rawURL, "https://github.com/") {
		path := strings.TrimPrefix(rawURL, "https://github.com/")
		parts := strings.SplitN(path, "/", 3)
		if len(parts) >= 2 && parts[0] != "" && parts[1] != "" {
			repo = parts[0] + "/" + parts[1]
			return repo, "https://github.com/" + repo
		}
	}

	// SSH shorthand: git@github.com:owner/repo
	if strings.HasPrefix(rawURL, "git@github.com:") {
		path := strings.TrimPrefix(rawURL, "git@github.com:")
		parts := strings.SplitN(path, "/", 3)
		if len(parts) >= 2 && parts[0] != "" && parts[1] != "" {
			repo = parts[0] + "/" + parts[1]
			return repo, "https://github.com/" + repo
		}
	}

	// SSH URL: ssh://git@github.com/owner/repo
	if strings.HasPrefix(rawURL, "ssh://git@github.com/") {
		path := strings.TrimPrefix(rawURL, "ssh://git@github.com/")
		parts := strings.SplitN(path, "/", 3)
		if len(parts) >= 2 && parts[0] != "" && parts[1] != "" {
			repo = parts[0] + "/" + parts[1]
			return repo, "https://github.com/" + repo
		}
	}

	return "", ""
}

func (s *Scanner) ClassifyPriority(path string) string {
	for _, p := range s.cfg.PriorityPathsDocker {
		if isUnder(path, p) {
			return "docker"
		}
	}
	for _, p := range s.cfg.PriorityPathsWorkspace {
		if isUnder(path, p) {
			return "workspace"
		}
	}
	for _, p := range s.cfg.PriorityPathsCopilot {
		if isUnder(path, p) {
			return "copilot"
		}
	}
	for _, p := range s.cfg.PriorityPathsAppData {
		if isUnder(path, p) {
			return "app_data"
		}
	}

	// Check dotdir pattern: e.g. ~/.{repo-name}/data/
	if s.cfg.AppDataDotdirPattern != "" {
		if matchesDotdirPattern(path, s.cfg.AppDataDotdirPattern) {
			return "app_data"
		}
	}

	return "other"
}

func DetectJournalMode(dbPath string) string {
	if _, err := os.Stat(dbPath + "-wal"); err == nil {
		return "wal"
	}
	if _, err := os.Stat(dbPath + "-journal"); err == nil {
		return "delete"
	}
	return "unknown"
}

func hasMatchingExtension(path string, extensions []string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	for _, e := range extensions {
		if ext == strings.ToLower(e) {
			return true
		}
	}
	return false
}

func matchesExcludePattern(path string, pattern string) bool {
	// Simple substring check: if the pattern appears in the path
	return strings.Contains(path, pattern)
}

func isUnder(path, prefix string) bool {
	if prefix == "" {
		return false
	}
	// Ensure prefix ends with separator for proper matching
	cleanPath := filepath.Clean(path)
	cleanPrefix := filepath.Clean(prefix)
	if cleanPath == cleanPrefix {
		return true
	}
	return strings.HasPrefix(cleanPath, cleanPrefix+string(filepath.Separator))
}

// matchesDotdirPattern checks if a path matches a pattern like /home/user/.{name}/data/
// where {name} is any directory name starting with a dot.
func matchesDotdirPattern(path string, pattern string) bool {
	// Pattern is something like /home/user/.{repo-name}/data
	// We need to find the placeholder and match the structure.
	idx := strings.Index(pattern, "{")
	if idx < 0 {
		return false
	}
	endIdx := strings.Index(pattern, "}")
	if endIdx < 0 {
		return false
	}

	prefix := pattern[:idx]  // e.g. "/home/user/."
	suffix := pattern[endIdx+1:] // e.g. "/data"

	if !strings.HasPrefix(path, prefix) {
		return false
	}

	rest := path[len(prefix):]
	if suffix == "" {
		return true
	}

	// Find the suffix in the rest
	suffixClean := strings.TrimSuffix(suffix, "/")
	sepIdx := strings.Index(rest, suffixClean)
	if sepIdx < 0 {
		return false
	}

	// The part before the suffix should be a single directory name (no separators)
	namePart := rest[:sepIdx]
	if namePart == "" || strings.Contains(namePart, string(filepath.Separator)) {
		return false
	}

	return true
}
