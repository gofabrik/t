# Releases

## Versioning

Tags follow v0.<go-minor>.<patch>: v0.26.x tracks Go 1.26, and the
first sync of Go 1.27 becomes v0.27.0. VERSION at the module root
names the exact upstream release the source was copied from.

Supported: the current and previous Go major, from a single branch.
The go directive stays at the previous major so the module builds on
both toolchains.

## Syncing

Copy from stable Go releases only, never a release candidate. Sync
every release; if a security release changes text/template or
html/template, sync it the same day.

1. On the upstream branch, run `go run ./cmd/sync <goroot>` against
   the new release's source tree and commit as "sync goX.Y.Z".
2. Merge upstream into main. Expected conflicts are at sites marked
   with `// patch:`; PATCHES.md lists them.
3. Update PATCHES.md if the divergence changed.
4. Run the tests with both supported toolchains. The synced
   standard-library tests must pass with only the changes documented
   in PATCHES.md.

## Releasing

1. Tests pass with both supported toolchains. The release-candidate
   CI job is non-blocking.
2. PATCHES.md current.
3. Push main and upstream.
4. Tag and push:

       git tag -a v0.26.1 -m "go1.26.6 sync"
       git push origin v0.26.1

## Monitoring

A nightly workflow runs `cmd/upstreamwatch` and reports newly seen
upstream commits. It fails if a supported release branch changes
html/template, VERSION trails the latest stable Go release, or no tag
exists for the current v0.<minor> line. New commits go to the
upstream-port issue; release-branch html/template changes open a
security-review issue.
