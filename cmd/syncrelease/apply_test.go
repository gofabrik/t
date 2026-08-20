package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func buildMinimalGOROOT(t *testing.T, version string) string {
	t.Helper()
	root := t.TempDir()

	writeFile(t, root, "VERSION", version+"\n")
	writeFile(t, root, "LICENSE", "Go license\n")
	writeFile(t, root, "PATENTS", "Go patents\n")

	writeFile(t, root, "src/text/template/template.go", "package template\n")
	writeFile(t, root, "src/text/template/parse/lex.go", "package parse\n")

	writeFile(t, root, "src/internal/fmtsort/sort.go", "package fmtsort\n")

	// aliasContentTypes requires these exact declarations in content.go.
	writeFile(t, root, "src/html/template/template.go", "package template\n")
	writeFile(t, root, "src/html/template/content.go",
		"package template\n\nimport (\n\t\"fmt\"\n)\n\n"+
			"type (\n"+
			"\tCSS string\n"+
			"\tHTML string\n"+
			"\tHTMLAttr string\n"+
			"\tJS string\n"+
			"\tJSStr string\n"+
			"\tURL string\n"+
			"\tSrcset string\n"+
			")\n\n"+
			"var _ = fmt.Sprintf\n",
	)

	return root
}

// conflictClone creates a clone whose main and upstream branches conflict.
func conflictClone(t *testing.T) string {
	t.Helper()
	selfRoot := repoRoot(t)
	cloneDir := t.TempDir()
	mustGit(t, "", "clone", "--no-local", "file://"+selfRoot, cloneDir)
	mustGit(t, cloneDir, "config", "user.email", "test@test")
	mustGit(t, cloneDir, "config", "user.name", "Test")
	mustGit(t, cloneDir, "fetch", "origin", "upstream:upstream")
	mustGit(t, cloneDir, "checkout", "main")

	// cmd/sync must leave the conflict marker untouched.
	writeFile(t, cloneDir, "sync_conflict_marker.txt", "main content\n")
	mustGit(t, cloneDir, "add", "sync_conflict_marker.txt")
	mustGit(t, cloneDir, "commit", "-m", "add conflict marker on main")

	mustGit(t, cloneDir, "checkout", "upstream")
	writeFile(t, cloneDir, "sync_conflict_marker.txt", "upstream content\n")
	mustGit(t, cloneDir, "add", "sync_conflict_marker.txt")
	mustGit(t, cloneDir, "commit", "-m", "add conflict marker on upstream")
	mustGit(t, cloneDir, "checkout", "main")
	return cloneDir
}

// TestApply_ConflictPath pins the exit status, plan state, and cleanup contract.
func TestApply_ConflictPath(t *testing.T) {
	cloneDir := conflictClone(t)
	goroot := buildMinimalGOROOT(t, "go1.27.0")
	planFile := filepath.Join(t.TempDir(), "plan.json")

	var stdout, stderr bytes.Buffer
	code := applyRelease(
		cloneDir, goroot,
		"deadbeef00000000000000000000000000000000",
		planFile,
		&stdout, &stderr,
	)
	t.Logf("stdout:\n%s", stdout.String())
	t.Logf("stderr:\n%s", stderr.String())

	if code != 2 {
		t.Fatalf("applyRelease = %d, want 2 (conflict)", code)
	}

	plan, err := loadPlan(planFile)
	if err != nil {
		t.Fatalf("loadPlan: %v", err)
	}
	if !plan.Conflict {
		t.Error("plan.Conflict = false, want true")
	}

	statusOut, err := exec.Command("git", "-C", cloneDir, "status", "--porcelain").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(statusOut)) != "" {
		t.Errorf("working tree dirty after conflict abort:\n%s", statusOut)
	}
}

