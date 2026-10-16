package session

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

// IsCodexSessionFilename reports whether name is rollout-*-<id>.jsonl.
func IsCodexSessionFilename(name, id string) bool {
	return strings.HasPrefix(name, "rollout-") && strings.HasSuffix(name, "-"+id+".jsonl")
}

func findCodexPath(root, id string) string {
	var found string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && IsCodexSessionFilename(d.Name(), id) {
			found = p
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// FindCodexOnHost locates a Codex rollout file by id under the date-nested sessions dir.
func FindCodexOnHost(id, sessionsDir string) HostSessionLookup {
	root := orDefault(sessionsDir, DefaultCodexSessionsDir)
	return HostSessionLookup{Path: findCodexPath(root, id), SearchedRoot: root}
}

// LocateCodexOnHost is FindCodexOnHost plus the relative path, failing when absent.
func LocateCodexOnHost(id, sessionsDir string) (Location, error) {
	root := orDefault(sessionsDir, DefaultCodexSessionsDir)
	p := findCodexPath(root, id)
	if p == "" {
		return Location{}, fmt.Errorf("session %s not found in %s", id, root)
	}
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return Location{}, err
	}
	return Location{Path: p, RelativePath: rel}, nil
}
