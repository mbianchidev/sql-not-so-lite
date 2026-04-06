package wal

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestParseWALFile_RealDB(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		t.Fatalf("set WAL mode: %v", err)
	}
	if _, err := db.Exec("PRAGMA wal_autocheckpoint=0"); err != nil {
		t.Fatalf("disable autocheckpoint: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users (name) VALUES ('alice'), ('bob')`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Parse the WAL while the connection is still open (before checkpoint).
	walPath := dbPath + "-wal"
	if _, err := os.Stat(walPath); os.IsNotExist(err) {
		t.Skip("WAL file not created — SQLite may have auto-checkpointed")
	}

	info, err := ParseWALFile(walPath, nil)
	if err != nil {
		t.Fatalf("ParseWALFile: %v", err)
	}

	if info.Header.Magic != magicBigEndian && info.Header.Magic != magicLittleEndian {
		t.Errorf("unexpected magic: 0x%08x", info.Header.Magic)
	}
	if info.Header.PageSize == 0 {
		t.Error("page size is 0")
	}
	if info.TotalFrames == 0 {
		t.Error("expected at least one frame")
	}
	if info.CommitFrames == 0 {
		t.Error("expected at least one commit frame")
	}
	if len(info.ChangedPages) == 0 {
		t.Error("expected at least one changed page")
	}
	// Creating tables modifies page 1 (sqlite_master).
	if !info.ChangedPages[1] {
		t.Error("expected page 1 to be in changed pages")
	}
}

func TestParseWALHeader_InvalidMagic(t *testing.T) {
	var buf [walHeaderSize]byte
	binary.BigEndian.PutUint32(buf[0:4], 0xDEADBEEF)

	r := bytes.NewReader(buf[:])
	_, err := ParseWALHeader(r)
	if err == nil {
		t.Fatal("expected error for invalid magic")
	}
}

func TestParseWALHeader_TooSmall(t *testing.T) {
	r := bytes.NewReader([]byte{0x37, 0x7f, 0x06})
	_, err := ParseWALHeader(r)
	if err == nil {
		t.Fatal("expected error for too-small input")
	}
}

func TestParseWAL_EmptyWAL(t *testing.T) {
	// WAL with valid header but no frames.
	var buf [walHeaderSize]byte
	binary.BigEndian.PutUint32(buf[0:4], magicBigEndian)
	binary.BigEndian.PutUint32(buf[4:8], 3007000)
	binary.BigEndian.PutUint32(buf[8:12], 4096) // page size
	binary.BigEndian.PutUint32(buf[16:20], 0xAA)
	binary.BigEndian.PutUint32(buf[20:24], 0xBB)

	r := bytes.NewReader(buf[:])
	info, err := ParseWAL(r, int64(len(buf)), nil)
	if err != nil {
		t.Fatalf("ParseWAL: %v", err)
	}
	if info.TotalFrames != 0 {
		t.Errorf("expected 0 frames, got %d", info.TotalFrames)
	}
	if info.CommitFrames != 0 {
		t.Errorf("expected 0 commit frames, got %d", info.CommitFrames)
	}
}

func TestParseWAL_WithFrames(t *testing.T) {
	const pageSize = 512
	salt1, salt2 := uint32(0xAA), uint32(0xBB)

	// Build a synthetic WAL: header + 3 frames.
	var buf bytes.Buffer

	// Header
	header := make([]byte, walHeaderSize)
	binary.BigEndian.PutUint32(header[0:4], magicBigEndian)
	binary.BigEndian.PutUint32(header[4:8], 3007000)
	binary.BigEndian.PutUint32(header[8:12], pageSize)
	binary.BigEndian.PutUint32(header[16:20], salt1)
	binary.BigEndian.PutUint32(header[20:24], salt2)
	buf.Write(header)

	writeFrame := func(pageNum, commitSize, s1, s2 uint32) {
		fh := make([]byte, frameHeaderSize)
		binary.BigEndian.PutUint32(fh[0:4], pageNum)
		binary.BigEndian.PutUint32(fh[4:8], commitSize)
		binary.BigEndian.PutUint32(fh[8:12], s1)
		binary.BigEndian.PutUint32(fh[12:16], s2)
		buf.Write(fh)
		buf.Write(make([]byte, pageSize)) // dummy page data
	}

	writeFrame(1, 0, salt1, salt2)  // frame 0: page 1, no commit
	writeFrame(5, 0, salt1, salt2)  // frame 1: page 5, no commit
	writeFrame(1, 3, salt1, salt2)  // frame 2: page 1, commit (db size = 3 pages)

	data := buf.Bytes()
	r := bytes.NewReader(data)

	info, err := ParseWAL(r, int64(len(data)), nil)
	if err != nil {
		t.Fatalf("ParseWAL: %v", err)
	}

	if info.TotalFrames != 3 {
		t.Errorf("expected 3 frames, got %d", info.TotalFrames)
	}
	if info.CommitFrames != 1 {
		t.Errorf("expected 1 commit frame, got %d", info.CommitFrames)
	}
	if !info.ChangedPages[1] || !info.ChangedPages[5] {
		t.Errorf("expected pages 1 and 5 in changed set, got %v", info.ChangedPages)
	}
	if len(info.ChangedPages) != 2 {
		t.Errorf("expected 2 unique changed pages, got %d", len(info.ChangedPages))
	}
}

func TestParseWAL_SaltMismatch(t *testing.T) {
	const pageSize = 512
	salt1, salt2 := uint32(0xAA), uint32(0xBB)

	var buf bytes.Buffer
	header := make([]byte, walHeaderSize)
	binary.BigEndian.PutUint32(header[0:4], magicBigEndian)
	binary.BigEndian.PutUint32(header[4:8], 3007000)
	binary.BigEndian.PutUint32(header[8:12], pageSize)
	binary.BigEndian.PutUint32(header[16:20], salt1)
	binary.BigEndian.PutUint32(header[20:24], salt2)
	buf.Write(header)

	// Frame with matching salt.
	fh1 := make([]byte, frameHeaderSize)
	binary.BigEndian.PutUint32(fh1[0:4], 2)
	binary.BigEndian.PutUint32(fh1[8:12], salt1)
	binary.BigEndian.PutUint32(fh1[12:16], salt2)
	buf.Write(fh1)
	buf.Write(make([]byte, pageSize))

	// Frame with mismatched salt (old generation).
	fh2 := make([]byte, frameHeaderSize)
	binary.BigEndian.PutUint32(fh2[0:4], 3)
	binary.BigEndian.PutUint32(fh2[8:12], 0xFF)
	binary.BigEndian.PutUint32(fh2[12:16], 0xFF)
	buf.Write(fh2)
	buf.Write(make([]byte, pageSize))

	data := buf.Bytes()
	info, err := ParseWAL(bytes.NewReader(data), int64(len(data)), nil)
	if err != nil {
		t.Fatalf("ParseWAL: %v", err)
	}

	if info.TotalFrames != 1 {
		t.Errorf("expected 1 frame (salt mismatch stops parsing), got %d", info.TotalFrames)
	}
}

func TestParseWAL_StartFrame(t *testing.T) {
	const pageSize = 512
	salt1, salt2 := uint32(0xAA), uint32(0xBB)

	var buf bytes.Buffer
	header := make([]byte, walHeaderSize)
	binary.BigEndian.PutUint32(header[0:4], magicBigEndian)
	binary.BigEndian.PutUint32(header[4:8], 3007000)
	binary.BigEndian.PutUint32(header[8:12], pageSize)
	binary.BigEndian.PutUint32(header[16:20], salt1)
	binary.BigEndian.PutUint32(header[20:24], salt2)
	buf.Write(header)

	writeFrame := func(pageNum uint32) {
		fh := make([]byte, frameHeaderSize)
		binary.BigEndian.PutUint32(fh[0:4], pageNum)
		binary.BigEndian.PutUint32(fh[8:12], salt1)
		binary.BigEndian.PutUint32(fh[12:16], salt2)
		buf.Write(fh)
		buf.Write(make([]byte, pageSize))
	}

	writeFrame(10) // frame 0
	writeFrame(20) // frame 1
	writeFrame(30) // frame 2

	data := buf.Bytes()
	info, err := ParseWAL(bytes.NewReader(data), int64(len(data)), &ParseOptions{StartFrame: 2})
	if err != nil {
		t.Fatalf("ParseWAL: %v", err)
	}

	if info.TotalFrames != 1 {
		t.Errorf("expected 1 frame (skipped 2), got %d", info.TotalFrames)
	}
	if !info.ChangedPages[30] {
		t.Errorf("expected page 30, got %v", info.ChangedPages)
	}
}

func TestParseWAL_PartialFrame(t *testing.T) {
	const pageSize = 512
	salt1, salt2 := uint32(0xAA), uint32(0xBB)

	var buf bytes.Buffer
	header := make([]byte, walHeaderSize)
	binary.BigEndian.PutUint32(header[0:4], magicBigEndian)
	binary.BigEndian.PutUint32(header[4:8], 3007000)
	binary.BigEndian.PutUint32(header[8:12], pageSize)
	binary.BigEndian.PutUint32(header[16:20], salt1)
	binary.BigEndian.PutUint32(header[20:24], salt2)
	buf.Write(header)

	// One valid frame.
	fh := make([]byte, frameHeaderSize)
	binary.BigEndian.PutUint32(fh[0:4], 7)
	binary.BigEndian.PutUint32(fh[8:12], salt1)
	binary.BigEndian.PutUint32(fh[12:16], salt2)
	buf.Write(fh)
	buf.Write(make([]byte, pageSize))

	// Partial second frame header (only 10 bytes).
	buf.Write(make([]byte, 10))

	data := buf.Bytes()
	info, err := ParseWAL(bytes.NewReader(data), int64(len(data)), nil)
	if err != nil {
		t.Fatalf("ParseWAL: %v", err)
	}
	if info.TotalFrames != 1 {
		t.Errorf("expected 1 frame (partial ignored), got %d", info.TotalFrames)
	}
}

func TestMapPagesToTables(t *testing.T) {
	rootPages := map[uint32]string{
		2: "users",
		3: "events",
		4: "config",
	}

	t.Run("specific pages", func(t *testing.T) {
		changed := map[uint32]bool{2: true, 4: true}
		tables := MapPagesToTables(changed, rootPages)
		if len(tables) != 2 {
			t.Fatalf("expected 2 tables, got %d", len(tables))
		}
		got := map[string]bool{}
		for _, n := range tables {
			got[n] = true
		}
		if !got["users"] || !got["config"] {
			t.Errorf("expected users and config, got %v", tables)
		}
	})

	t.Run("page 1 returns all tables", func(t *testing.T) {
		changed := map[uint32]bool{1: true}
		tables := MapPagesToTables(changed, rootPages)
		if len(tables) != 3 {
			t.Fatalf("expected all 3 tables when page 1 changed, got %d: %v", len(tables), tables)
		}
	})

	t.Run("no matching pages", func(t *testing.T) {
		changed := map[uint32]bool{99: true}
		tables := MapPagesToTables(changed, rootPages)
		if len(tables) != 0 {
			t.Errorf("expected 0 tables, got %v", tables)
		}
	})
}

func TestReadRootPages(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE users (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE events (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	m, err := ReadRootPages(db)
	if err != nil {
		t.Fatalf("ReadRootPages: %v", err)
	}

	if len(m) != 2 {
		t.Fatalf("expected 2 root pages, got %d: %v", len(m), m)
	}

	names := make(map[string]bool)
	for _, name := range m {
		names[name] = true
	}
	if !names["users"] || !names["events"] {
		t.Errorf("expected users and events, got %v", m)
	}
}

func TestParseWALFile_NotExist(t *testing.T) {
	_, err := ParseWALFile("/nonexistent/wal/file", nil)
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}
