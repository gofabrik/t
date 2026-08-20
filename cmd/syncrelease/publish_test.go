package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type fakeAPI struct {
	issues         []ghIssue
	comments       []ghComment // issue 1 only
	prs            []ghPR
	labelExists    bool
	issuesCreated  int
	commentsPosted int
	prsCreated     int
	labelsCreated  int
}

func (f *fakeAPI) handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/repos/testowner/testrepo/labels/release-sync", func(w http.ResponseWriter, r *http.Request) {
		if f.labelExists {
			json.NewEncoder(w).Encode(map[string]string{"name": "release-sync"})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/repos/testowner/testrepo/labels", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var payload struct {
				Name  string `json:"name"`
				Color string `json:"color"`
			}
			json.NewDecoder(r.Body).Decode(&payload)
			if len(payload.Color) != 6 {
				w.WriteHeader(http.StatusUnprocessableEntity)
				return
			}
			f.labelExists = true
			f.labelsCreated++
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{"name": payload.Name})
		}
	})

	mux.HandleFunc("/repos/testowner/testrepo/issues", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode(f.issues)
			return
		}
		if r.Method == http.MethodPost {
			var payload struct {
				Title  string   `json:"title"`
				Labels []string `json:"labels"`
			}
			json.NewDecoder(r.Body).Decode(&payload)
			issue := ghIssue{Number: len(f.issues) + 1, State: "open", Title: payload.Title}
			f.issues = append(f.issues, issue)
			f.issuesCreated++
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(issue)
		}
	})

	mux.HandleFunc("/repos/testowner/testrepo/issues/1/comments", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			page := 1
			if v := r.URL.Query().Get("page"); v != "" {
				fmt.Sscanf(v, "%d", &page)
			}
			per := 100
			lo := (page - 1) * per
			hi := lo + per
			if lo > len(f.comments) {
				lo = len(f.comments)
			}
			if hi > len(f.comments) {
				hi = len(f.comments)
			}
			json.NewEncoder(w).Encode(f.comments[lo:hi])
			return
		}
		if r.Method == http.MethodPost {
			var payload struct {
				Body string `json:"body"`
			}
			json.NewDecoder(r.Body).Decode(&payload)
			f.comments = append(f.comments, ghComment{Body: payload.Body})
			f.commentsPosted++
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{"id": 1})
		}
	})

	mux.HandleFunc("/repos/testowner/testrepo/pulls", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode(f.prs)
			return
		}
		if r.Method == http.MethodPost {
			var payload struct {
				Title string `json:"title"`
				Head  string `json:"head"`
				Base  string `json:"base"`
				Body  string `json:"body"`
			}
			json.NewDecoder(r.Body).Decode(&payload)
			pr := ghPR{Number: len(f.prs) + 1, State: "open", Title: payload.Title}
			f.prs = append(f.prs, pr)
			f.prsCreated++
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(pr)
		}
	})
	return mux
}

func publishTestEnv(t *testing.T, api *fakeAPI, remoteHasUpstream, remoteHasSyncBranch bool) (*publisher, string) {
	t.Helper()

	srv := httptest.NewServer(api.handler())
	t.Cleanup(srv.Close)

	repoDir := t.TempDir()
	mustGit(t, repoDir, "init", "-b", "main")
	mustGit(t, repoDir, "config", "user.email", "test@test")
	mustGit(t, repoDir, "config", "user.name", "Test")

	writeFile(t, repoDir, "VERSION", "go1.26.6\n")
	writeFile(t, repoDir, "cmd/upstreamwatch/last_synced",
		"0000000000000000000000000000000000000000\n")
	mustGit(t, repoDir, "add", ".")
	mustGit(t, repoDir, "commit", "-m", "init")

	mustGit(t, repoDir, "checkout", "-b", "upstream")
	writeFile(t, repoDir, "VERSION", "go1.27.0\n")
	mustGit(t, repoDir, "add", ".")
	mustGit(t, repoDir, "commit", "-m", "sync go1.27.0")

	mustGit(t, repoDir, "checkout", "-b", "sync/go1.27.0", "main")
	mustGit(t, repoDir, "merge", "--no-ff", "-m", "Merge branch 'upstream': go1.27.0", "upstream")
	mustGit(t, repoDir, "checkout", "main")

	bareDir := t.TempDir() + "/remote.git"
	mustGit(t, "", "init", "--bare", "-b", "main", bareDir)
	mustGit(t, repoDir, "remote", "add", "origin", "file://"+bareDir)

	if remoteHasUpstream {
		mustGit(t, repoDir, "push", "origin", "upstream")
	}
	if remoteHasSyncBranch {
		mustGit(t, repoDir, "push", "origin", "sync/go1.27.0")
	}

	pub := &publisher{
		apiBase:  srv.URL,
		remote:   "file://" + bareDir,
		owner:    "testowner",
		repo:     "testrepo",
		token:    "testtoken",
		repoRoot: repoDir,
		http:     srv.Client(),
	}
	return pub, bareDir
}

