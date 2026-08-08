# gofabrik/t

Maintained forks of Go standard library packages.

    import "github.com/gofabrik/t/text/template"
    import "github.com/gofabrik/t/html/template"

Both packages add ExecuteFuncs, which applies a function map to one
execution without modifying the template. It can replace only
registered functions, which keeps html/template's escape analysis
valid. The API follows golang/go#54450.

## Why

The standard template packages have no way to bind functions per
execution. To expose request-scoped values such as CSRF tokens or
translations, callers must clone the template for each render or
pass the values in the template data. Hugo and Gitea maintainers
requested per-execution functions in golang/go#36462 and
golang/go#54450. The proposal was accepted in 2022, and CL 510738
implemented it but was never merged. ExecuteFuncs is not in Go 1.27.

This fork includes the change from CL 510738 and keeps its
ExecuteFuncs signature. Apart from the documented patches, the
packages mirror the standard library, so switching in either
direction is an import-path change. The fork will stop receiving
releases if Go adds ExecuteFuncs or another maintained package
provides an equivalent API.

The packages track the latest stable Go release; VERSION names the
exact source tree. Each release line tracks the matching Go version:
v0.26.x tracks Go 1.26 and builds with Go 1.25 or newer. PATCHES.md
lists every divergence from upstream; in-place edits to upstream
files are marked with a `// patch:` comment at the edit site.

Source is copied by cmd/sync and carries the Go project's license; see
LICENSE. RELEASE.md describes the support window and the release
steps.
