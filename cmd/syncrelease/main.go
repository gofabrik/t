// Syncrelease syncs an upstream Go release from a full repository checkout.
// Apply returns 2 for merge conflicts; all commands return 1 for other errors.
package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

var httpClient = &http.Client{Timeout: 30 * time.Second}

func main() {
	os.Exit(runCmd(os.Args[1:], os.Stdout, os.Stderr))
}

func runCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: syncrelease <detect|plan|fetch|apply|publish> ...")
		return 1
	}
	switch args[0] {
	case "detect":
		return cmdDetect(stdout, stderr)
	case "plan":
		return cmdPlan(args[1:], stdout, stderr)
	case "fetch":
		return cmdFetch(args[1:], stdout, stderr)
	case "apply":
		return cmdApply(args[1:], stdout, stderr)
	case "publish":
		return cmdPublish(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "syncrelease: unknown command %q\n", args[0])
		return 1
	}
}

func cmdDetect(stdout, stderr io.Writer) int {
	resp, err := httpClient.Get("https://go.dev/dl/?mode=json")
	if err != nil {
		fmt.Fprintln(stderr, "syncrelease detect:", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(stderr, "syncrelease detect: dl JSON:", resp.Status)
		return 1
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintln(stderr, "syncrelease detect:", err)
		return 1
	}
	latest, err := latestStable(body)
	if err != nil {
		fmt.Fprintln(stderr, "syncrelease detect:", err)
		return 1
	}
	current, err := readCurrentVersion(".")
	if err != nil {
		fmt.Fprintln(stderr, "syncrelease detect:", err)
		return 1
	}
	if latest.newerThan(current) {
		fmt.Fprintln(stdout, latest.String())
	}
	return 0
}

func cmdPlan(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: syncrelease plan <release>")
		return 1
	}
	release, err := parseRelease(args[0])
	if err != nil {
		fmt.Fprintln(stderr, "syncrelease plan:", err)
		return 1
	}
	current, err := readCurrentVersion(".")
	if err != nil {
		fmt.Fprintln(stderr, "syncrelease plan:", err)
		return 1
	}
	plan := planFor(current, release)
	if err := writePlan(plan, stdout); err != nil {
		fmt.Fprintln(stderr, "syncrelease plan:", err)
		return 1
	}
	return 0
}

func cmdFetch(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: syncrelease fetch <release>")
		return 1
	}
	if _, err := parseRelease(args[0]); err != nil {
		fmt.Fprintln(stderr, "syncrelease fetch:", err)
		return 1
	}
	path, err := fetchRelease(args[0])
	if err != nil {
		fmt.Fprintln(stderr, "syncrelease fetch:", err)
		return 1
	}
	fmt.Fprintln(stdout, path)
	return 0
}

func cmdApply(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: syncrelease apply <goroot> --plan-out FILE [--synced-sha SHA]")
		return 1
	}
	goroot := args[0]
	var syncedSHA, planOut string
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--synced-sha":
			if i+1 >= len(rest) {
				fmt.Fprintln(stderr, "syncrelease apply: --synced-sha requires a value")
				return 1
			}
			i++
			syncedSHA = rest[i]
		case "--plan-out":
			if i+1 >= len(rest) {
				fmt.Fprintln(stderr, "syncrelease apply: --plan-out requires a value")
				return 1
			}
			i++
			planOut = rest[i]
		default:
			fmt.Fprintf(stderr, "syncrelease apply: unknown flag %q\n", rest[i])
			return 1
		}
	}
	if planOut == "" {
		fmt.Fprintln(stderr, "syncrelease apply: --plan-out is required")
		return 1
	}
	repoRoot, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, "syncrelease apply:", err)
		return 1
	}
	return applyRelease(repoRoot, goroot, syncedSHA, planOut, stdout, stderr)
}

func cmdPublish(args []string, stdout, stderr io.Writer) int {
	var planFile string
	var dryRun bool
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--plan":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "syncrelease publish: --plan requires a value")
				return 1
			}
			i++
			planFile = args[i]
		case "--dry-run":
			dryRun = true
		default:
			fmt.Fprintf(stderr, "syncrelease publish: unknown flag %q\n", args[i])
			return 1
		}
	}
	if planFile == "" {
		fmt.Fprintln(stderr, "syncrelease publish: --plan is required")
		return 1
	}
	plan, err := loadPlan(planFile)
	if err != nil {
		fmt.Fprintln(stderr, "syncrelease publish:", err)
		return 1
	}
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		fmt.Fprintln(stderr, "syncrelease publish: GITHUB_TOKEN not set")
		return 1
	}
	repoRoot, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, "syncrelease publish:", err)
		return 1
	}
	owner, repo, err := resolveOwnerRepo(repoRoot)
	if err != nil {
		fmt.Fprintln(stderr, "syncrelease publish:", err)
		return 1
	}
	pub := newPublisher(repoRoot, token, owner, repo)
	pub.dryRun = dryRun
	if err := pub.reconcile(plan, stdout); err != nil {
		fmt.Fprintln(stderr, "syncrelease publish:", err)
		return 1
	}
	return 0
}