func lsRemoteRefs(t *testing.T, bareDir, pattern string) []string {
	t.Helper()
	out, err := exec.Command("git", "ls-remote", "file://"+bareDir, pattern).Output()
	if err != nil {
		t.Fatalf("ls-remote: %v", err)
	}
	var refs []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			refs = append(refs, line)
		}
	}
	return refs
}

func TestPublish_FreshRelease(t *testing.T) {
	api := &fakeAPI{}
	plan := Plan{
		Release:       "go1.27.0",
		Current:       "go1.26.6",
		MajorTurnover: true,
		Directive:     "go 1.26",
		Matrix:        []string{"1.26.x", "1.27.x"},
		Checklist:     []string{"verify patches"},
	}
	pub, bareDir := publishTestEnv(t, api, false, false)
	var out bytes.Buffer
	if err := pub.reconcile(plan, &out); err != nil {
		t.Fatalf("reconcile: %v\n%s", err, out.String())
	}
	if api.issuesCreated != 1 {
		t.Errorf("issuesCreated = %d, want 1", api.issuesCreated)
	}
	if api.prsCreated != 1 {
		t.Errorf("prsCreated = %d, want 1", api.prsCreated)
	}
	if api.commentsPosted != 0 {
		t.Errorf("commentsPosted = %d, want 0", api.commentsPosted)
	}
	if len(lsRemoteRefs(t, bareDir, "refs/heads/upstream")) == 0 {
		t.Error("upstream branch not pushed to remote")
	}
	if len(lsRemoteRefs(t, bareDir, "refs/heads/sync/go1.27.0")) == 0 {
		t.Error("sync/go1.27.0 branch not pushed to remote")
	}
}

func TestPublish_InterruptedAfterBranchPush(t *testing.T) {
	api := &fakeAPI{}
	plan := Plan{Release: "go1.27.0", Current: "go1.26.6"}
	pub, bareDir := publishTestEnv(t, api, true, true)
	var out bytes.Buffer
	if err := pub.reconcile(plan, &out); err != nil {
		t.Fatalf("reconcile: %v\n%s", err, out.String())
	}
	if api.issuesCreated != 1 {
		t.Errorf("issuesCreated = %d, want 1", api.issuesCreated)
	}
	if api.prsCreated != 1 {
		t.Errorf("prsCreated = %d, want 1", api.prsCreated)
	}
	if len(lsRemoteRefs(t, bareDir, "refs/heads/upstream")) == 0 {
		t.Error("upstream ref missing from remote")
	}
	if len(lsRemoteRefs(t, bareDir, "refs/heads/sync/go1.27.0")) == 0 {
		t.Error("sync/go1.27.0 ref missing from remote")
	}
}

