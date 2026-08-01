package replicator

import (
	"path/filepath"
	"testing"
)

func TestIsManagedReplica(t *testing.T) {
	replicaDir := filepath.Join(t.TempDir(), "replicas")

	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "replica file", path: filepath.Join(replicaDir, "app.sqlite"), want: true},
		{name: "replica directory", path: replicaDir, want: true},
		{name: "sibling prefix", path: replicaDir + "-backup/app.sqlite", want: false},
		{name: "unrelated file", path: filepath.Join(filepath.Dir(replicaDir), "source.sqlite"), want: false},
		{name: "empty directory", path: filepath.Join(replicaDir, "app.sqlite"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := replicaDir
			if tt.name == "empty directory" {
				dir = ""
			}
			if got := IsManagedReplica(tt.path, dir); got != tt.want {
				t.Fatalf("IsManagedReplica(%q, %q) = %v, want %v", tt.path, dir, got, tt.want)
			}
		})
	}
}
