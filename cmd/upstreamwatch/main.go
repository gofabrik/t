// Upstreamwatch reports commits in the Go repository that touch the
// synced packages and have not been reported here yet.
//
// Usage (from the module root):
//
//	go run ./cmd/upstreamwatch [-advance]
//
// It queries the golang/go commit history through the GitHub API,
// path-filtered, without cloning, on three refs: master, where the
// next sync's content accumulates, and the release branches of the
// current and previous Go major, where point and security fixes land.
// GITHUB_TOKEN, when set, authenticates the requests.
//
// Two markers live next to this tool: last_synced, the commit the
// last cmd/sync consumed, and last_alerted, the newest commit already
// reported. Commits after last_alerted are new; -advance moves the
// marker past them once they are reported. The distance between the
// two markers is the outstanding port debt.
//
// The exit status is 2 when attention is required: a release-branch
// commit touches html/template (the same-day security case, reported
// on a SECURITY line), the latest stable Go release is newer than
// VERSION (a STALE line), or, when GITHUB_REPOSITORY is set, the
// tracked release has no matching v0.<minor> tag (a RELEASE line - a
// sync is finished when consumers can require it). Exit status 1 is
// reserved for errors, so callers can tell a broken run from a report
// that needs action. Commits touching
// the escape files or the parse package are flagged in the report;
// their merges need human review rather than a rubber stamp.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

const api = "https://api.github.com/repos/golang/go"

// paths are the upstream directories the module syncs; parse is
// queried separately so its commits can be flagged.
var paths = []string{
	"src/text/template",
	"src/text/template/parse",
	"src/html/template",
	"src/internal/fmtsort",
}

var client = &http.Client{Timeout: 30 * time.Second}

type commit struct {
	Sha    string
	Commit struct {
		Committer struct{ Date time.Time }
		Message   string
	}
	Files []struct{ Filename string }
}

func main() {
	advance := flag.Bool("advance", false, "move last_alerted past the reported commits")
	flag.Parse()
	attention, err := run(*advance)
	if err != nil {
		fmt.Fprintln(os.Stderr, "upstreamwatch:", err)
		os.Exit(1)
	}
	if attention {
		os.Exit(2)
	}
}