func TestPublish_ConflictRerun_MarkerDedup(t *testing.T) {
	existingIssue := ghIssue{Number: 1, State: "open", Title: "Sync go1.27.0"}
	markerComment := ghComment{Body: conflictMarker + "\nresolution steps"}
	api := &fakeAPI{
		issues:   []ghIssue{existingIssue},
		comments: []ghComment{markerComment},
	}
	plan := Plan{Release: "go1.27.0", Current: "go1.26.6", Conflict: true}
	pub, bareDir := publishTestEnv(t, api, false, false)
	var out bytes.Buffer
	if err := pub.reconcile(plan, &out); err != nil {
		t.Fatalf("reconcile: %v\n%s", err, out.String())
	}
	if api.commentsPosted != 0 {
		t.Errorf("commentsPosted = %d after marker present, want 0", api.commentsPosted)
	}
	if api.prsCreated != 0 {
		t.Errorf("prsCreated = %d for conflict state, want 0", api.prsCreated)
	}
	if len(lsRemoteRefs(t, bareDir, "refs/heads/upstream")) == 0 {
		t.Error("upstream not pushed even though conflict path requires it")
	}
	if len(lsRemoteRefs(t, bareDir, "refs/heads/sync/go1.27.0")) > 0 {
		t.Error("sync/go1.27.0 unexpectedly on remote during conflict")
	}
}

func TestPublish_ConflictFirstNotice(t *testing.T) {
	existingIssue := ghIssue{Number: 1, State: "open", Title: "Sync go1.27.0"}
	api := &fakeAPI{issues: []ghIssue{existingIssue}}
	plan := Plan{
		Release:       "go1.27.0",
		Conflict:      true,
		SyncedSHA:     "deadbeef00000000000000000000000000000000",
		MajorTurnover: true,
		Directive:     "go 1.26",
		Matrix:        []string{"1.26.x", "1.27.x"},
	}
	pub, bareDir := publishTestEnv(t, api, false, false)
	var out bytes.Buffer
	if err := pub.reconcile(plan, &out); err != nil {
		t.Fatalf("reconcile: %v\n%s", err, out.String())
	}
	if api.commentsPosted != 1 {
		t.Errorf("commentsPosted = %d, want 1", api.commentsPosted)
	}
	if len(api.comments) == 0 || !strings.Contains(api.comments[0].Body, conflictMarker) {
		t.Error("posted comment missing conflict marker")
	}
	body := api.comments[0].Body
	if !strings.Contains(body, "deadbeef00000000000000000000000000000000") {
		t.Error("conflict notice missing SyncedSHA")
	}
	if !strings.Contains(body, "go mod edit -go=1.26") {
		t.Error("conflict notice missing go mod edit directive")
	}
	if !strings.Contains(body, `["1.26.x", "1.27.x"]`) {
		t.Error("conflict notice missing matrix values")
	}
	if len(lsRemoteRefs(t, bareDir, "refs/heads/upstream")) == 0 {
		t.Error("upstream not pushed before conflict notice")
	}
	if len(lsRemoteRefs(t, bareDir, "refs/heads/sync/go1.27.0")) > 0 {
		t.Error("sync/go1.27.0 unexpectedly on remote")
	}
}

func TestPublish_ResolvedBranchPickup(t *testing.T) {
	existingIssue := ghIssue{Number: 1, State: "open", Title: "Sync go1.27.0"}
	api := &fakeAPI{issues: []ghIssue{existingIssue}}
	plan := Plan{Release: "go1.27.0", Conflict: true}
	pub, bareDir := publishTestEnv(t, api, true, true)
	var out bytes.Buffer
	if err := pub.reconcile(plan, &out); err != nil {
		t.Fatalf("reconcile: %v\n%s", err, out.String())
	}
	if api.prsCreated != 1 {
		t.Errorf("prsCreated = %d, want 1 (resolved-branch pickup)", api.prsCreated)
	}
	if api.commentsPosted != 0 {
		t.Errorf("commentsPosted = %d, want 0", api.commentsPosted)
	}
	if len(lsRemoteRefs(t, bareDir, "refs/heads/sync/go1.27.0")) == 0 {
		t.Error("sync/go1.27.0 missing from remote")
	}
}

