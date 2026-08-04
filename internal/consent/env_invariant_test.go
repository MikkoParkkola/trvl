package consent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// The browser-consent gates are only as good as one unstated assumption: that a
// decline cannot arrive while a scrape is already running. That assumption is
// true for a reason worth writing down. CookiesDeclined and Tier2Declined read
// os.Getenv on every call, and process environment does not change underneath a
// running process unless the process changes it itself. Nothing outside tests
// does, so the value a gate reads at the top of a scrape is the value that holds
// for the whole scrape.
//
// A reviewer asked for a post-run re-check to close a mid-run decline. There is
// no such window to close, and a branch that can never be taken is a branch that
// can never be tested either. The honest way to answer the review is not to add
// unreachable code, it is to make the premise fail loudly if anyone ever breaks
// it. That is this test.
//
// The check is deliberately wider than the consent variables themselves. A
// narrow check for os.Setenv("TRVL_NO_BROWSER_COOKIES") is evaded by a variable,
// a loop over a map, or a helper one call away, and a security invariant that a
// refactor can slip past is not an invariant. So no non-test code in this module
// mutates process environment at all, and anything that starts has to be added
// here on purpose.
//
// Adding to allowedEnvMutators is allowed. Adding to it without confirming the
// call cannot reach CookiesEnv, Tier2Env or Tier2LegacyEnv reopens a consent
// bypass that a user cannot see and no other test will catch.
var allowedEnvMutators = map[string]string{
	// "relative/path.go": "why this one cannot touch the consent variables",
}

func TestNoNonTestCodeMutatesProcessEnvironment(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("locating the module root: %v", err)
	}

	fset := token.NewFileSet()
	var violations []string

	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "testdata", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if _, allowed := allowedEnvMutators[filepath.ToSlash(rel)]; allowed {
			return nil
		}

		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			// A file this test cannot read is a file it cannot clear, so say so
			// rather than skipping quietly to a green result.
			violations = append(violations, rel+": unparseable, so its environment use is unknown: "+parseErr.Error())
			return nil
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if sel.Sel.Name != "Setenv" && sel.Sel.Name != "Unsetenv" && sel.Sel.Name != "Clearenv" {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "os" {
				return true
			}
			pos := fset.Position(call.Pos())
			violations = append(violations, rel+":"+itoa(pos.Line)+": os."+sel.Sel.Name)
			return true
		})
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walking the module: %v", walkErr)
	}

	if len(violations) > 0 {
		t.Fatalf("non-test code now mutates process environment, which breaks the assumption the browser-consent gates rest on:\n  %s\n\n"+
			"A consent decline is read fresh from the environment on every gate check, and the gates assume that value cannot move while a scrape runs. "+
			"If any of these can reach %s, %s or %s, a user who declines mid-run is silently ignored and the scrape keeps its browser session. "+
			"Either drop the call, or add the file to allowedEnvMutators with a reason it cannot reach those three.",
			strings.Join(violations, "\n  "), CookiesEnv, Tier2Env, Tier2LegacyEnv)
	}
}

// itoa keeps the failure message free of a strconv import for one call.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
