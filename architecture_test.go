package rumourmill

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "github.com/Whealinghold/rumour_mill"

// allowed lists the sibling packages each package may import. Everything else
// outside the standard library is forbidden: the core packages must not depend
// on declare or on anything third-party, and rules must not know about knowledge.
var allowed = map[string][]string{
	"knowledge": nil,
	"logic":     nil,
	"rules":     {"logic"},
	"spread":    {"knowledge", "logic"},
	"infer":     {"knowledge"},
	"declare":   {"knowledge", "logic", "rules", "spread", "infer"},
}

func TestImportBoundaries(t *testing.T) {
	for pkg, ok := range allowed {
		files, err := filepath.Glob(filepath.Join(pkg, "*.go"))
		if err != nil || len(files) == 0 {
			t.Fatalf("%s: no files (%v)", pkg, err)
		}
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			src, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			f, err := parser.ParseFile(token.NewFileSet(), file, src, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range f.Imports {
				path, _ := strconv.Unquote(imp.Path.Value)
				first, _, _ := strings.Cut(path, "/")
				switch {
				case !strings.Contains(first, "."): // standard library
				case strings.HasPrefix(path, modulePath+"/"):
					sibling := strings.TrimPrefix(path, modulePath+"/")
					found := false
					for _, a := range ok {
						found = found || a == sibling
					}
					if !found {
						t.Errorf("%s imports %s, which %s may not depend on", file, sibling, pkg)
					}
				default:
					t.Errorf("%s imports %s: only the standard library and sibling packages are allowed", file, path)
				}
			}
		}
	}
}