func TestPublish_ClosedUnmergedPRHalt(t *testing.T) {
	closedPR := ghPR{Number: 5, State: "closed", MergedAt: "", Title: "Sync go1.27.0"}
	api := &fakeAPI{prs: []ghPR{closedPR}}
	pub, bareDir := publishTestEnv(t, api, false, false)
	var out bytes.Buffer
	if err := pub.reconcile(Plan{Release: "go1.27.0"}, &out); err != nil {
		t.Fatalf("reconcile: %v\n%s", err, out.String())
	}
	if api.issuesCreated != 0 {
		t.Errorf("issuesCreated = %d after closed-unmerged PR, want 0", api.issuesCreated)
	}
	if api.prsCreated != 0 {
		t.Errorf("prsCreated = %d after closed-unmerged PR, want 0", api.prsCreated)
	}
	if !strings.Contains(out.String(), "closed without merge") {
		t.Errorf("output missing halt message: %s", out.String())
	}
	if len(lsRemoteRefs(t, bareDir, "refs/heads/*")) > 0 {
		t.Error("refs pushed to remote despite closed-unmerged halt")
	}
}

func TestPublish_MergedPR_NoOp(t *testing.T) {
	mergedPR := ghPR{Number: 3, State: "closed", MergedAt: "2026-01-01T00:00:00Z", Title: "Sync go1.27.0"}
	api := &fakeAPI{prs: []ghPR{mergedPR}}
	pub, bareDir := publishTestEnv(t, api, false, false)
	var out bytes.Buffer
	if err := pub.reconcile(Plan{Release: "go1.27.0"}, &out); err != nil {
		t.Fatalf("reconcile: %v\n%s", err, out.String())
	}
	if api.issuesCreated != 0 {
		t.Errorf("issuesCreated = %d after merged PR, want 0", api.issuesCreated)
	}
	if api.prsCreated != 0 {
		t.Errorf("prsCreated = %d after merged PR, want 0", api.prsCreated)
	}
	if !strings.Contains(out.String(), "merged") {
		t.Errorf("output missing merged message: %s", out.String())
	}
	if len(lsRemoteRefs(t, bareDir, "refs/heads/*")) > 0 {
		t.Error("refs pushed to remote despite merged-PR no-op")
	}
}

func TestPushArgs(t *testing.T) {
	cases := []struct {
		remote   string
		token    string
		refs     []string
		wantAuth bool
	}{
		{"https://github.com/owner/repo.git", "mytoken", []string{"upstream"}, true},
		{"https://github.com/owner/repo.git", "", []string{"sync/go1.27.0"}, false},
		{"origin", "mytoken", []string{"upstream"}, true},
		{"file:///tmp/remote.git", "mytoken", []string{"upstream"}, true},
		{"git@github.com:owner/repo.git", "mytoken", []string{"upstream"}, true},
	}
	for _, c := range cases {
		args := pushArgs(c.remote, c.token, c.refs...)
		hasAuth := len(args) > 0 && args[0] == "-c"
		if hasAuth != c.wantAuth {
			t.Errorf("pushArgs(%q, %q, %v)[0]: got auth=%v, want %v",
				c.remote, c.token, c.refs, hasAuth, c.wantAuth)
		}
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "push") {
			t.Errorf("pushArgs result missing 'push': %v", args)
		}
		if !strings.Contains(joined, c.remote) {
			t.Errorf("pushArgs result missing remote %q: %v", c.remote, args)
		}
		for _, ref := range c.refs {
			if !strings.Contains(joined, ref) {
				t.Errorf("pushArgs result missing ref %q: %v", ref, args)
			}
		}
		if c.wantAuth && c.token != "" {
			wantCred := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + c.token))
			if !strings.Contains(joined, wantCred) {
				t.Errorf("pushArgs: base64 credential not found in args: %v", args)
			}
		}
	}
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	mustGitWithEnv(t, dir, nil, args...)
}

