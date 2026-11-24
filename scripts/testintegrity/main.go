// Command testintegrity identifies declaration-preserving Go test moves.
// It prints only source paths whose full contents survive in one destination.
package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/scanner"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func git(args ...string) ([]byte, error) { return exec.Command("git", args...).Output() }

type source struct {
	constraints  string
	imports      map[string]bool
	declarations map[string]int
}

// Whitespace and comments do not affect declarations; literal values do.
func tokens(data []byte) string {
	fs := token.NewFileSet()
	var s scanner.Scanner
	s.Init(fs.AddFile("", -1, len(data)), data, nil, 0)
	var out strings.Builder
	for {
		_, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		fmt.Fprintf(&out, "%d:%q;", tok, lit)
	}
	return out.String()
}

func parse(name string, data []byte) (source, error) {
	result := source{declarations: map[string]int{}, imports: map[string]bool{}}
	fs := token.NewFileSet()
	f, err := parser.ParseFile(fs, name, data, parser.ParseComments)
	if err != nil {
		return result, err
	}
	// Include explicit constraints and compiler directives. Preserve spelling
	// conservatively rather than attempting Boolean equivalence.
	for _, group := range f.Comments {
		for _, c := range group.List {
			if strings.HasPrefix(c.Text, "//go:") || strings.HasPrefix(c.Text, "// +build") {
				result.constraints += c.Text + "\n"
			}
		}
	}
	// Go also derives constraints from filename suffixes.
	suffixes := " aix android darwin dragonfly freebsd hurd illumos ios js linux nacl netbsd openbsd plan9 solaris wasip1 windows zos 386 amd64 amd64p32 arm armbe arm64 arm64be loong64 mips mipsle mips64 mips64le mips64p32 mips64p32le ppc ppc64 ppc64le riscv riscv64 s390 s390x sparc sparc64 wasm "
	parts := strings.Split(strings.TrimSuffix(filepath.Base(name), "_test.go"), "_")
	for i := len(parts) - 1; i > 0 && strings.Contains(suffixes, " "+parts[i]+" "); i-- {
		result.constraints += "filename:" + parts[i] + "\n"
	}
	imports := map[string]string{}
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || path == "C" {
			return result, fmt.Errorf("unsupported import")
		}
		result.imports[path] = true
		alias := filepath.Base(path)
		if imp.Name != nil {
			alias = imp.Name.Name
		}
		if alias == "." {
			return result, fmt.Errorf("dot import cannot be verified")
		}
		if alias == "_" {
			result.constraints += "side-effect:" + path + "\n"
		} else {
			imports[alias] = path
		}
	}
	// Normalize only references bound to imports, never local variables with
	// the same name. This allows pb.Message -> lkproto.Message safely.
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		if ok && id.Obj == nil && imports[id.Name] != "" {
			id.Name = "IMPORT[" + imports[id.Name] + "]"
		}
		return true
	})
	for _, decl := range f.Decls {
		if d, ok := decl.(*ast.GenDecl); ok && d.Tok == token.IMPORT {
			continue
		}
		var b bytes.Buffer
		if err := format.Node(&b, fs, decl); err != nil {
			return result, err
		}
		result.declarations[tokens(b.Bytes())]++
	}
	return result, nil
}

// Moving tests into another module would remove them from the original suite.
func moduleRoot(name string, files map[string]bool) string {
	for dir := filepath.Dir(name); ; dir = filepath.Dir(dir) {
		if files[filepath.Join(dir, "go.mod")] {
			return dir
		}
		if dir == "." {
			return "."
		}
	}
}

func main() {
	if len(os.Args) < 3 {
		os.Exit(2)
	}
	base := os.Args[1]
	cached := base == "--cached"
	ref := base
	if cached {
		ref = "HEAD"
	}
	// Only changed, explicitly checked destinations are eligible. Existing
	// copies elsewhere cannot excuse deleting an independent test suite.
	changed, err := git("diff", base, "--no-renames", "--name-only", "--diff-filter=AM", "-z")
	if err != nil {
		panic(err)
	}
	eligible := map[string]bool{}
	for _, name := range strings.Split(string(changed), "\x00") {
		eligible[name] = true
	}
	requested := map[string]bool{}
	for _, name := range os.Args[2:] {
		requested[name] = true
	}
	// Only tracked/indexed destinations are eligible. An ignored backup or an
	// unstaged file must never justify removing a staged test.
	names, err := git("ls-files", "-z")
	if err != nil {
		panic(err)
	}
	oldNames, err := git("ls-tree", "-r", "--name-only", "-z", ref)
	if err != nil {
		panic(err)
	}
	oldFiles, currentFiles := map[string]bool{}, map[string]bool{}
	for _, name := range strings.Split(string(oldNames), "\x00") {
		oldFiles[name] = true
	}
	for _, name := range strings.Split(string(names), "\x00") {
		currentFiles[name] = true
	}
	candidates := map[string]source{}
	for _, name := range strings.Split(string(names), "\x00") {
		if !strings.HasSuffix(name, "_test.go") || !eligible[name] || !requested[name] || strings.HasPrefix(filepath.Base(name), "_") || strings.HasPrefix(filepath.Base(name), ".") {
			continue
		}
		ignored := false
		for _, part := range strings.Split(filepath.Dir(name), string(filepath.Separator)) {
			if part == "testdata" || part == "vendor" || (part != "." && (strings.HasPrefix(part, ".") || strings.HasPrefix(part, "_"))) {
				ignored = true
			}
		}
		if ignored {
			continue
		}
		var data []byte
		if cached {
			data, err = git("show", ":"+name)
		} else {
			data, err = os.ReadFile(name)
		}
		if err != nil {
			continue
		}
		parsed, err := parse(name, data)
		if err == nil {
			candidates[name] = parsed
		}
	}
	destinations := make([]string, 0, len(candidates))
	for name := range candidates {
		destinations = append(destinations, name)
	}
	sort.Strings(destinations)
	available := map[string]source{}
	for name, current := range candidates {
		added := source{constraints: current.constraints, imports: current.imports, declarations: map[string]int{}}
		for decl, count := range current.declarations {
			added.declarations[decl] = count
		}
		if data, err := git("show", ref+":"+name); err == nil {
			if old, err := parse(name, data); err == nil && old.constraints == current.constraints {
				for decl, count := range old.declarations {
					added.declarations[decl] -= count
				}
			}
		}
		available[name] = added
	}
	for _, name := range os.Args[2:] {
		data, err := git("show", ref+":"+name)
		if err != nil {
			continue
		}
		old, err := parse(name, data)
		if err != nil || len(old.declarations) == 0 {
			continue
		}
		for _, destination := range destinations {
			if moduleRoot(name, oldFiles) != moduleRoot(destination, currentFiles) {
				continue
			}
			current := available[destination]
			if destination == name {
				current = candidates[destination]
			}
			if old.constraints != current.constraints {
				continue
			}
			preserved := true
			for path := range old.imports {
				if !current.imports[path] {
					preserved = false
				}
			}
			for decl, count := range old.declarations {
				if current.declarations[decl] < count {
					preserved = false
					break
				}
			}
			if preserved {
				if destination != name {
					for decl, count := range old.declarations {
						current.declarations[decl] -= count
					}
				}
				fmt.Println(name)
				break
			}
		}
	}
}
