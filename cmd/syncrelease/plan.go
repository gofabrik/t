package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// Plan describes a release sync in plan.json.
type Plan struct {
	Release       string   `json:"release"`
	Current       string   `json:"current"`
	MajorTurnover bool     `json:"major_turnover"`
	Directive     string   `json:"directive"`
	Matrix        []string `json:"matrix"`
	Checklist     []string `json:"checklist,omitempty"`
	Conflict      bool     `json:"conflict,omitempty"`
	SyncedSHA     string   `json:"synced_sha,omitempty"`
}

// planFor always records the support window, even for patch-only syncs.
func planFor(current, release goRelease) Plan {
	p := Plan{
		Release:       release.String(),
		Current:       current.String(),
		MajorTurnover: current.isTurnover(release),
	}
	if p.MajorTurnover {
		prev := release.minor - 1
		p.Directive = fmt.Sprintf("go %d.%d", release.major, prev)
		p.Matrix = []string{
			fmt.Sprintf("%d.%d.x", release.major, prev),
			fmt.Sprintf("%d.%d.x", release.major, release.minor),
		}
		p.Checklist = []string{
			fmt.Sprintf("verify compatibility patches gated on go %d.%d floor", current.major, current.minor-1),
			"update README version claims",
			"verify PATCHES.md accuracy",
			fmt.Sprintf("tag v0.%d.0 after merge", release.minor),
		}
	} else {
		prev := current.minor - 1
		p.Directive = fmt.Sprintf("go %d.%d", current.major, prev)
		p.Matrix = []string{
			fmt.Sprintf("%d.%d.x", current.major, prev),
			fmt.Sprintf("%d.%d.x", current.major, current.minor),
		}
	}
	return p
}

func writePlan(p Plan, w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(p)
}

func loadPlan(path string) (Plan, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Plan{}, err
	}
	var p Plan
	if err := json.Unmarshal(data, &p); err != nil {
		return Plan{}, fmt.Errorf("%s: %v", path, err)
	}
	return p, nil
}

func rewriteGoMod(data []byte, directive string) ([]byte, error) {
	re := regexp.MustCompile(`(?m)^go \d+\.\d+(\.\d+)?$`)
	if !re.Match(data) {
		return nil, fmt.Errorf("go directive not found in go.mod")
	}
	return re.ReplaceAll(data, []byte(directive)), nil
}

func rewriteTestYML(data []byte, prev, curr int) ([]byte, error) {
	re := regexp.MustCompile(`go: \[.*\]`)
	if !re.Match(data) {
		return nil, fmt.Errorf("go matrix not found in test.yml")
	}
	replacement := fmt.Sprintf(`go: ["1.%d.x", "1.%d.x"]`, prev, curr)
	return re.ReplaceAll(data, []byte(replacement)), nil
}

// latestStable ignores unstable and non-release entries.
func latestStable(body []byte) (goRelease, error) {
	var releases []struct {
		Version string
		Stable  bool
	}
	if err := json.Unmarshal(body, &releases); err != nil {
		return goRelease{}, err
	}
	for _, rel := range releases {
		if !rel.Stable {
			continue
		}
		r, err := parseRelease(rel.Version)
		if err != nil {
			continue
		}
		return r, nil
	}
	return goRelease{}, fmt.Errorf("no stable release found")
}

func dlSHA256(body []byte, release string) (filename, sha256 string, err error) {
	var releases []struct {
		Version string
		Files   []struct {
			Filename string
			Sha256   string
			Kind     string
		}
	}
	if err = json.Unmarshal(body, &releases); err != nil {
		return
	}
	for _, rel := range releases {
		if rel.Version != release {
			continue
		}
		for _, f := range rel.Files {
			if f.Kind == "source" {
				return f.Filename, f.Sha256, nil
			}
		}
	}
	err = fmt.Errorf("source file for %s not found in dl JSON", release)
	return
}

func readCurrentVersion(dir string) (goRelease, error) {
	data, err := os.ReadFile(dir + "/VERSION")
	if err != nil {
		return goRelease{}, err
	}
	line, _, _ := strings.Cut(string(data), "\n")
	return parseRelease(strings.TrimSpace(line))
}