func mustGitWithEnv(t *testing.T, dir string, env []string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func gitRevParse(t *testing.T, dir, ref string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", ref).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRepoFromRemote(t *testing.T) {
	cases := []struct {
		url       string
		wantOwner string
		wantRepo  string
		wantErr   bool
	}{
		{"https://github.com/owner/repo.git", "owner", "repo", false},
		{"https://github.com/owner/repo", "owner", "repo", false},
		{"git@github.com:owner/repo.git", "owner", "repo", false},
		{"git@github.com:owner/repo", "owner", "repo", false},
		{"notaurl", "", "", true},
	}
	for _, c := range cases {
		owner, repo, err := repoFromRemote(c.url)
		if (err != nil) != c.wantErr {
			t.Errorf("repoFromRemote(%q) err = %v, wantErr %v", c.url, err, c.wantErr)
			continue
		}
		if !c.wantErr && (owner != c.wantOwner || repo != c.wantRepo) {
			t.Errorf("repoFromRemote(%q) = (%q, %q), want (%q, %q)",
				c.url, owner, repo, c.wantOwner, c.wantRepo)
		}
	}
}

func TestConflictNoticeBody_Golden(t *testing.T) {
	plan := Plan{
		Release:       "go1.27.0",
		MajorTurnover: true,
		Directive:     "go 1.26",
		Matrix:        []string{"1.26.x", "1.27.x"},
		SyncedSHA:     "deadbeef00000000000000000000000000000000",
		Conflict:      true,
	}
	want := conflictMarker + "\n\n" +
		"Merge conflict for go1.27.0.\n" +
		"Resolve locally and push `sync/go1.27.0`:\n\n" +
		"```sh\n" +
		"git fetch origin\n" +
		"git checkout -b sync/go1.27.0 origin/main\n" +
		"git merge --no-commit --no-ff origin/upstream\n" +
		"# resolve conflicts at the // patch: sites (PATCHES.md lists them)\n" +
		"printf '%s\\n' deadbeef00000000000000000000000000000000 > cmd/upstreamwatch/last_synced\n" +
		"git add -A\n" +
		"git commit -m \"Merge branch 'upstream': go1.27.0\"\n" +
		"go mod edit -go=1.26\n" +
		`sed -i.bak 's/go: \[.*\]/go: ["1.26.x", "1.27.x"]/' .github/workflows/test.yml && rm .github/workflows/test.yml.bak` + "\n" +
		"git add go.mod .github/workflows/test.yml\n" +
		"git commit -m \"turnover: directive go 1.26, matrix 1.26.x/1.27.x\"\n" +
		"git push origin sync/go1.27.0\n" +
		"```\n"
	if got := conflictNoticeBody(plan); got != want {
		t.Errorf("notice body mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestPublish_EnsureLabelCreatesOnce(t *testing.T) {
	api := &fakeAPI{}
	plan := Plan{Release: "go1.27.0", Current: "go1.26.6", Conflict: true,
		SyncedSHA: "deadbeef00000000000000000000000000000000"}
	pub, _ := publishTestEnv(t, api, false, false)
	var out bytes.Buffer
	if err := pub.reconcile(plan, &out); err != nil {
		t.Fatalf("reconcile: %v\n%s", err, out.String())
	}
	if api.labelsCreated != 1 {
		t.Errorf("labelsCreated = %d, want 1", api.labelsCreated)
	}
	var out2 bytes.Buffer
	if err := pub.reconcile(plan, &out2); err != nil {
		t.Fatal(err)
	}
	if api.labelsCreated != 1 {
		t.Errorf("labelsCreated after rerun = %d, want 1 (existing label reused)", api.labelsCreated)
	}
}

func TestPublish_ConflictMarkerFoundBeyondFirstPage(t *testing.T) {
	existingIssue := ghIssue{Number: 1, State: "open", Title: "Sync go1.27.0"}
	filler := make([]ghComment, 100)
	for i := range filler {
		filler[i] = ghComment{Body: fmt.Sprintf("noise %d", i)}
	}
	api := &fakeAPI{
		issues:      []ghIssue{existingIssue},
		comments:    append(filler, ghComment{Body: conflictMarker + "\nnotice"}),
		labelExists: true,
	}
	plan := Plan{Release: "go1.27.0", Current: "go1.26.6", Conflict: true,
		SyncedSHA: "deadbeef00000000000000000000000000000000"}
	pub, _ := publishTestEnv(t, api, false, false)
	before := api.commentsPosted
	var out bytes.Buffer
	if err := pub.reconcile(plan, &out); err != nil {
		t.Fatalf("reconcile: %v\n%s", err, out.String())
	}
	if api.commentsPosted != before {
		t.Errorf("notice reposted despite marker on page 2: posted=%d", api.commentsPosted-before)
	}
	if !strings.Contains(out.String(), "already posted") {
		t.Errorf("missing dedup log:\n%s", out.String())
	}
}
