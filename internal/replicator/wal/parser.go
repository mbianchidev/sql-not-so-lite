package wal

import (
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

const (
	walHeaderSize   = 32
	frameHeaderSize = 24

	// Magic numbers for SQLite WAL files.
	magicBigEndian    = 0x377f0682
	magicLittleEndian = 0x377f0683
)

var (
	ErrInvalidMagic = errors.New("wal: invalid magic number")
	ErrFileTooSmall = errors.New("wal: file too small for WAL header")
)

// WALHeader represents the 32-byte header at the start of a WAL file.
type WALHeader struct {
	Magic         uint32
	FormatVersion uint32
	PageSize      uint32
	CheckpointSeq uint32
	Salt1         uint32
	Salt2         uint32
}

// Frame represents a single WAL frame header (page data is skipped).
type Frame struct {
	PageNumber uint32
	CommitSize uint32 // >0 means this frame commits a transaction
	Salt1      uint32
	Salt2      uint32
}

// WALInfo contains the parsed result of a WAL file.
type WALInfo struct {
	Header       WALHeader
	Frames       []Frame
	TotalFrames  int
	CommitFrames int             // count of frames where CommitSize > 0
	ChangedPages map[uint32]bool // set of unique modified page numbers
}

// ParseOptions controls how WAL parsing behaves.
type ParseOptions struct {
	StartFrame int // skip frames before this index (0-based)
}

// ParseWALHeader reads and validates the 32-byte WAL header.
func ParseWALHeader(r io.ReaderAt) (*WALHeader, error) {
	var buf [walHeaderSize]byte
	n, err := r.ReadAt(buf[:], 0)
	if n < walHeaderSize {
		if err == nil {
			err = ErrFileTooSmall
		}
		return nil, fmt.Errorf("%w: got %d bytes", ErrFileTooSmall, n)
	}

	magic := binary.BigEndian.Uint32(buf[0:4])
	if magic != magicBigEndian && magic != magicLittleEndian {
		return nil, fmt.Errorf("%w: 0x%08x", ErrInvalidMagic, magic)
	}

	h := &WALHeader{
		Magic:         magic,
		FormatVersion: binary.BigEndian.Uint32(buf[4:8]),
		PageSize:      binary.BigEndian.Uint32(buf[8:12]),
		CheckpointSeq: binary.BigEndian.Uint32(buf[12:16]),
		Salt1:         binary.BigEndian.Uint32(buf[16:20]),
		Salt2:         binary.BigEndian.Uint32(buf[20:24]),
	}
	return h, nil
}

// ParseWAL parses a WAL from the given ReaderAt with known fileSize.
func ParseWAL(r io.ReaderAt, fileSize int64, opts *ParseOptions) (*WALInfo, error) {
	if fileSize < walHeaderSize {
		return nil, ErrFileTooSmall
	}

	header, err := ParseWALHeader(r)
	if err != nil {
		return nil, err
	}

	info := &WALInfo{
		Header:       *header,
		ChangedPages: make(map[uint32]bool),
	}

	pageSize := int64(header.PageSize)
	frameStride := frameHeaderSize + pageSize
	startFrame := 0
	if opts != nil && opts.StartFrame > 0 {
		startFrame = opts.StartFrame
	}

	offset := int64(walHeaderSize) + int64(startFrame)*frameStride

	var frameBuf [frameHeaderSize]byte
	for offset+frameHeaderSize <= fileSize {
		n, err := r.ReadAt(frameBuf[:], offset)
		if n < frameHeaderSize {
			break // partial frame header — writer may be mid-write
		}
		if err != nil && !errors.Is(err, io.EOF) {
			break
		}

		f := Frame{
			PageNumber: binary.BigEndian.Uint32(frameBuf[0:4]),
			CommitSize: binary.BigEndian.Uint32(frameBuf[4:8]),
			Salt1:      binary.BigEndian.Uint32(frameBuf[8:12]),
			Salt2:      binary.BigEndian.Uint32(frameBuf[12:16]),
		}

		// Skip frames from a previous WAL generation.
		if f.Salt1 != header.Salt1 || f.Salt2 != header.Salt2 {
			break
		}

		info.Frames = append(info.Frames, f)
		info.TotalFrames++
		info.ChangedPages[f.PageNumber] = true

		if f.CommitSize > 0 {
			info.CommitFrames++
		}

		offset += frameStride

		// If remaining bytes can't hold a full frame, stop.
		if offset+frameHeaderSize > fileSize {
			break
		}
	}

	return info, nil
}

// ParseWALFile is a convenience wrapper that opens a file and calls ParseWAL.
func ParseWALFile(path string, opts *ParseOptions) (*WALInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("wal: open %s: %w", path, err)
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("wal: stat %s: %w", path, err)
	}

	return ParseWAL(f, st.Size(), opts)
}

// MapPagesToTables returns table names whose root pages appear in changedPages.
// If page 1 (sqlite_master) changed, all tables are returned.
func MapPagesToTables(changedPages map[uint32]bool, rootPages map[uint32]string) []string {
	if changedPages[1] {
		tables := make([]string, 0, len(rootPages))
		for _, name := range rootPages {
			tables = append(tables, name)
		}
		return tables
	}

	var tables []string
	for pg, name := range rootPages {
		if changedPages[pg] {
			tables = append(tables, name)
		}
	}
	return tables
}

// ReadRootPages queries sqlite_master for user table root pages.
func ReadRootPages(db *sql.DB) (map[uint32]string, error) {
	rows, err := db.Query(`SELECT rootpage, name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return nil, fmt.Errorf("wal: query sqlite_master: %w", err)
	}
	defer rows.Close()

	m := make(map[uint32]string)
	for rows.Next() {
		var rootPage uint32
		var name string
		if err := rows.Scan(&rootPage, &name); err != nil {
			return nil, fmt.Errorf("wal: scan row: %w", err)
		}
		m[rootPage] = name
	}
	return m, rows.Err()
}
