# Releases

## Support model

main tracks the latest stable Go release on one active release line. The go.mod
directive stays one major behind, and CI tests both the previous and current
toolchains. At each major rollover the old v0.<minor>.x line freezes. The new
line still admits the previous toolchain and carries upstream's current fixes.

## Versioning

v0.<go-minor>.<n> counts fork releases within a line, not upstream patch
numbers. v0.26.0 is the first release on the Go 1.26 line; v0.26.1 is the
second, regardless of how many Go 1.26.x patches upstream shipped. At a major
rollover, v0.27.0 begins the Go 1.27 line. VERSION names the exact upstream
release the source was copied from.

## Syncing

### Automated flow

sync.yml (daily schedule and workflow_dispatch) runs the mechanical steps:

1. **detect**: compares go.dev/dl's latest stable release with VERSION; skips
   when current.
2. **fetch**: downloads the release tarball from dl.google.com and verifies
   its sha256 against go.dev/dl's JSON feed.
3. **apply**: builds cmd/sync from the triggering branch, advances the upstream
   branch ("sync goX.Y.Z"), creates sync/goX.Y.Z from main, and merges
   upstream. On a major turnover, also sets go.mod's directive and test.yml's
   matrix. Writes plan.json to RUNNER_TEMP. Exit 2 means merge conflict.
4. **publish**: reads plan.json and converges each artifact toward its target
   state:
   - tracking issue (label `release-sync`, title "Sync goX.Y.Z"): creates if
     absent, reuses the open issue.
   - upstream branch: pushes the advance; already-current remote is a no-op.
   - sync/goX.Y.Z: pushes if absent; an existing remote branch (including a
     human-pushed resolution) is left as-is.
   - PR: opens if none is open, base main, body "Closes #N" with the review
     checklist; a PR closed without merging halts further mutation.
   - conflict state: upstream is pushed, the tracking issue receives the
     resolution commands once (deduplicated by marker), no PR is opened.

Merging the PR closes the tracking issue (main is the default branch).

### Manual steps

Run from the repository root (main branch). The upstream branch has no go.mod;
the tools must be built here.

```sh
go build -o /tmp/syncrelease ./cmd/syncrelease

# Empty output means VERSION is current.
/tmp/syncrelease detect

goroot="$(/tmp/syncrelease fetch go1.27.0)"

# Exit 2 means conflict; plan.json must be outside the repository.
/tmp/syncrelease apply "$goroot" --plan-out /tmp/plan.json

# This read echoes the token; use read -rs to hide it.
printf 'token: ' && read -r GITHUB_TOKEN && export GITHUB_TOKEN
/tmp/syncrelease publish --plan /tmp/plan.json
```

If an installed GOROOT of the target release is available, pass it directly
instead of fetching:

```sh
/tmp/syncrelease apply /usr/local/go --plan-out /tmp/plan.json
```

## Conflict resolution

Take upstream and reapply the local patch in every unmerged file. Expected
sites have `// patch:` markers and are listed in PATCHES.md, but any file may
conflict. publish posts the commands once to the tracking issue and opens no
PR. Run the sequence in a fresh checkout and substitute the SHA from the
notice.

```sh
git fetch origin
git checkout -b sync/go1.27.0 origin/main
git merge --no-commit --no-ff origin/upstream
# resolve conflicts at the // patch: sites (PATCHES.md lists them),
# then record the golang/go commit SHA quoted in the conflict notice
printf '%s\n' 0000000000000000000000000000000000000000 > cmd/upstreamwatch/last_synced
git add -A
git commit -m "Merge branch 'upstream': go1.27.0"
```

On a major turnover, add the turnover commit next:

```sh
go mod edit -go=1.26
sed -i.bak 's/go: \[.*\]/go: ["1.26.x", "1.27.x"]/' .github/workflows/test.yml && rm .github/workflows/test.yml.bak
git add go.mod .github/workflows/test.yml
git commit -m "turnover: directive go 1.26, matrix 1.26.x/1.27.x"
```

Push the resolved branch:

```sh
git push origin sync/go1.27.0
```

The next publish run finds the remote branch and opens the PR.

## Turnover checklist

On a major turnover, cmd/syncrelease updates VERSION, sets the go.mod directive
to the new previous major, and changes the test.yml matrix to
`[previous.x, current.x]`.

Before merging the sync PR, verify:

- Compatibility patches gated on the old floor: the errors.AsType patch at
  text/template/exec_test.go can be dropped once the directive reaches Go 1.26
  (the version that added errors.AsType).
- README version claims: the "ExecuteFuncs is not in Go X.Y" line.
- PATCHES.md accuracy: verify every entry reflects the post-sync divergence.
- Tagging: the new line's first tag (v0.<new-minor>.0) is always a human step.
  After the PR merges and tests pass, tag the merged commit on main:

      git checkout main && git pull
      git tag -a v0.27.0 -m "go1.27.0 sync"
      git push origin v0.27.0

## SYNC_TOKEN

sync.yml requires a fine-grained PAT stored as the secret SYNC_TOKEN.

**Required repository permissions:** Contents (write), Issues (write),
Pull requests (write), Workflows (write). Workflows write is required because
turnover commits edit .github/workflows/test.yml.

**Expiry:** maximum 366 days. Set a calendar reminder to rotate before expiry.
A lapsed token surfaces as a failed sync.yml run, not a silent skip.

**Rotation:** generate a new token and update the secret at
Settings > Secrets and variables > Actions > SYNC_TOKEN.

**Inactivity:** GitHub disables scheduled workflows after 60 days of repository
inactivity. Re-enable at Actions > sync > Enable workflow.

## Monitoring

A nightly workflow runs cmd/upstreamwatch and monitors golang/go's master
branch and its release-branch.go1.X branches (the active upstream release
branches). It fails if a supported release branch changes html/template,
VERSION trails the latest stable Go release, or no tag exists for the current
v0.<minor> line. New commits are posted to the upstream-port issue;
release-branch html/template changes open a security-review issue.
