// Sync copies the template packages from a Go source tree into this
// module and rewrites their import paths.
//
// Usage:
//
//	go run ./cmd/sync <goroot>
//
// goroot is an unpacked Go source tree (the directory containing src
// and VERSION). Sync replaces the synced directories wholesale, so it
// must be run on the upstream branch, never on main; main receives
// the result by merge. Files named patch_*.go are left in place.
// html/template's content types are declared as aliases of the
// standard library's; see PATCHES.md.
//
// The release named by the tree's VERSION file is recorded in VERSION
// at the module root, and the tree's LICENSE and PATENTS files are
// copied alongside it.
package main

import (
	"bytes"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const module = "github.com/gofabrik/t"

// dirs are the package directories copied from src, testdata included.
var dirs = []string{
	"text/template",
	"html/template",
	"internal/fmtsort",
}

// rewrites map stdlib import paths to this module. The text/template
// prefix also covers text/template/parse. internal/godebug and
// internal/testenv resolve to small local packages instead of their
// stdlib counterparts; see PATCHES.md.
var rewrites = []struct{ old, new string }{
	{`"text/template`, `"` + module + `/text/template`},
	{`"html/template`, `"` + module + `/html/template`},
	{`"internal/fmtsort"`, `"` + module + `/internal/fmtsort"`},
	{`"internal/godebug"`, `"` + module + `/internal/godebug"`},
	{`"internal/testenv"`, `"` + module + `/internal/testenv"`},
}

// contentTypes are the exported content types in html/template/content.go.
var contentTypes = []string{"CSS", "HTML", "HTMLAttr", "JS", "JSStr", "URL", "Srcset"}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: sync <goroot>")
		os.Exit(2)
	}
	goroot := os.Args[1]
	if err := run(goroot); err != nil {
		fmt.Fprintln(os.Stderr, "sync:", err)
		os.Exit(1)
	}
}

func run(goroot string) error {
	version, err := os.ReadFile(filepath.Join(goroot, "VERSION"))
	if err != nil {
		return err
	}
	for _, dir := range dirs {
		if err := clean(dir); err != nil {
			return err
		}
		if err := copyTree(filepath.Join(goroot, "src", dir), dir); err != nil {
			return err
		}
	}
	if err := aliasContentTypes(); err != nil {
		return err
	}
	for _, name := range []string{"LICENSE", "PATENTS"} {
		data, err := os.ReadFile(filepath.Join(goroot, name))
		if err != nil {
			return err
		}
		if err := os.WriteFile(name, data, 0o644); err != nil {
			return err
		}
	}
	// The VERSION file may carry build metadata on extra lines; the
	// release name is the first.
	release, _, _ := strings.Cut(string(version), "\n")
	return os.WriteFile("VERSION", []byte(release+"\n"), 0o644)
}

// aliasContentTypes rewrites html/template's content type declarations
// into aliases of the standard library's, so values typed by either
// package interoperate. It runs after the import rewrites, which is
// what keeps the inserted "html/template" import pointing at the
// standard library.
func aliasContentTypes() error {
	path := filepath.Join("html", "template", "content.go")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	imports := []byte("import (\n\t\"fmt\"\n")
	if !bytes.Contains(data, imports) {
		return fmt.Errorf("%s: import block not where expected", path)
	}
	data = bytes.Replace(data, imports, []byte("import (\n\t\"fmt\"\n\t\"html/template\"\n"), 1)
	for _, name := range contentTypes {
		decl := []byte("\n\t" + name + " string\n")
		if !bytes.Contains(data, decl) {
			return fmt.Errorf("%s: no declaration for %s", path, name)
		}
		data = bytes.Replace(data, decl, []byte("\n\t"+name+" = template."+name+"\n"), 1)
	}
	formatted, err := format.Source(data)
	if err != nil {
		return fmt.Errorf("%s after rewrite: %v", path, err)
	}
	return os.WriteFile(path, formatted, 0o644)
}

// clean removes a synced directory's contents, keeping patch_* files.
func clean(dir string) error {
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if strings.HasPrefix(filepath.Base(path), "patch_") {
			return nil
		}
		return os.Remove(path)
	})
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.HasSuffix(path, ".go") {
			orig := data
			for _, r := range rewrites {
				data = bytes.ReplaceAll(data, []byte(r.old), []byte(r.new))
			}
			// The module paths are longer than the stdlib ones they
			// replace and sort differently within import blocks.
			if !bytes.Equal(data, orig) {
				formatted, err := format.Source(data)
				if err != nil {
					return fmt.Errorf("%s after rewrite: %v", target, err)
				}
				data = formatted
			}
		}
		return os.WriteFile(target, data, 0o644)
	})
}
