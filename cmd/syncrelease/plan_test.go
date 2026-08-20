package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestPlanFor_LiveSkew(t *testing.T) {
	cur, _ := parseRelease("go1.26.6")
	nxt, _ := parseRelease("go1.27.0")
	p := planFor(cur, nxt)

	if p.Release != "go1.27.0" {
		t.Errorf("Release = %q, want go1.27.0", p.Release)
	}
	if p.Current != "go1.26.6" {
		t.Errorf("Current = %q, want go1.26.6", p.Current)
	}
	if !p.MajorTurnover {
		t.Error("MajorTurnover = false, want true")
	}
	if p.Directive != "go 1.26" {
		t.Errorf("Directive = %q, want go 1.26", p.Directive)
	}
	if len(p.Matrix) != 2 || p.Matrix[0] != "1.26.x" || p.Matrix[1] != "1.27.x" {
		t.Errorf("Matrix = %v, want [1.26.x 1.27.x]", p.Matrix)
	}
	wantChecklist := []string{
		"verify compatibility patches gated on go 1.25 floor",
		"update README version claims",
		"verify PATCHES.md accuracy",
		"tag v0.27.0 after merge",
	}
	if len(p.Checklist) != len(wantChecklist) {
		t.Fatalf("Checklist = %v, want %v", p.Checklist, wantChecklist)
	}
	for i, want := range wantChecklist {
		if p.Checklist[i] != want {
			t.Errorf("Checklist[%d] = %q, want %q", i, p.Checklist[i], want)
		}
	}
}

func TestPlanFor_PatchOnly(t *testing.T) {
	cur, _ := parseRelease("go1.26.5")
	nxt, _ := parseRelease("go1.26.6")
	p := planFor(cur, nxt)

	if p.MajorTurnover {
		t.Error("MajorTurnover = true for patch-only, want false")
	}
	if p.Directive != "go 1.25" {
		t.Errorf("Directive = %q, want go 1.25", p.Directive)
	}
	if len(p.Matrix) != 2 || p.Matrix[0] != "1.25.x" || p.Matrix[1] != "1.26.x" {
		t.Errorf("Matrix = %v, want [1.25.x 1.26.x]", p.Matrix)
	}
	if len(p.Checklist) != 0 {
		t.Errorf("Checklist = %v, want empty for patch-only", p.Checklist)
	}
}

func TestRewriteGoMod(t *testing.T) {
	data, err := os.ReadFile("testdata/go.mod.fixture")
	if err != nil {
		t.Fatal(err)
	}
	got, err := rewriteGoMod(data, "go 1.26")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) == string(data) {
		t.Error("rewriteGoMod: content unchanged")
	}
	if !containsLine(string(got), "go 1.26") {
		t.Errorf("rewriteGoMod: 'go 1.26' not found in result:\n%s", got)
	}
	if containsLine(string(got), "go 1.25") {
		t.Errorf("rewriteGoMod: old directive still present:\n%s", got)
	}
}

func TestRewriteGoMod_NotFound(t *testing.T) {
	_, err := rewriteGoMod([]byte("module foo\n"), "go 1.26")
	if err == nil {
		t.Error("expected error when directive absent")
	}
}

func TestRewriteTestYML(t *testing.T) {
	data, err := os.ReadFile("testdata/test.yml.fixture")
	if err != nil {
		t.Fatal(err)
	}
	got, err := rewriteTestYML(data, 26, 27)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) == string(data) {
		t.Error("rewriteTestYML: content unchanged")
	}
	want := `go: ["1.26.x", "1.27.x"]`
	if !containsLine(string(got), want) {
		t.Errorf("rewriteTestYML: %q not found in result:\n%s", want, got)
	}
	if containsLine(string(got), `go: ["1.25.x", "1.26.x"]`) {
		t.Errorf("rewriteTestYML: old matrix still present:\n%s", got)
	}
}

func TestRewriteTestYML_NotFound(t *testing.T) {
	_, err := rewriteTestYML([]byte("no matrix here\n"), 26, 27)
	if err == nil {
		t.Error("expected error when matrix absent")
	}
}