// TestApply_Rerun pins conflict-path convergence across repeated runs.
func TestApply_Rerun(t *testing.T) {
	cloneDir := conflictClone(t)
	goroot := buildMinimalGOROOT(t, "go1.27.0")

	planDir := t.TempDir()
	plan1File := filepath.Join(planDir, "plan1.json")
	plan2File := filepath.Join(planDir, "plan2.json")
	sha := "deadbeef00000000000000000000000000000000"

	var out1, err1 bytes.Buffer
	if code := applyRelease(cloneDir, goroot, sha, plan1File, &out1, &err1); code != 2 {
		t.Fatalf("first run: code %d, want 2\nstderr: %s", code, err1.String())
	}
	count1 := commitCount(t, cloneDir, "upstream")

	var out2, err2 bytes.Buffer
	if code := applyRelease(cloneDir, goroot, sha, plan2File, &out2, &err2); code != 2 {
		t.Fatalf("second run: code %d, want 2\nstderr: %s", code, err2.String())
	}
	if count2 := commitCount(t, cloneDir, "upstream"); count2 != count1 {
		t.Errorf("second run added upstream commits: before=%d after=%d", count1, count2)
	}
	if !strings.Contains(out2.String(), "already carries") {
		t.Errorf("second run did not skip the sync commit:\n%s", out2.String())
	}

	plan1, err := loadPlan(plan1File)
	if err != nil {
		t.Fatal(err)
	}
	plan2, err := loadPlan(plan2File)
	if err != nil {
		t.Fatal(err)
	}
	if !plan2.MajorTurnover || !plan2.Conflict {
		t.Errorf("rerun plan lost state: %+v", plan2)
	}
	if !reflect.DeepEqual(plan1, plan2) {
		t.Errorf("rerun plan differs:\nfirst:  %+v\nsecond: %+v", plan1, plan2)
	}
}

func commitCount(t *testing.T, dir, branch string) int {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-list", "--count", branch).Output()
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("parse commit count: %v", err)
	}
	return n
}

func TestApplyRejectsPlanInsideRepo(t *testing.T) {
	cloneDir := conflictClone(t)
	goroot := buildMinimalGOROOT(t, "go1.27.0")
	var out, errBuf bytes.Buffer
	code := applyRelease(cloneDir, goroot,
		"deadbeef00000000000000000000000000000000",
		filepath.Join(cloneDir, "plan.json"), &out, &errBuf)
	if code != 1 || !strings.Contains(errBuf.String(), "outside the repository") {
		t.Errorf("in-repo plan-out: code %d, stderr %q", code, errBuf.String())
	}
}

func TestApplyRejectsSymlinkedPlan(t *testing.T) {
	cloneDir := conflictClone(t)
	goroot := buildMinimalGOROOT(t, "go1.27.0")
	sha := "deadbeef00000000000000000000000000000000"

	outside := t.TempDir()
	linkDir := filepath.Join(outside, "into-repo")
	if err := os.Symlink(cloneDir, linkDir); err != nil {
		t.Fatal(err)
	}
	var out, errBuf bytes.Buffer
	code := applyRelease(cloneDir, goroot, sha,
		filepath.Join(linkDir, "plan.json"), &out, &errBuf)
	if code != 1 || !strings.Contains(errBuf.String(), "outside the repository") {
		t.Errorf("dir-symlink plan-out: code %d, stderr %q", code, errBuf.String())
	}

	linkFile := filepath.Join(outside, "plan.json")
	if err := os.Symlink(filepath.Join(cloneDir, "plan.json"), linkFile); err != nil {
		t.Fatal(err)
	}
	errBuf.Reset()
	code = applyRelease(cloneDir, goroot, sha, linkFile, &out, &errBuf)
	if code != 1 || !strings.Contains(errBuf.String(), "must not be a symlink") {
		t.Errorf("file-symlink plan-out: code %d, stderr %q", code, errBuf.String())
	}
}

func TestApplyRejectsRelativePlanInsideRepo(t *testing.T) {
	cloneDir := conflictClone(t)
	goroot := buildMinimalGOROOT(t, "go1.27.0")
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cloneDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(orig)
	var out, errBuf bytes.Buffer
	code := applyRelease(cloneDir, goroot,
		"deadbeef00000000000000000000000000000000", "plan.json", &out, &errBuf)
	if code != 1 || !strings.Contains(errBuf.String(), "outside the repository") {
		t.Errorf("relative in-repo plan-out: code %d, stderr %q", code, errBuf.String())
	}
}
