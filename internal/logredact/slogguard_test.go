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
//
// 2026-08-06: the list said "every package owned by the log-leak sweep" and
// omitted every package the #531 sweep actually edited — providers, ground,
// flights, cookies, watch, mcp. So the sweep's own packages were the only ones
// NOT enforced, and the guard's comment described a coverage it did not have.
//
// This matters more than a missing entry, because the shell script written for
// #531 (scripts/ci/check-log-url-redaction.sh) is line-based: it matches a slog
// call and its fields on ONE line, so a call split across lines is invisible to
// it. Nine such calls existed, one of them logging both a raw URL and a raw
// error. THIS guard parses Go syntax, so multi-line calls are handled by
// construction, and it covers the Context and LogAttrs variants the shell
// script never looked at. It is also an ordinary Go test, so it already runs in
// CI with everything else.
//
// The right fix for #531 was always to add these six lines. Two shell scripts
// were hand-rolled instead, each of which had to be discovered unable to fail
// before it was believed.
var enforcedDirs = []string{
	"cmd/trvl",
	"internal/atomicjson",
	"internal/batchexec",
	"internal/cars",
	"internal/cookies",
	"internal/deals",
	"internal/flights",
	"internal/ground",
	"internal/hotels",
	"internal/logredact",
	"internal/mobility",
	"internal/models",
	"internal/multimodal",
	"internal/nab",
	"internal/optimizer",
	"internal/providers",
	"internal/route",
	"internal/safeexec",
	"internal/serpapi",
	"internal/testutil",
	"internal/trip",
	"internal/tripcoalesce",
	"internal/waf",
	"internal/watch",
	"mcp",
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

// urlishKey matches a slog KEY whose name says its value is a URL. Matched on
// the key rather than the value's variable name, so resolvedURL, listingURL and
// bookingURL are covered without having to enumerate every name anyone picks.
var urlishKey = regexp.MustCompile(`(?i)^[a-z_]*(url|uri|link|href|endpoint)$`)

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
				risky := riskyExpr.MatchString(src) || riskyIdent.MatchString(src)

				// KEY-BASED RULE. slog takes alternating key, value pairs after
				// the message, so an odd index is a key and the next argument is
				// its value. If the key SAYS the value is a URL, the value must
				// be wrapped whatever the variable happens to be called.
				//
				// This exists because the name-based rules above only recognise
				// a short list of identifiers -- url, rawURL, endpoint and so on
				// -- and real code calls them resolvedURL, listingURL,
				// bookingURL. Both of those reached a log unwrapped under a
				// literal "url" key while every check in the repository reported
				// clean. The key is the honest signal: whoever wrote "url" was
				// telling us what the value is.
				//
				// Deliberately not the mirror rule for error keys: an "err" key
				// is attached to file and parse failures that never touch a URL,
				// and a guard that fires on those gets deleted. Errors stay
				// covered by riskyExpr/riskyIdent above.
				if !risky && i%2 == 1 && i+1 < len(call.Args) {
					if lit, ok := arg.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						key := strings.Trim(lit.Value, `"`)
						if urlishKey.MatchString(key) {
							valSrc := exprString(fset, call.Args[i+1])
							if !strings.Contains(valSrc, "logredact.") {
								pos := fset.Position(call.Args[i+1].Pos())
								msg := filepath.ToSlash(rel) + ":" + itoa(pos.Line) +
									": slog value " + valSrc + " is logged under the URL-shaped key " +
									lit.Value + "; wrap it with logredact.URL"
								if isEnforced(rel) {
									failures = append(failures, msg)
								} else {
									advisories = append(advisories, msg)
								}
							}
						}
					}
				}

				if !risky {
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
