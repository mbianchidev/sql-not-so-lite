package etcqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const interchangeFixtureDDL = `
CREATE TABLE interchange_meta(kind TEXT,format INTEGER,reference TEXT,revision INTEGER,compact_revision INTEGER);
INSERT INTO interchange_meta VALUES ('bbolt-interchange',2,'3.7.3',42,-1);
CREATE TABLE interchange_buckets(name BLOB PRIMARY KEY);
CREATE TABLE interchange_entries(bucket BLOB,key BLOB,value BLOB,PRIMARY KEY(bucket,key));
INSERT INTO interchange_buckets VALUES (x'61757468');
INSERT INTO interchange_entries VALUES (x'61757468',x'66697874757265',x'001122ff');
`

func TestInterchangeArtifactsAreProtectedWithoutReadingEntries(t *testing.T) {
	for _, poisonEntries := range []bool{false, true} {
		name := "full artifact"
		if poisonEntries {
			name = "unreadable entry view"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "renamed.sqlite")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(interchangeFixtureDDL); err != nil {
				db.Close()
				t.Fatal(err)
			}
			if poisonEntries {
				if _, err := db.Exec(`DROP TABLE interchange_entries; CREATE VIEW interchange_entries AS SELECT * FROM missing_sensitive_source`); err != nil {
					db.Close()
					t.Fatal(err)
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			footer := sha256.Sum256(original)
			original = append(original, footer[:]...)
			if err := os.WriteFile(path, original, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := CheckOrdinaryFile(context.Background(), path); !errors.Is(err, ErrProtected) {
				t.Fatalf("sensitive interchange artifact was not protected: %v", err)
			}
			after, err := os.ReadFile(path)
			if err != nil || sha256.Sum256(after) != sha256.Sum256(original) {
				t.Fatal("schema/marker classification changed the interchange artifact")
			}
		})
	}
}

func TestOrdinaryInterchangeNameCollisionIsNotProtected(t *testing.T) {
	for _, ddl := range []string{
		`CREATE TABLE interchange_meta(note TEXT)`,
		`CREATE TABLE interchange_meta(kind TEXT,format INTEGER,reference TEXT,revision INTEGER,compact_revision INTEGER);
		 INSERT INTO interchange_meta VALUES ('ordinary-application',2,'3.7.3',42,-1)`,
	} {
		path := filepath.Join(t.TempDir(), "ordinary.sqlite")
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ddl); err != nil {
			db.Close()
			t.Fatal(err)
		}
		db.Close()
		if err := CheckOrdinaryFile(context.Background(), path); err != nil {
			t.Fatalf("ordinary database was rejected by an interchange name collision: %v", err)
		}
	}
}
