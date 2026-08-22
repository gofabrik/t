package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

const conflictMarker = "<!-- syncrelease-conflict-notice -->"

type publisher struct {
	apiBase  string
	remote   string
	owner    string
	repo     string
	token    string
	dryRun   bool
	repoRoot string
	http     *http.Client
}

func newPublisher(repoRoot, token, owner, repo string) *publisher {
	return &publisher{
		apiBase:  "https://api.github.com",
		remote:   "origin",
		owner:    owner,
		repo:     repo,
		token:    token,
		repoRoot: repoRoot,
		http:     &http.Client{Timeout: 30 * time.Second},
	}
}

// reconcile creates whatever the sync still needs. A merged PR is the
// only terminal state; a closed or deleted PR is recreated because the
// release is still unsynced.
func (p *publisher) reconcile(plan Plan, stdout io.Writer) error {
	syncBranch := "sync/" + plan.Release
	head := p.owner + ":" + syncBranch

	prs, err := p.listPRs(head)
	if err != nil {
		return fmt.Errorf("list PRs: %v", err)
	}
	for _, pr := range prs {
		if pr.MergedAt != "" {
			fmt.Fprintf(stdout, "PR #%d merged; release sync complete\n", pr.Number)
			return nil
		}
	}

	issue, err := p.ensureIssue(plan, stdout)
	if err != nil {
		return fmt.Errorf("issue: %v", err)
	}

	remoteHasSyncBranch, err := p.remoteHasBranch(syncBranch)
	if err != nil {
		return fmt.Errorf("check remote branch: %v", err)
	}

	// Conflict notices refer only to an upstream branch that is already remote.
	if plan.Conflict && !remoteHasSyncBranch {
		if err := p.pushUpstream(stdout); err != nil {
			return fmt.Errorf("push upstream: %v", err)
		}
		return p.postConflictNotice(issue, plan, stdout)
	}

	if err := p.pushUpstream(stdout); err != nil {
		return fmt.Errorf("push upstream: %v", err)
	}

	if !remoteHasSyncBranch {
		if err := p.pushBranch(syncBranch, stdout); err != nil {
			return fmt.Errorf("push %s: %v", syncBranch, err)
		}
	}

	for _, pr := range prs {
		if pr.State == "open" {
			fmt.Fprintf(stdout, "PR #%d already open for %s\n", pr.Number, syncBranch)
			return nil
		}
	}
	return p.openPR(plan, issue, stdout)
}

type ghIssue struct {
	Number int    `json:"number"`
	State  string `json:"state"`
	Title  string `json:"title"`
}

type ghComment struct {
	Body string `json:"body"`
}

type ghPR struct {
	Number   int    `json:"number"`
	State    string `json:"state"`
	MergedAt string `json:"merged_at"`
	Title    string `json:"title"`
}

func (p *publisher) listPRs(head string) ([]ghPR, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/pulls?state=all&head=%s&per_page=10",
		p.apiBase, p.owner, p.repo, head)
	var prs []ghPR
	if err := p.apiGet(url, &prs); err != nil {
		return nil, err
	}
	return prs, nil
}

// ensureLabel creates the release-sync label when it does not exist, so
// issue creation and label-filtered lookup stay consistent.
func (p *publisher) ensureLabel(stdout io.Writer) error {
	url := fmt.Sprintf("%s/repos/%s/%s/labels/release-sync", p.apiBase, p.owner, p.repo)
	var label struct {
		Name string `json:"name"`
	}
	err := p.apiGet(url, &label)
	if err == nil {
		return nil
	}
	if !strings.Contains(err.Error(), "404") {
		return err
	}
	if p.dryRun {
		fmt.Fprintf(stdout, "dry-run: would create label release-sync\n")
		return nil
	}
	body := struct {
		Name        string `json:"name"`
		Color       string `json:"color"`
		Description string `json:"description"`
	}{Name: "release-sync", Color: "1d76db", Description: "tracks an upstream release sync"}
	var created struct {
		Name string `json:"name"`
	}
	if err := p.apiPost(
		fmt.Sprintf("%s/repos/%s/%s/labels", p.apiBase, p.owner, p.repo),
		body, &created,
	); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "created label release-sync\n")
	return nil
}

