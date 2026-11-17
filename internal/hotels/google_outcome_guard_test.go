package hotels

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// googleOutcomeViolations lists the ways a file may bypass the one recorded
// Google outcome (#826 design rev 6): writing its state outside
// google_outcome.go, the deleted status-string helpers, an error built from a
// ProviderStatus.Error string, a cooldown refusal built by hand, or a Google
// notice outside the rooms notice line and the out-of-scope SerpAPI source.
func googleOutcomeViolations(fset *token.FileSet, name string, file *ast.File) []string {
	var out []string
	report := func(n ast.Node, what string) {
		out = append(out, fset.Position(n.Pos()).String()+": "+what)
	}
	base := filepath.Base(name)
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range n.Lhs {
				if base != "google_outcome.go" && writesOutcomeState(lhs) {
					report(n, "writes Google outcome state outside google_outcome.go")
				}
			}
		case *ast.CompositeLit:
			if id, ok := n.Type.(*ast.Ident); ok && base != "google_outcome.go" &&
				(id.Name == "googleOutcome" || id.Name == "googleCall") && len(n.Elts) > 0 {
				report(n, "builds a Google outcome by hand")
			}
		case *ast.Ident:
			if n.Name == "googleStatusErr" || n.Name == "googleRefusal" {
				report(n, "uses deleted helper "+n.Name)
			}
		case *ast.BasicLit:
			if n.Kind == token.STRING && base != "google_outcome.go" {
				if v, err := strconv.Unquote(n.Value); err == nil && strings.Contains(v, "refused (HTTP") {
					report(n, `builds a "refused (HTTP" refusal outside googleCooldownErr`)
				}
			}
		case *ast.CallExpr:
			fun, ok := n.Fun.(*ast.SelectorExpr)
			if ok && (fun.Sel.Name == "Errorf" || fun.Sel.Name == "New") {
				for _, arg := range n.Args {
					if sel, ok := arg.(*ast.SelectorExpr); ok && sel.Sel.Name == "Error" {
						report(n, "builds an error from a status Error string")
					}
				}
			}
			if id, ok := n.Fun.(*ast.Ident); ok && id.Name == "providerRateLimitNotice" && len(n.Args) > 0 {
				if lit, ok := n.Args[0].(*ast.BasicLit); ok && lit.Value == `"Google Hotels"` &&
					base != "rooms.go" && base != "serpapi_fallback.go" {
					report(n, "words a Google notice outside rooms.go")
				}
			}
		}
		return true
	})
	return out
}

// writesOutcomeState reports whether an assignment target is, or reaches into,
// a field only the outcome and its calls own: googleOutcome's strongest and
// settled, or anything under googleCall's events (an event's err included).
// The names are the outcome's own, so an unrelated field called err passes
// without type information (muse, review on 738ac158).
func writesOutcomeState(e ast.Expr) bool {
	for {
		switch x := e.(type) {
		case *ast.SelectorExpr:
			switch x.Sel.Name {
			case "strongest", "settled", "events":
				return true
			}
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.StarExpr:
			e = x.X
		case *ast.ParenExpr:
			e = x.X
		default:
			return false
		}
	}
}

// TestGoogleOutcomeGuard_Package: no non-test file in package hotels bypasses
// the recorded Google outcome.
func TestGoogleOutcomeGuard_Package(t *testing.T) {
	names, err := filepath.Glob("*.go")
	if err != nil || len(names) == 0 {
		t.Fatalf("glob: %v (%d files)", err, len(names))
	}
	fset := token.NewFileSet()
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range googleOutcomeViolations(fset, name, file) {
			t.Error(v)
		}
	}
}

// TestGoogleOutcomeGuard_Fixtures: each banned pattern trips the guard, so the
// package scan above cannot pass by checking nothing.
func TestGoogleOutcomeGuard_Fixtures(t *testing.T) {
	fixtures := map[string]string{
		"state write":     `func f(o *googleOutcome) { o.settled = true }`,
		"events write":    `func f(c *googleCall) { c.events = nil }`,
		"strongest write": `func f(o *googleOutcome) { o.strongest = nil }`,
		"event write":     `func f(c *googleCall) { c.events[0].err = nil }`,
		"hand-built":      `var o = googleOutcome{strongest: nil}`,
		"deleted helper":  `var x = googleStatusErr`,
		"cooldown text":   `var e = fmt.Errorf("x refused (HTTP 429)")`,
		"status string":   `func f(ps models.ProviderStatus) error { return fmt.Errorf("google: %s", ps.Error) }`,
		"stray notice":    `var n = providerRateLimitNotice("Google Hotels", nil)`,
		"refusal wrapper": `var y = googleRefusal`,
	}
	for name, body := range fixtures {
		t.Run(name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "fixture.go", "package hotels\n"+body+"\n", 0)
			if err != nil {
				t.Fatal(err)
			}
			if got := googleOutcomeViolations(fset, "fixture.go", file); len(got) == 0 {
				t.Fatalf("fixture %q passed the guard, want a violation", body)
			}
		})
	}
}

// TestGoogleOutcomeGuard_UnrelatedErrFieldPasses: the guard bans writes to the
// outcome's own fields, not to every field that happens to be called err
// (muse, review on 738ac158).
func TestGoogleOutcomeGuard_UnrelatedErrFieldPasses(t *testing.T) {
	const src = "package hotels\ntype probe struct{ err error }\nfunc f(p *probe) { p.err = nil }\n"
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "fixture.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := googleOutcomeViolations(fset, "fixture.go", file); len(got) != 0 {
		t.Fatalf("an unrelated .err write tripped the guard: %v", got)
	}
}
