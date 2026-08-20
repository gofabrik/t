package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestApply_E2E runs apply against the installed go1.27.0 toolchain.
//
// Run with: SYNCRELEASE_E2E=1 go test ./cmd/syncrelease/ -run TestApply_E2E -v
func TestApply_E2E(t *testing.T) {
	if os.Getenv("SYNCRELEASE_E2E") != "1" {
		t.Skip("set SYNCRELEASE_E2E=1 to run")
	}

	goroot := filepath.Join(os.Getenv("HOME"), ".goup", "versions", "go1.27.0")
	if _, err := os.Stat(filepath.Join(goroot, "VERSION")); err != nil {
		t.Fatalf("go1.27.0 not found at %s: %v", goroot, err)
	}

	selfRoot := repoRoot(t)
	cloneDir := t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	run("", "git", "clone", "--no-local", "file://"+selfRoot, cloneDir)
	run(cloneDir, "git", "config", "user.email", "test@test")
	run(cloneDir, "git", "config", "user.name", "Test")
	run(cloneDir, "git", "fetch", "origin", "upstream", "main")
	run(cloneDir, "git", "checkout", "-B", "main", "origin/main")
	deleteOtherBranches(t, cloneDir, "main")
	run(cloneDir, "git", "branch", "upstream", "origin/upstream")
	writeFile(t, cloneDir, "VERSION", "go1.26.6\n")
	run(cloneDir, "git", "add", "VERSION")
	run(cloneDir, "git", "commit", "--allow-empty", "-m", "pin VERSION go1.26.6")

	mainHead := gitRevParse(t, cloneDir, "HEAD")

	syncedSHA := "a4b5c6d7e8f9a0b1c2d3e4f5a6b7c8d9e0f1a2b3"

	planFile := filepath.Join(t.TempDir(), "plan.json")

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cloneDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	var stdout, stderr bytes.Buffer
	code := run2([]string{"apply", goroot, "--synced-sha", syncedSHA, "--plan-out", planFile},
		&stdout, &stderr)
	t.Logf("stdout:\n%s", stdout.String())
	t.Logf("stderr:\n%s", stderr.String())
	if code != 0 {
		t.Fatalf("apply exited %d", code)
	}

	versionData, err := os.ReadFile(filepath.Join(cloneDir, "VERSION"))
	if err != nil {
		t.Fatal(err)
	}
	versionLine, _, _ := strings.Cut(strings.TrimSpace(string(versionData)), "\n")
	if versionLine != "go1.27.0" {
		t.Errorf("VERSION = %q, want go1.27.0", versionLine)
	}

	goModData, err := os.ReadFile(filepath.Join(cloneDir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !containsLine(string(goModData), "go 1.26") {
		t.Errorf("go.mod missing 'go 1.26':\n%s", goModData)
	}
	if containsLine(string(goModData), "go 1.25") {
		t.Errorf("go.mod still has 'go 1.25':\n%s", goModData)
	}

	testYMLData, err := os.ReadFile(filepath.Join(cloneDir, ".github", "workflows", "test.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !containsSubstring(string(testYMLData), `"1.26.x"`) || !containsSubstring(string(testYMLData), `"1.27.x"`) {
		t.Errorf("test.yml matrix not updated:\n%s", testYMLData)
	}

	lastSynced, err := os.ReadFile(filepath.Join(cloneDir, "cmd", "upstreamwatch", "last_synced"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(lastSynced)) != syncedSHA {
		t.Errorf("last_synced = %q, want %q", strings.TrimSpace(string(lastSynced)), syncedSHA)
	}

	upstreamHead := gitRevParse(t, cloneDir, "upstream")

	mergeSHABytes, err := exec.Command("git", "-C", cloneDir, "rev-list", "--merges", "-1", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	mergeSHA := strings.TrimSpace(string(mergeSHABytes))

	subjectBytes, err := exec.Command("git", "-C", cloneDir, "log", "-1", "--format=%s", mergeSHA).Output()
	if err != nil {
		t.Fatal(err)
	}
	wantSubject := "Merge branch 'upstream': go1.27.0"
	if got := strings.TrimSpace(string(subjectBytes)); got != wantSubject {
		t.Errorf("merge commit subject = %q, want %q", got, wantSubject)
	}

	parentLines, err := exec.Command("git", "-C", cloneDir, "log", "-1", "--format=%P", mergeSHA).Output()
	if err != nil {
		t.Fatal(err)
	}
	parents := strings.Fields(strings.TrimSpace(string(parentLines)))
	if len(parents) != 2 {
		t.Fatalf("merge commit has %d parents, want 2", len(parents))
	}
	if parents[0] != mainHead {
		t.Errorf("merge commit first parent = %s, want pre-merge main HEAD %s", parents[0], mainHead)
	}
	if parents[1] != upstreamHead {
		t.Errorf("merge commit second parent = %s, want upstream HEAD %s", parents[1], upstreamHead)
	}

	lastSyncedAtMerge, err := exec.Command("git", "-C", cloneDir, "show",
		mergeSHA+":cmd/upstreamwatch/last_synced").Output()
	if err != nil {
		t.Fatalf("git show last_synced at merge commit: %v", err)
	}
	if strings.TrimSpace(string(lastSyncedAtMerge)) != syncedSHA {
		t.Errorf("last_synced at merge commit = %q, want %q",
			strings.TrimSpace(string(lastSyncedAtMerge)), syncedSHA)
	}

	plan, err := loadPlan(planFile)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Release != "go1.27.0" {
		t.Errorf("plan.Release = %q, want go1.27.0", plan.Release)
	}
	if !plan.MajorTurnover {
		t.Error("plan.MajorTurnover = false, want true")
	}
	if plan.SyncedSHA != syncedSHA {
		t.Errorf("plan.SyncedSHA = %q, want %q", plan.SyncedSHA, syncedSHA)
	}
	// A clean-path rerun must produce no commits and the same plan.
	countBefore, err := exec.Command("git", "-C", cloneDir, "rev-list", "--count", "sync/go1.27.0").Output()
	if err != nil {
		t.Fatal(err)
	}
	plan2File := filepath.Join(t.TempDir(), "plan2.json")
	stdout.Reset()
	stderr.Reset()
	if code := run2([]string{"apply", goroot, "--synced-sha", syncedSHA, "--plan-out", plan2File},
		&stdout, &stderr); code != 0 {
		t.Fatalf("rerun apply exited %d\nstderr: %s", code, stderr.String())
	}
	countAfter, err := exec.Command("git", "-C", cloneDir, "rev-list", "--count", "sync/go1.27.0").Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(countBefore) != string(countAfter) {
		t.Errorf("rerun added commits: %s -> %s", countBefore, countAfter)
	}
	plan2, err := loadPlan(plan2File)
	if err != nil {
		t.Fatal(err)
	}
	if !plan2.MajorTurnover {
		t.Errorf("rerun plan lost turnover: %+v", plan2)
	}
	if !reflect.DeepEqual(plan, plan2) {
		t.Errorf("rerun plan differs: %+v vs %+v", plan, plan2)
	}
}

func run2(args []string, stdout, stderr *bytes.Buffer) int {
	return runCmd(args, stdout, stderr)
}

func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func countMergeParents(t *testing.T, dir string) int {
	t.Helper()
	// Turnover edits may place a non-merge commit at HEAD.
	sha, err := exec.Command("git", "-C", dir, "rev-list", "--merges", "-1", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("git", "-C", dir, "cat-file", "-p", strings.TrimSpace(string(sha))).Output()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "parent ") {
			count++
		}
	}
	return count
}

func containsSubstring(s, sub string) bool {
	return strings.Contains(s, sub)
}