func (p *publisher) ensureIssue(plan Plan, stdout io.Writer) (*ghIssue, error) {
	if err := p.ensureLabel(stdout); err != nil {
		return nil, fmt.Errorf("label: %v", err)
	}
	title := "Sync " + plan.Release
	var issues []ghIssue
	url := fmt.Sprintf("%s/repos/%s/%s/issues?state=open&labels=release-sync&per_page=100",
		p.apiBase, p.owner, p.repo)
	if err := p.apiGet(url, &issues); err != nil {
		return nil, err
	}
	for i := range issues {
		if issues[i].Title == title {
			fmt.Fprintf(stdout, "issue #%d already open: %s\n", issues[i].Number, title)
			return &issues[i], nil
		}
	}
	if p.dryRun {
		fmt.Fprintf(stdout, "dry-run: would create issue %q\n", title)
		return &ghIssue{Number: 0, Title: title}, nil
	}
	body := struct {
		Title  string   `json:"title"`
		Labels []string `json:"labels"`
	}{
		Title:  title,
		Labels: []string{"release-sync"},
	}
	var created ghIssue
	if err := p.apiPost(
		fmt.Sprintf("%s/repos/%s/%s/issues", p.apiBase, p.owner, p.repo),
		body, &created,
	); err != nil {
		return nil, err
	}
	fmt.Fprintf(stdout, "created issue #%d: %s\n", created.Number, title)
	return &created, nil
}

func (p *publisher) postConflictNotice(issue *ghIssue, plan Plan, stdout io.Writer) error {
	if issue == nil || issue.Number == 0 {
		if p.dryRun {
			fmt.Fprintf(stdout, "dry-run: would post conflict notice:\n%s", conflictNoticeBody(plan))
		}
		return nil
	}
	// Deduplication must cover the full issue history.
	for page := 1; ; page++ {
		var comments []ghComment
		url := fmt.Sprintf("%s/repos/%s/%s/issues/%d/comments?per_page=100&page=%d",
			p.apiBase, p.owner, p.repo, issue.Number, page)
		if err := p.apiGet(url, &comments); err != nil {
			return err
		}
		found := false
		for _, c := range comments {
			if strings.Contains(c.Body, conflictMarker) {
				found = true
				break
			}
		}
		if found {
			fmt.Fprintf(stdout, "conflict notice already posted on #%d\n", issue.Number)
			return nil
		}
		if len(comments) < 100 {
			break
		}
	}
	if p.dryRun {
		fmt.Fprintf(stdout, "dry-run: would post conflict notice on #%d\n", issue.Number)
		return nil
	}
	body := conflictNoticeBody(plan)
	payload := struct {
		Body string `json:"body"`
	}{Body: body}
	var result json.RawMessage
	postURL := fmt.Sprintf("%s/repos/%s/%s/issues/%d/comments",
		p.apiBase, p.owner, p.repo, issue.Number)
	if err := p.apiPost(postURL, payload, &result); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "posted conflict notice on #%d\n", issue.Number)
	return nil
}

func conflictNoticeBody(plan Plan) string {
	var b strings.Builder
	b.WriteString(conflictMarker + "\n\n")
	b.WriteString("Merge conflict for " + plan.Release + ".\n")
	b.WriteString("Resolve locally and push `sync/" + plan.Release + "`:\n\n")
	b.WriteString("```sh\n")
	b.WriteString("git fetch origin\n")
	b.WriteString("git checkout -b sync/" + plan.Release + " origin/main\n")
	b.WriteString("git merge --no-commit --no-ff origin/upstream\n")
	b.WriteString("# resolve conflicts at the // patch: sites (PATCHES.md lists them)\n")
	b.WriteString("printf '%s\\n' " + plan.SyncedSHA + " > cmd/upstreamwatch/last_synced\n")
	b.WriteString("git add -A\n")
	b.WriteString("git commit -m \"Merge branch 'upstream': " + plan.Release + "\"\n")
	if plan.MajorTurnover {
		directiveVal := strings.TrimPrefix(plan.Directive, "go ")
		matrixStr := `["` + strings.Join(plan.Matrix, `", "`) + `"]`
		b.WriteString("go mod edit -go=" + directiveVal + "\n")
		b.WriteString(`sed -i.bak 's/go: \[.*\]/go: ` + matrixStr + `/' .github/workflows/test.yml && rm .github/workflows/test.yml.bak` + "\n")
		b.WriteString("git add go.mod .github/workflows/test.yml\n")
		b.WriteString("git commit -m \"turnover: directive " + plan.Directive +
			", matrix " + strings.Join(plan.Matrix, "/") + "\"\n")
	}
	b.WriteString("git push origin sync/" + plan.Release + "\n")
	b.WriteString("```\n")
	return b.String()
}

func (p *publisher) remoteHasBranch(branch string) (bool, error) {
	remote := p.effectiveRemote()
	out, err := p.gitOutput("ls-remote", remote, "refs/heads/"+branch)
	if err != nil {
		return false, fmt.Errorf("ls-remote %s: %v", remote, err)
	}
	return len(bytes.TrimSpace(out)) > 0, nil
}

