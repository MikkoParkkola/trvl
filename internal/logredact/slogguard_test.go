package logredact

import (
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// enforcedDirs are the packages this guard fails on: every package owned by the
// log-leak sweep, including the ones that were already clean. Enforcing the
// clean ones is the point — the guard exists to stop a new leak being added,
// not only to hold the fixed sites in place.
//
// Everything else in the repository is reported with t.Log only: other packages
// are being edited concurrently by other owners, and a shared tree must not go
// red because of a file this package does not own.
var enforcedDirs = []string{
	"cmd/trvl",
	"internal/atomicjson",
	"internal/batchexec",
	"internal/cars",
	"internal/deals",
	"internal/hotels",
	"internal/logredact",
	"internal/mobility",
	"internal/models",
	"internal/multimodal",
	"internal/nab",
	"internal/optimizer",
	"internal/route",
	"internal/safeexec",
	"internal/serpapi",
	"internal/testutil",
	"internal/trip",
	"internal/tripcoalesce",
	"internal/waf",
}

var slogFuncs = map[string]bool{
	"Debug": true, "Info": true, "Warn": true, "Error": true,
	"DebugContext": true, "InfoContext": true, "WarnContext": true, "ErrorContext": true,
	"Log": true, "LogAttrs": true,
}

// riskyExpr matches argument source text that can carry a URL, a path, or an
// error string into a log record. Errors count because net/http embeds the full
// request URL in *url.Error.
var riskyExpr = regexp.MustCompile(`\.URL\b|\.Error\(\)|\.Path\b|\.RawQuery\b|\.String\(\)`)

// riskyIdent matches bare identifiers whose name says the value is a URL or an
// error, e.g. url, rawURL, err, derr, err2, brErr. The error branch is
// deliberately narrow: a looser pattern also matches ordinary words such as
// "event" or "elapsed", and a guard that fails on those gets deleted.
var riskyIdent = regexp.MustCompile(`(?i)^(u|url|rawurl|requrl|urlstr|endpoint|link|href|uri)$|^[a-z]*[eE]rr[0-9]*$`)

// repoRoot walks up from this file to the module root. Using runtime.Caller
// rather than a relative "../.." keeps the guard correct no matter which
// directory `go test` is invoked from.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for range 8 {
		if _, err := filepath.Glob(filepath.Join(dir, "go.mod")); err == nil {
			if matches, _ := filepath.Glob(filepath.Join(dir, "go.mod")); len(matches) > 0 {
				return dir
			}
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("go.mod not found above this file")
	return ""
}

func isEnforced(rel string) bool {
	rel = filepath.ToSlash(rel)
	for _, d := range enforcedDirs {
		if strings.HasPrefix(rel, d+"/") {
			return true
		}
	}
	return false
}

// TestNoRawURLOrErrorReachesSlog is the regression guard for the log-leak
// sweep: it fails when a slog call in an owned package passes a URL-shaped or
// error-shaped value that has not gone through this package first.
//
// It is deliberately syntactic. A type-checked analysis would catch more, but
// this runs in the normal test suite with no extra dependency, and the shape it
// matches is exactly the shape that produced the defect.
func TestNoRawURLOrErrorReachesSlog(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()
	var failures, advisories []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable subtree: not this guard's business
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "node_modules", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		f, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return nil // a package mid-edit by another owner must not fail this
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !slogFuncs[sel.Sel.Name] {
				return true
			}
			if !mentionsSlog(sel.X) {
				return true
			}
			for i, arg := range call.Args {
				if i == 0 {
					continue // the message literal
				}
				src := exprString(fset, arg)
				if strings.Contains(src, "logredact.") {
					continue
				}
				if !riskyExpr.MatchString(src) && !riskyIdent.MatchString(src) {
					continue
				}
				pos := fset.Position(arg.Pos())
				msg := filepath.ToSlash(rel) + ":" + itoa(pos.Line) + ": slog arg " + src +
					" may carry a URL or an error string; wrap it with logredact"
				if isEnforced(rel) {
					failures = append(failures, msg)
				} else {
					advisories = append(advisories, msg)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	for _, a := range advisories {
		t.Log("advisory (package not owned by this sweep): " + a)
	}
	for _, f := range failures {
		t.Error(f)
	}
}

// mentionsSlog reports whether the receiver of a logging call is the slog
// package or a value whose name looks like a logger.
func mentionsSlog(x ast.Expr) bool {
	switch v := x.(type) {
	case *ast.Ident:
		n := strings.ToLower(v.Name)
		return n == "slog" || strings.Contains(n, "logger") || n == "log"
	case *ast.SelectorExpr:
		return mentionsSlog(v.Sel)
	case *ast.CallExpr:
		return mentionsSlog(v.Fun)
	}
	return false
}

func exprString(fset *token.FileSet, e ast.Expr) string {
	var sb strings.Builder
	if err := printer.Fprint(&sb, fset, e); err != nil {
		return ""
	}
	return sb.String()
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}
