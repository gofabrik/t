# gofabrik/t

Maintained forks of Go standard library packages.

    import "github.com/gofabrik/t/text/template"
    import "github.com/gofabrik/t/html/template"

The packages track the latest stable Go release; VERSION names the
exact source tree. Release lines follow Go's: v0.26.x corresponds to
Go 1.26, and the module builds with the previous Go release. PATCHES.md
lists every divergence from upstream; in-place edits to upstream files
are marked with a `// patch:` comment at the edit site.

Source is copied by cmd/sync and carries the Go project's license; see
LICENSE.
