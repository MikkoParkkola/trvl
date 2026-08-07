package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type finding struct {
	position token.Position
	key      string
}

func importAliases(file *ast.File, importPath string) map[string]bool {
	aliases := make(map[string]bool)
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || path != importPath {
			continue
		}
		name := filepath.Base(path)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		aliases[name] = true
	}
	return aliases
}

func selectorCall(call *ast.CallExpr, aliases map[string]bool, names ...string) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	if !ok || !aliases[ident.Name] {
		return false
	}
	for _, name := range names {
		if selector.Sel.Name == name {
			return true
		}
	}
	return false
}

func stringLiteral(expr ast.Expr) (string, bool) {
	literal, ok := expr.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(literal.Value)
	return value, err == nil
}

func isURLKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	return key == "url" || strings.HasSuffix(key, "_url")
}

func isRedacted(expr ast.Expr, logredactAliases map[string]bool) bool {
	call, ok := expr.(*ast.CallExpr)
	return ok && selectorCall(call, logredactAliases, "URL")
}

func scanSource(filename string, source []byte) ([]finding, int, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, source, 0)
	if err != nil {
		return nil, 0, err
	}
	slogAliases := importAliases(file, "log/slog")
	logredactAliases := importAliases(file, "github.com/MikkoParkkola/trvl/internal/logredact")
	if len(slogAliases) == 0 {
		return nil, 0, nil
	}

	var findings []finding
	checked := 0
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || !selectorCall(call, slogAliases, "Debug", "Info", "Warn", "Error") {
			return true
		}
		for i := 1; i+1 < len(call.Args); i += 2 {
			key, ok := stringLiteral(call.Args[i])
			if !ok || !isURLKey(key) {
				continue
			}
			checked++
			if !isRedacted(call.Args[i+1], logredactAliases) {
				findings = append(findings, finding{position: fset.Position(call.Args[i+1].Pos()), key: key})
			}
		}
		for _, arg := range call.Args[1:] {
			attr, ok := arg.(*ast.CallExpr)
			if !ok || !selectorCall(attr, slogAliases, "Any", "String") || len(attr.Args) < 2 {
				continue
			}
			key, ok := stringLiteral(attr.Args[0])
			if !ok || !isURLKey(key) {
				continue
			}
			checked++
			if !isRedacted(attr.Args[1], logredactAliases) {
				findings = append(findings, finding{position: fset.Position(attr.Args[1].Pos()), key: key})
			}
		}
		return false
	})
	return findings, checked, nil
}

func scanTree(root string) ([]finding, int, error) {
	rootHandle, err := os.OpenRoot(root)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rootHandle.Close() }()

	var findings []finding
	checked := 0
	err = fs.WalkDir(rootHandle.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "testdata", "vendor", "third_party":
				if path != "." {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		source, err := rootHandle.ReadFile(path)
		if err != nil {
			return err
		}
		fileFindings, fileChecked, err := scanSource(path, source)
		if err != nil {
			return err
		}
		findings = append(findings, fileFindings...)
		checked += fileChecked
		return nil
	})
	return findings, checked, err
}

func main() {
	findings, checked, err := scanTree(".")
	if err != nil {
		fmt.Fprintf(os.Stderr, "check log URL redaction: %v\n", err)
		os.Exit(1)
	}
	for _, item := range findings {
		fmt.Fprintf(os.Stderr, "error: %s logs %q without logredact.URL\n", item.position, item.key)
	}
	if len(findings) > 0 {
		os.Exit(1)
	}
	fmt.Printf("ok: %d URL-keyed log site(s) redacted\n", checked)
}
