package rumourmill

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// banned are words from the originating game's world. Matched case-insensitively
// at word starts, so "WorldReader" does not trip "dread". Add terms here if new
// leaks are found.
var banned = regexp.MustCompile(`(?i)\b(tyrian|aldamar|barrow|dread|merchant|stanford|epic[ _]?whispers|druidic|native clans)`)

// TestNoDomainVocabulary scans non-test Go source and docs for the banned
// terms. It skips doc/design/reference (intentionally game-flavoured) and the
// files that must name the terms in order to ban them (doc/rules, CLAUDE.md, AGENTS.md).
func TestNoDomainVocabulary(t *testing.T) {
	skipDirs := map[string]bool{".git": true, "doc/design/reference": true, "doc/rules": true}
	skipFiles := map[string]bool{"CLAUDE.md": true, "AGENTS.md": true}
	scanned := 0
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[filepath.ToSlash(path)] {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		scan := (strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")) ||
			strings.HasSuffix(name, ".md") || name == "justfile"
		if !scan || skipFiles[name] {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		scanned++
		for i, line := range strings.Split(string(data), "\n") {
			if m := banned.FindString(line); m != "" {
				t.Errorf("%s:%d: banned domain term %q", path, i+1, m)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if scanned == 0 {
		t.Fatal("scanned no files; the guard is not guarding anything")
	}
}

func TestBannedPatternCatchesLeaks(t *testing.T) {
	for _, s := range []string{"Tyrian Legion", "the Barrow", "zone dread", "Merchants", "epic_whispers", "Epic Whispers"} {
		if !banned.MatchString(s) {
			t.Errorf("pattern should match %q", s)
		}
	}
	for _, s := range []string{"WorldReader", "spread", "spreader", "merge"} {
		if banned.MatchString(s) {
			t.Errorf("pattern should not match %q", s)
		}
	}
}