func run(advance bool) (attention bool, err error) {
	synced, err := marker("last_synced")
	if err != nil {
		return false, err
	}
	alerted, err := marker("last_alerted")
	if err != nil {
		return false, err
	}
	version, err := os.ReadFile("VERSION")
	if err != nil {
		return false, err
	}
	release, _, _ := strings.Cut(string(version), "\n")
	major, minor, _, err := parseRelease(release)
	if err != nil {
		return false, err
	}
	fmt.Printf("tracking %s; synced %s, alerted %s\n", release, synced[:7], alerted[:7])

	var base commit
	if err := get(api+"/commits/"+alerted, &base); err != nil {
		return false, err
	}
	since := base.Commit.Committer.Date

	refs := []string{
		"master",
		fmt.Sprintf("release-branch.go%d.%d", major, minor),
		fmt.Sprintf("release-branch.go%d.%d", major, minor-1),
	}
	newestSha := ""
	newest := since
	for _, ref := range refs {
		type entry struct {
			commit
			paths map[string]bool
		}
		entries := map[string]*entry{}
		for _, p := range paths {
			var cs []commit
			url := fmt.Sprintf("%s/commits?sha=%s&path=%s&since=%s&per_page=100",
				api, ref, p, since.UTC().Format(time.RFC3339))
			if err := get(url, &cs); err != nil {
				return false, err
			}
			for _, c := range cs {
				if !c.Commit.Committer.Date.After(since) {
					continue
				}
				e := entries[c.Sha]
				if e == nil {
					e = &entry{commit: c, paths: map[string]bool{}}
					entries[c.Sha] = e
				}
				e.paths[p] = true
			}
		}
		if len(entries) == 0 {
			continue
		}
		sorted := make([]*entry, 0, len(entries))
		for _, e := range entries {
			sorted = append(sorted, e)
		}
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].Commit.Committer.Date.Before(sorted[j].Commit.Committer.Date)
		})
		fmt.Println(ref)
		for _, e := range sorted {
			var flags []string
			if e.paths["src/text/template/parse"] {
				flags = append(flags, "parse")
			}
			if e.paths["src/html/template"] {
				if ref != "master" {
					attention = true
				}
				esc, err := touchesEscape(e.Sha)
				if err != nil {
					return false, err
				}
				if esc {
					flags = append(flags, "escape")
				}
			}
			subject, _, _ := strings.Cut(e.Commit.Message, "\n")
			suffix := ""
			if len(flags) > 0 {
				suffix = " [" + strings.Join(flags, ", ") + "]"
			}
			fmt.Printf("  %s %s %s%s\n", e.Sha[:7], e.Commit.Committer.Date.Format("2006-01-02"), subject, suffix)
			if e.Commit.Committer.Date.After(newest) {
				newest = e.Commit.Committer.Date
				newestSha = e.Sha
			}
		}
	}
	if attention {
		fmt.Println("SECURITY: release-branch commits touch html/template; same-day sync required")
	}

	var releases []struct {
		Version string
		Stable  bool
	}
	if err := get("https://go.dev/dl/?mode=json", &releases); err != nil {
		return false, err
	}
	if len(releases) > 0 && newer(releases[0].Version, release) {
		fmt.Printf("STALE: latest stable %s is newer than VERSION %s\n", releases[0].Version, release)
		attention = true
	}

	if repo := os.Getenv("GITHUB_REPOSITORY"); repo != "" {
		var tags []struct{ Name string }
		if err := get("https://api.github.com/repos/"+repo+"/tags?per_page=100", &tags); err != nil {
			return false, err
		}
		prefix := fmt.Sprintf("v0.%d.", minor)
		tagged := false
		for _, tag := range tags {
			if strings.HasPrefix(tag.Name, prefix) {
				tagged = true
				break
			}
		}
		if !tagged {
			fmt.Printf("RELEASE: %s is synced but no %s* tag exists\n", release, prefix)
			attention = true
		}
	}

	if newestSha == "" && !attention {
		fmt.Printf("clean: no unported commits since %s\n", alerted[:7])
	}
	if advance && newestSha != "" {
		if err := os.WriteFile(markerPath("last_alerted"), []byte(newestSha+"\n"), 0o644); err != nil {
			return false, err
		}
	}
	return attention, nil
}

func markerPath(name string) string {
	return "cmd/upstreamwatch/" + name
}

func marker(name string) (string, error) {
	data, err := os.ReadFile(markerPath(name))
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(string(data))
	if len(sha) < 7 {
		return "", fmt.Errorf("%s: not a commit sha", markerPath(name))
	}
	return sha, nil
}

// touchesEscape reports whether the commit changes html/template's
// escape files, the security-sensitive core of the autoescaper.
func touchesEscape(sha string) (bool, error) {
	var c commit
	if err := get(api+"/commits/"+sha, &c); err != nil {
		return false, err
	}
	for _, f := range c.Files {
		if strings.HasPrefix(f.Filename, "src/html/template/escape") {
			return true, nil
		}
	}
	return false, nil
}

func parseRelease(s string) (major, minor, patch int, err error) {
	if n, _ := fmt.Sscanf(s, "go%d.%d.%d", &major, &minor, &patch); n < 2 {
		return 0, 0, 0, fmt.Errorf("unrecognized release %q", s)
	}
	return major, minor, patch, nil
}

func newer(a, b string) bool {
	amaj, amin, apat, err := parseRelease(a)
	if err != nil {
		return false
	}
	bmaj, bmin, bpat, err := parseRelease(b)
	if err != nil {
		return false
	}
	if amaj != bmaj {
		return amaj > bmaj
	}
	if amin != bmin {
		return amin > bmin
	}
	return apat > bpat
}

func get(url string, v any) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}