func TestLatestStable(t *testing.T) {
	data, err := os.ReadFile("testdata/dl.json")
	if err != nil {
		t.Fatal(err)
	}
	r, err := latestStable(data)
	if err != nil {
		t.Fatal(err)
	}
	if r.String() != "go1.27.0" {
		t.Errorf("latestStable = %s, want go1.27.0", r)
	}
}

func TestLatestStable_IgnoresRC(t *testing.T) {
	data, err := os.ReadFile("testdata/dl.json")
	if err != nil {
		t.Fatal(err)
	}
	r, err := latestStable(data)
	if err != nil {
		t.Fatal(err)
	}
	if r.String() == "go1.28rc1" {
		t.Error("latestStable returned RC entry, want stable only")
	}
	if r.String() != "go1.27.0" {
		t.Errorf("latestStable = %s, want go1.27.0", r)
	}
}

func TestDLSHA256(t *testing.T) {
	data, err := os.ReadFile("testdata/dl.json")
	if err != nil {
		t.Fatal(err)
	}
	filename, sha, err := dlSHA256(data, "go1.27.0")
	if err != nil {
		t.Fatal(err)
	}
	if filename != "go1.27.0.src.tar.gz" {
		t.Errorf("filename = %q, want go1.27.0.src.tar.gz", filename)
	}
	if sha != "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899" {
		t.Errorf("sha256 = %q, unexpected", sha)
	}
}

func TestParseLsRemote_Annotated(t *testing.T) {
	data, err := os.ReadFile("testdata/lsremote_annotated.txt")
	if err != nil {
		t.Fatal(err)
	}
	sha, err := parseLsRemote(data, "go1.27.0")
	if err != nil {
		t.Fatal(err)
	}
	want := "a4b5c6d7e8f9a0b1c2d3e4f5a6b7c8d9e0f1a2b3"
	if sha != want {
		t.Errorf("parseLsRemote(annotated) = %q, want %q", sha, want)
	}
}

func TestParseLsRemote_Lightweight(t *testing.T) {
	data, err := os.ReadFile("testdata/lsremote_lightweight.txt")
	if err != nil {
		t.Fatal(err)
	}
	sha, err := parseLsRemote(data, "go1.27.0")
	if err != nil {
		t.Fatal(err)
	}
	want := "a4b5c6d7e8f9a0b1c2d3e4f5a6b7c8d9e0f1a2b3"
	if sha != want {
		t.Errorf("parseLsRemote(lightweight) = %q, want %q", sha, want)
	}
}

func TestParseLsRemote_PeeledWins(t *testing.T) {
	raw := []byte("TAGOBJSHA1234567890123456789012345678901\trefs/tags/go1.27.0\nCOMMITSHA123456789012345678901234567890\trefs/tags/go1.27.0^{}\n")
	sha, err := parseLsRemote(raw, "go1.27.0")
	if err != nil {
		t.Fatal(err)
	}
	if sha != "COMMITSHA123456789012345678901234567890" {
		t.Errorf("peeled SHA not preferred: got %q", sha)
	}
}

func TestParseLsRemote_NotFound(t *testing.T) {
	_, err := parseLsRemote([]byte("abc\trefs/tags/go1.26.0\n"), "go1.27.0")
	if err == nil {
		t.Error("expected error for missing tag")
	}
}

func splitLines(s string) []string {
	return strings.Split(s, "\n")
}

func containsLine(s, want string) bool {
	for _, line := range splitLines(s) {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}

func TestApplyPublishRequirePlanPath(t *testing.T) {
	var out, errBuf bytes.Buffer
	if code := runCmd([]string{"apply", "/nonexistent"}, &out, &errBuf); code != 1 ||
		!strings.Contains(errBuf.String(), "--plan-out is required") {
		t.Errorf("apply without --plan-out: code %d, stderr %q", code, errBuf.String())
	}
	errBuf.Reset()
	if code := runCmd([]string{"publish"}, &out, &errBuf); code != 1 ||
		!strings.Contains(errBuf.String(), "--plan is required") {
		t.Errorf("publish without --plan: code %d, stderr %q", code, errBuf.String())
	}
}
