// Package testenv is a minimal stand-in for the standard library's
// internal/testenv, providing only what the template package tests
// use.
package testenv

import (
	"os"
	"os/exec"
	"testing"
)

// MustHaveGoBuild skips t if a go command is not available.
func MustHaveGoBuild(t testing.TB) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("skipping: go command not found: %v", err)
	}
}

// GoToolPath returns the path to the go command.
func GoToolPath(t testing.TB) string {
	MustHaveGoBuild(t)
	path, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// SetGODEBUG appends v to the GODEBUG environment variable for the
// duration of the test.
func SetGODEBUG(t testing.TB, v string) {
	t.Helper()
	t.Setenv("GODEBUG", os.Getenv("GODEBUG")+","+v)
}
