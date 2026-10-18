package rumourmill

import (
	"go/doc"
	"go/parser"
	"go/token"
	"io/fs"
	"strings"
	"testing"
)

// TestExportedIdentifiersAreDocumented checks the "Docs" rule: every package has
// a package comment that starts "Package <name> ", and every exported type,
// function, method, constant and variable has a doc comment.
func TestExportedIdentifiersAreDocumented(t *testing.T) {
	for pkg := range allowed {
		fset := token.NewFileSet()
		parsed, err := parser.ParseDir(fset, pkg, func(fi fs.FileInfo) bool {
			return !strings.HasSuffix(fi.Name(), "_test.go")
		}, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		astPkg, ok := parsed[pkg]
		if !ok {
			t.Fatalf("package %s not found", pkg)
		}
		p := doc.New(astPkg, modulePath+"/"+pkg, 0)
		if !strings.HasPrefix(p.Doc, "Package "+pkg+" ") {
			t.Errorf("%s: package comment must start %q, got %q", pkg, "Package "+pkg+" ", firstLine(p.Doc))
		}
		missing := func(kind, name, text string) {
			if strings.TrimSpace(text) == "" {
				t.Errorf("%s: exported %s %s has no doc comment", pkg, kind, name)
			}
		}
		for _, v := range append(p.Consts, p.Vars...) {
			missing("const/var group", strings.Join(v.Names, ","), v.Doc)
		}
		for _, f := range p.Funcs {
			missing("func", f.Name, f.Doc)
		}
		for _, ty := range p.Types {
			missing("type", ty.Name, ty.Doc)
			for _, f := range append(ty.Funcs, ty.Methods...) {
				missing("func/method", ty.Name+"."+f.Name, f.Doc)
			}
			for _, v := range append(ty.Consts, ty.Vars...) {
				missing("const/var group", strings.Join(v.Names, ","), v.Doc)
			}
		}
	}
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
