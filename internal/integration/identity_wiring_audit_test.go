package integration

import (
	"bufio"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestWiringAudit_SettersCoveredInMain(t *testing.T) {
	root := findPrismRepoRoot(t)
	mainLines := readWiringAuditLines(t, filepath.Join(root, "cmd", "prism", "main.go"))
	targets := []wiringAuditTarget{
		{displayName: "admin.API", dir: filepath.Join(root, "internal", "admin"), typeName: "API", receiverVar: "adminAPI"},
		{displayName: "authserver.Server", dir: filepath.Join(root, "internal", "authserver"), typeName: "Server", receiverVar: "authSrv"},
		{displayName: "gateway.Gateway", dir: filepath.Join(root, "internal", "gateway"), typeName: "Gateway", receiverVar: "gw"},
	}
	excluded := map[string]bool{
		"admin.API.SetBinaryFetcher":                        true,
		"authserver.Server.SetAgentPolicy":                  true,
		"authserver.Server.SetDefaultBackendPolicies":       true,
		"authserver.Server.SetDefaultScopes":                true,
		"authserver.Server.SetGrantBinding":                 true,
		"authserver.Server.SetGrantToolAvailabilityChecker": true,
		"authserver.Server.SetGroup":                        true,
		"gateway.Gateway.SetBinaryMount":                    true,
		"gateway.Gateway.SetBridgeURL":                      true,
		"gateway.Gateway.SetBridgeURLs":                     true,
		"gateway.Gateway.SetWorkspaceBridgeConfig":          true,
	}

	missing := 0
	for _, target := range targets {
		for _, setter := range collectWiringAuditSetters(t, target) {
			key := target.displayName + "." + setter.name
			if setter.notWired || excluded[key] {
				continue
			}
			if !mainHasWiringAuditCall(mainLines, target.receiverVar, setter.name) {
				t.Errorf("%s is defined at %s:%d but has no %s.%s( call in cmd/prism/main.go",
					key, setter.file, setter.line, target.receiverVar, setter.name)
				missing++
			}
		}
	}
	if missing > 0 {
		t.FailNow()
	}
}

type wiringAuditTarget struct {
	displayName string
	dir         string
	typeName    string
	receiverVar string
}

type wiringAuditSetter struct {
	name     string
	file     string
	line     int
	notWired bool
}

func findPrismRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	for dir := filepath.Dir(file); ; dir = filepath.Dir(dir) {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && strings.Contains(string(data), "module github.com/1broseidon/prism") {
			return dir
		}
		if parent := filepath.Dir(dir); parent == dir {
			break
		}
	}
	t.Fatal("could not locate repository root with module github.com/1broseidon/prism")
	return ""
}

func collectWiringAuditSetters(t *testing.T, target wiringAuditTarget) []wiringAuditSetter {
	t.Helper()
	entries, err := os.ReadDir(target.dir)
	if err != nil {
		t.Fatalf("ReadDir %s: %v", target.dir, err)
	}
	var setters []wiringAuditSetter
	seen := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		path := filepath.Join(target.dir, entry.Name())
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("ParseFile %s: %v", path, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || !strings.HasPrefix(fn.Name.Name, "Set") {
				continue
			}
			if !wiringAuditReceiverIsPointerTo(fn.Recv, target.typeName) {
				continue
			}
			if seen[fn.Name.Name] {
				continue
			}
			seen[fn.Name.Name] = true
			pos := fset.Position(fn.Pos())
			setters = append(setters, wiringAuditSetter{
				name:     fn.Name.Name,
				file:     path,
				line:     pos.Line,
				notWired: wiringAuditHasNotWiredComment(fn.Doc),
			})
		}
	}
	return setters
}

func wiringAuditReceiverIsPointerTo(recv *ast.FieldList, typeName string) bool {
	if recv == nil || len(recv.List) != 1 {
		return false
	}
	star, ok := recv.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	ident, ok := star.X.(*ast.Ident)
	return ok && ident.Name == typeName
}

func wiringAuditHasNotWiredComment(group *ast.CommentGroup) bool {
	if group == nil {
		return false
	}
	for _, comment := range group.List {
		if strings.Contains(comment.Text, "not-wired:") {
			return true
		}
	}
	return false
}

func readWiringAuditLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open %s: %v", path, err)
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("Scan %s: %v", path, err)
	}
	return lines
}

func mainHasWiringAuditCall(lines []string, receiverVar, method string) bool {
	pattern := regexp.MustCompile(`\b` + regexp.QuoteMeta(receiverVar) + `\s*\.\s*` + regexp.QuoteMeta(method) + `\s*\(`)
	for _, line := range lines {
		code := strings.SplitN(line, "//", 2)[0]
		if pattern.MatchString(code) {
			return true
		}
	}
	return false
}
