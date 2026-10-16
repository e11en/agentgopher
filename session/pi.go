package session

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var piLeadingSep = regexp.MustCompile(`^[/\\]`)
var piDrive = regexp.MustCompile(`^([A-Za-z]):[/\\]+`)
var piReplace = regexp.MustCompile(`[/\\:]`)

// EncodePiSessionDir encodes a cwd the way pi names its session directory:
// leading separator stripped, separators and drive colons replaced by "-",
// wrapped in "--" markers. Verified against pi 0.73.1.
func EncodePiSessionDir(cwd string) string {
	stripped := piDrive.ReplaceAllString(piLeadingSep.ReplaceAllString(cwd, ""), "$1-")
	return "--" + piReplace.ReplaceAllString(stripped, "-") + "--"
}

// PiSessionDirPath is the host session directory for cwd.
func PiSessionDirPath(cwd, sessionsDir string) string {
	return filepath.Join(orDefault(sessionsDir, DefaultPiSessionsDir), EncodePiSessionDir(cwd))
}

// IsPiSessionFilename reports whether name is *_<id>.jsonl.
func IsPiSessionFilename(name, id string) bool { return strings.HasSuffix(name, "_"+id+".jsonl") }

func findPi(root, id string) (Location, bool) {
	dirs, err := os.ReadDir(root)
	if err != nil {
		return Location{}, false
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(root, d.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if IsPiSessionFilename(f.Name(), id) {
				return Location{Path: filepath.Join(root, d.Name(), f.Name()), RelativePath: filepath.Join(d.Name(), f.Name())}, true
			}
		}
	}
	return Location{}, false
}

// FindPiOnHost locates a pi session by id across all encoded cwd directories.
func FindPiOnHost(id, sessionsDir string) HostSessionLookup {
	root := orDefault(sessionsDir, DefaultPiSessionsDir)
	loc, ok := findPi(root, id)
	if !ok {
		return HostSessionLookup{SearchedRoot: root}
	}
	return HostSessionLookup{Path: loc.Path, SearchedRoot: root}
}

// LocatePiOnHost is FindPiOnHost plus the relative path, failing when absent.
func LocatePiOnHost(id, sessionsDir string) (Location, error) {
	root := orDefault(sessionsDir, DefaultPiSessionsDir)
	loc, ok := findPi(root, id)
	if !ok {
		return Location{}, fmt.Errorf("session %s not found in %s", id, root)
	}
	return loc, nil
}