func (p *publisher) pushUpstream(stdout io.Writer) error {
	remote := p.effectiveRemote()
	remoteRef, err := p.gitOutput("ls-remote", remote, "refs/heads/upstream")
	if err != nil {
		return err
	}
	localRef, err := p.gitOutput("rev-parse", "refs/heads/upstream")
	if err != nil {
		return nil
	}
	localSHA := strings.TrimSpace(string(localRef))
	if len(bytes.TrimSpace(remoteRef)) > 0 {
		remoteSHA := strings.Fields(string(remoteRef))[0]
		if remoteSHA == localSHA {
			fmt.Fprintf(stdout, "remote upstream already at %s\n", localSHA[:7])
			return nil
		}
	}
	if p.dryRun {
		fmt.Fprintf(stdout, "dry-run: would push upstream\n")
		return nil
	}
	return p.pushWithAuth(stdout, "upstream")
}

func (p *publisher) pushBranch(branch string, stdout io.Writer) error {
	if p.dryRun {
		fmt.Fprintf(stdout, "dry-run: would push %s\n", branch)
		return nil
	}
	return p.pushWithAuth(stdout, branch)
}

func (p *publisher) openPR(plan Plan, issue *ghIssue, stdout io.Writer) error {
	syncBranch := "sync/" + plan.Release
	if p.dryRun {
		fmt.Fprintf(stdout, "dry-run: would open PR for %s\n", syncBranch)
		return nil
	}
	prBody := buildPRBody(plan, issue)
	payload := struct {
		Title string `json:"title"`
		Head  string `json:"head"`
		Base  string `json:"base"`
		Body  string `json:"body"`
	}{
		Title: "Sync " + plan.Release,
		Head:  syncBranch,
		Base:  "main",
		Body:  prBody,
	}
	var created ghPR
	if err := p.apiPost(
		fmt.Sprintf("%s/repos/%s/%s/pulls", p.apiBase, p.owner, p.repo),
		payload, &created,
	); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "opened PR #%d: Sync %s\n", created.Number, plan.Release)
	return nil
}

func buildPRBody(plan Plan, issue *ghIssue) string {
	var b strings.Builder
	if issue != nil && issue.Number > 0 {
		fmt.Fprintf(&b, "Closes #%d\n\n", issue.Number)
	}
	if len(plan.Checklist) > 0 {
		b.WriteString("## Review checklist\n\n")
		for _, item := range plan.Checklist {
			fmt.Fprintf(&b, "- [ ] %s\n", item)
		}
	}
	return b.String()
}

// pushArgs injects host-scoped GitHub credentials for checkouts where
// persist-credentials is false. The setting is inert for file and SSH remotes.
func pushArgs(remote, token string, refs ...string) []string {
	var args []string
	if token != "" {
		cred := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
		args = append(args,
			"-c", "http.https://github.com/.extraheader=AUTHORIZATION: basic "+cred,
		)
	}
	args = append(args, "push", remote)
	args = append(args, refs...)
	return args
}

func (p *publisher) pushWithAuth(stdout io.Writer, refs ...string) error {
	remote := p.effectiveRemote()
	cmd := exec.Command("git", pushArgs(remote, p.token, refs...)...)
	cmd.Dir = p.repoRoot
	cmd.Stdout = stdout
	cmd.Stderr = stdout
	return cmd.Run()
}

func (p *publisher) effectiveRemote() string {
	if p.remote != "" {
		return p.remote
	}
	return "origin"
}

func (p *publisher) apiGet(url string, v any) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

func (p *publisher) apiPost(url string, body, result any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("POST %s: %s", url, resp.Status)
	}
	if result != nil {
		return json.NewDecoder(resp.Body).Decode(result)
	}
	return nil
}

func (p *publisher) gitOutput(args ...string) ([]byte, error) {
	return gitOutput(p.repoRoot, args...)
}

// repoFromRemote parses owner and repository from HTTPS or SSH remotes.
func repoFromRemote(url string) (owner, repo string, err error) {
	url = strings.TrimSuffix(url, ".git")
	if i := strings.Index(url, ":"); i >= 0 && !strings.HasPrefix(url, "http") {
		url = url[i+1:]
	}
	if i := strings.LastIndex(url, "github.com/"); i >= 0 {
		url = url[i+len("github.com/"):]
	}
	parts := strings.SplitN(url, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("cannot parse owner/repo from %q", url)
	}
	return parts[0], parts[1], nil
}

// resolveOwnerRepo returns owner and repo from GITHUB_REPOSITORY env or the
// git remote origin URL.
func resolveOwnerRepo(repoRoot string) (owner, repo string, err error) {
	if r := os.Getenv("GITHUB_REPOSITORY"); r != "" {
		parts := strings.SplitN(r, "/", 2)
		if len(parts) == 2 {
			return parts[0], parts[1], nil
		}
	}
	out, err := gitOutput(repoRoot, "remote", "get-url", "origin")
	if err != nil {
		return "", "", fmt.Errorf("git remote get-url origin: %v", err)
	}
	return repoFromRemote(strings.TrimSpace(string(out)))
}
