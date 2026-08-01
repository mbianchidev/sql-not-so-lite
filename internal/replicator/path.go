package replicator

import (
	"path/filepath"
	"strings"
)

// IsManagedReplica reports whether path is the replica directory or is inside it.
func IsManagedReplica(path, replicaDir string) bool {
	if path == "" || replicaDir == "" {
		return false
	}

	cleanPath := canonicalPath(path)
	cleanReplicaDir := canonicalPath(replicaDir)

	if cleanPath == cleanReplicaDir {
		return true
	}
	return strings.HasPrefix(cleanPath, cleanReplicaDir+string(filepath.Separator))
}

func canonicalPath(path string) string {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	if resolvedPath, err := filepath.EvalSymlinks(absolutePath); err == nil {
		return resolvedPath
	}
	return filepath.Clean(absolutePath)
}
