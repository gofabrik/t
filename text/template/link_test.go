// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package template_test

import (
	"bytes"
	"github.com/gofabrik/t/internal/testenv"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

// Issue 36021: verify that text/template doesn't prevent the linker from removing
// unused methods.
func TestLinkerGC(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	testenv.MustHaveGoBuild(t)
	const prog = `package main

import (
	_ "github.com/gofabrik/t/text/template"
)

type T struct{}

func (t *T) Unused() { println("THIS SHOULD BE ELIMINATED") }
func (t *T) Used() {}

var sink *T

func main() {
	var t T
	sink = &t
	t.Used()
}
`
	td := t.TempDir()

	// patch: unlike the standard library's, this package resolves
	// through a module, so the probe program needs one pointing back at
	// this checkout. The go directive is derived from the root go.mod so
	// turnover bumps never break this test.
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	rootmod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	goline := regexp.MustCompile(`(?m)^go .*$`).Find(rootmod)
	gomod := "module m\n\n" + string(goline) + "\n\nrequire github.com/gofabrik/t v0.0.0\n\nreplace github.com/gofabrik/t => " + root + "\n"
	if err := os.WriteFile(filepath.Join(td, "go.mod"), []byte(gomod), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(td, "x.go"), []byte(prog), 0644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(testenv.GoToolPath(t), "build", "-o", "x.exe", "x.go")
	cmd.Dir = td
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v, %s", err, out)
	}
	slurp, err := os.ReadFile(filepath.Join(td, "x.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(slurp, []byte("THIS SHOULD BE ELIMINATED")) {
		t.Error("binary contains code that should be deadcode eliminated")
	}
}
