package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// applyRelease syncs the release at goroot and writes its plan outside the
// repository. It returns 0 on success, 2 on merge conflict, and 1 otherwise.
func applyRelease(repoRoot, goroot, syncedSHA, planOut string, stdout, stderr io.Writer) int {
	if planOut == "" {
		fmt.Fprintf(stderr, "syncrelease apply: plan output path is required\n")
		return 1
	}
	// Resolve symlinks so planOut cannot bypass the clean-tree invariant.
	planAbs, err := filepath.Abs(planOut)
	if err != nil {
		fmt.Fprintf(stderr, "syncrelease apply: %v\n", err)
		return 1
	}
	planDir, err := filepath.EvalSymlinks(filepath.Dir(planAbs))
	if err != nil {
		fmt.Fprintf(stderr, "syncrelease apply: resolve plan dir: %v\n", err)
		return 1
	}
	absPlan := filepath.Join(planDir, filepath.Base(planAbs))
	realRepo, err := filepath.EvalSymlinks(repoRoot)
	if err != nil {
		fmt.Fprintf(stderr, "syncrelease apply: %v\n", err)
		return 1
	}
	if fi, err := os.Lstat(absPlan); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		fmt.Fprintf(stderr, "syncrelease apply: plan output must not be a symlink (got %s)\n", planOut)
		return 1
	}
	if strings.HasPrefix(absPlan+string(filepath.Separator), realRepo+string(filepath.Separator)) {
		fmt.Fprintf(stderr, "syncrelease apply: plan output must be outside the repository (got %s)\n", absPlan)
		return 1
	}
	planOut = absPlan

	// git add -A must capture only cmd/sync output.
	preCheck, err := gitOutput(repoRoot, "status", "--porcelain")
	if err != nil {
		fmt.Fprintf(stderr, "syncrelease apply: git status: %v\n", err)
		return 1
	}
	if len(bytes.TrimSpace(preCheck)) > 0 {
		fmt.Fprintf(stderr, "syncrelease apply: working tree is not clean; stash or commit changes first\n")
		return 1
	}

	gorootVersion, err := os.ReadFile(filepath.Join(goroot, "VERSION"))
	if err != nil {
		fmt.Fprintf(stderr, "syncrelease apply: read GOROOT VERSION: %v\n", err)
		return 1
	}
	relStr, _, _ := strings.Cut(strings.TrimSpace(string(gorootVersion)), "\n")
	release, err := parseRelease(relStr)
	if err != nil {
		fmt.Fprintf(stderr, "syncrelease apply: %v\n", err)
		return 1
	}

	// main's VERSION keeps the plan identical when rerunning a sync branch.
	mainVersion, err := gitOutput(repoRoot, "show", "main:VERSION")
	if err != nil {
		fmt.Fprintf(stderr, "syncrelease apply: read main VERSION: %v\n", err)
		return 1
	}
	currentLine, _, _ := strings.Cut(strings.TrimSpace(string(mainVersion)), "\n")
	current, err := parseRelease(currentLine)
	if err != nil {
		fmt.Fprintf(stderr, "syncrelease apply: %v\n", err)
		return 1
	}

	plan := planFor(current, release)

	if syncedSHA == "" {
		tag := release.String()
		lsOut, err := gitOutput(repoRoot, "ls-remote", "https://github.com/golang/go",
			"refs/tags/"+tag+"*")
		if err != nil {
			fmt.Fprintf(stderr, "syncrelease apply: ls-remote: %v\n", err)
			return 1
		}
		syncedSHA, err = parseLsRemote(lsOut, tag)
		if err != nil {
			fmt.Fprintf(stderr, "syncrelease apply: %v\n", err)
			return 1
		}
	}
	plan.SyncedSHA = syncedSHA

	binDir, err := os.MkdirTemp("", "syncrelease-bin-*")
	if err != nil {
		fmt.Fprintf(stderr, "syncrelease apply: %v\n", err)
		return 1
	}
	defer os.RemoveAll(binDir)
	syncBin := filepath.Join(binDir, "sync")
	buildCmd := exec.Command("go", "build", "-o", syncBin, "./cmd/sync")
	buildCmd.Dir = repoRoot
	buildCmd.Stdout = stdout
	buildCmd.Stderr = stderr
	if err := buildCmd.Run(); err != nil {
		fmt.Fprintf(stderr, "syncrelease apply: go build cmd/sync: %v\n", err)
		return 1
	}

	if err := git(repoRoot, stdout, stderr, "checkout", "upstream"); err != nil {
		fmt.Fprintf(stderr, "syncrelease apply: checkout upstream: %v\n", err)
		return 1
	}

	syncCmd := exec.Command(syncBin, goroot)
	syncCmd.Dir = repoRoot
	syncCmd.Stdout = stdout
	syncCmd.Stderr = stderr
	if err := syncCmd.Run(); err != nil {
		fmt.Fprintf(stderr, "syncrelease apply: cmd/sync: %v\n", err)
		return 1
	}

	statusOut, err := gitOutput(repoRoot, "status", "--porcelain")
	if err != nil {
		fmt.Fprintf(stderr, "syncrelease apply: git status: %v\n", err)
		return 1
	}
	if len(bytes.TrimSpace(statusOut)) > 0 {
		if err := git(repoRoot, stdout, stderr, "add", "-A"); err != nil {
			fmt.Fprintf(stderr, "syncrelease apply: git add: %v\n", err)
			return 1
		}
		msg := "sync " + release.String()
		if err := git(repoRoot, stdout, stderr, "commit", "-m", msg); err != nil {
			fmt.Fprintf(stderr, "syncrelease apply: git commit: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "committed: %s\n", msg)
	} else {
		fmt.Fprintf(stdout, "upstream already carries %s, skipping commit\n", release.String())
	}

	syncBranch := "sync/" + release.String()
	branchOut, err := gitOutput(repoRoot, "show-ref", "--verify", "refs/heads/"+syncBranch)
	if err != nil || len(bytes.TrimSpace(branchOut)) == 0 {
		if err := git(repoRoot, stdout, stderr, "checkout", "-b", syncBranch, "main"); err != nil {
			fmt.Fprintf(stderr, "syncrelease apply: create %s: %v\n", syncBranch, err)
			return 1
		}
	} else {
		if err := git(repoRoot, stdout, stderr, "checkout", syncBranch); err != nil {
			fmt.Fprintf(stderr, "syncrelease apply: checkout %s: %v\n", syncBranch, err)
			return 1
		}
	}

	// Reruns preserve the existing merge and last_synced value.
	ancestorCmd := exec.Command("git", "merge-base", "--is-ancestor", "upstream", "HEAD")
	ancestorCmd.Dir = repoRoot
	alreadyMerged := ancestorCmd.Run() == nil
	if alreadyMerged {
		fmt.Fprintf(stdout, "already merged: upstream is ancestor of %s\n", syncBranch)
	} else {
		// Keep the merge open so last_synced becomes part of the merge commit.
		mergeMsg := fmt.Sprintf("Merge branch 'upstream': %s", release.String())
		mergeCmd := exec.Command("git", "merge", "--no-commit", "--no-ff", "upstream")
		mergeCmd.Dir = repoRoot
		mergeCmd.Stdout = stdout
		mergeCmd.Stderr = stderr
		if err := mergeCmd.Run(); err != nil {
			conflicted, cerr := hasConflicts(repoRoot)
			// Conflicts must leave a clean checkout for reruns.
			_ = exec.Command("git", "-C", repoRoot, "merge", "--abort").Run()
			if cerr != nil || !conflicted {
				fmt.Fprintf(stderr, "syncrelease apply: merge failed: %v\n", err)
				return 1
			}
			plan.Conflict = true
			if werr := writePlanFile(plan, planOut); werr != nil {
				fmt.Fprintf(stderr, "syncrelease apply: write plan: %v\n", werr)
				return 1
			}
			fmt.Fprintf(stderr, "syncrelease apply: merge conflict; run manually and push %s\n", syncBranch)
			return 2
		}

		lastSyncedPath := filepath.Join(repoRoot, "cmd", "upstreamwatch", "last_synced")
		if err := os.WriteFile(lastSyncedPath, []byte(syncedSHA+"\n"), 0o644); err != nil {
			fmt.Fprintf(stderr, "syncrelease apply: write last_synced: %v\n", err)
			return 1
		}
		if err := git(repoRoot, stdout, stderr, "add", "cmd/upstreamwatch/last_synced"); err != nil {
			fmt.Fprintf(stderr, "syncrelease apply: stage last_synced: %v\n", err)
			return 1
		}

		if err := git(repoRoot, stdout, stderr, "commit", "-m", mergeMsg); err != nil {
			fmt.Fprintf(stderr, "syncrelease apply: merge commit: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "merged upstream into %s\n", syncBranch)
	}

	if plan.MajorTurnover {
		if err := applyTurnoverEdits(repoRoot, release, plan, stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "syncrelease apply: turnover edits: %v\n", err)
			return 1
		}
	}

	if err := writePlanFile(plan, planOut); err != nil {
		fmt.Fprintf(stderr, "syncrelease apply: write plan: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "plan written to %s\n", planOut)
	return 0
}

func applyTurnoverEdits(repoRoot string, release goRelease, plan Plan, stdout, stderr io.Writer) error {
	prev := release.minor - 1

	goModPath := filepath.Join(repoRoot, "go.mod")
	goModData, err := os.ReadFile(goModPath)
	if err != nil {
		return fmt.Errorf("read go.mod: %v", err)
	}
	newGoMod, err := rewriteGoMod(goModData, plan.Directive)
	if err != nil {
		return fmt.Errorf("rewrite go.mod: %v", err)
	}

	testYMLPath := filepath.Join(repoRoot, ".github", "workflows", "test.yml")
	testYMLData, err := os.ReadFile(testYMLPath)
	if err != nil {
		return fmt.Errorf("read test.yml: %v", err)
	}
	newTestYML, err := rewriteTestYML(testYMLData, prev, release.minor)
	if err != nil {
		return fmt.Errorf("rewrite test.yml: %v", err)
	}

	goModChanged := !bytes.Equal(newGoMod, goModData)
	testYMLChanged := !bytes.Equal(newTestYML, testYMLData)
	if !goModChanged && !testYMLChanged {
		fmt.Fprintf(stdout, "turnover already applied\n")
		return nil
	}

	var toAdd []string
	if goModChanged {
		if err := os.WriteFile(goModPath, newGoMod, 0o644); err != nil {
			return fmt.Errorf("write go.mod: %v", err)
		}
		toAdd = append(toAdd, "go.mod")
	}
	if testYMLChanged {
		if err := os.WriteFile(testYMLPath, newTestYML, 0o644); err != nil {
			return fmt.Errorf("write test.yml: %v", err)
		}
		toAdd = append(toAdd, ".github/workflows/test.yml")
	}

	if err := git(repoRoot, stdout, stderr, append([]string{"add"}, toAdd...)...); err != nil {
		return fmt.Errorf("stage turnover edits: %v", err)
	}
	turnoverMsg := fmt.Sprintf("turnover: directive %s, matrix %s",
		plan.Directive, strings.Join(plan.Matrix, "/"))
	if err := git(repoRoot, stdout, stderr, "commit", "-m", turnoverMsg); err != nil {
		return fmt.Errorf("commit turnover edits: %v", err)
	}
	fmt.Fprintf(stdout, "turnover edits committed\n")
	return nil
}

func writePlanFile(p Plan, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return writePlan(p, f)
}

func hasConflicts(repoRoot string) (bool, error) {
	out, err := gitOutput(repoRoot, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if len(line) >= 2 {
			switch line[:2] {
			case "UU", "AA", "DD", "AU", "UA", "DU", "UD":
				return true, nil
			}
		}
	}
	return false, nil
}

func git(dir string, stdout, stderr io.Writer, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func gitOutput(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	return cmd.Output()
}
