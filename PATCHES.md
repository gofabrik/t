# Divergence from upstream

Every difference between this module and the Go source tree named in
VERSION, by file. In-place edits to upstream files are marked with a
`// patch:` comment at the edit site. Import path rewrites performed
by cmd/sync are not listed; they apply uniformly to every file.

## Replaced packages

- internal/godebug: minimal reimplementation. The standard library's
  version is tied to runtime metrics and cannot be vendored. Settings
  are read from the GODEBUG environment variable; IncNonDefault is a
  no-op. Used by html/template/escape.go.
- internal/testenv: minimal reimplementation providing MustHaveGoBuild,
  GoToolPath, and SetGODEBUG, the functions the template tests use. The
  standard library's version pulls internal/cfg, internal/goarch and
  internal/platform.

## Rewritten declarations

- html/template/content.go: CSS, HTML, HTMLAttr, JS, JSStr, URL and
  Srcset are declared as aliases of the standard library's types, so
  values typed by either package interoperate. cmd/sync performs the
  rewrite.

## Added files

- text/template/patch_exec.go (and tests): ExecuteFuncs, the
  per-execution function overlay of golang/go#54450.
- html/template/patch_template.go (and tests): ExecuteFuncs for the
  escaped package.

## Patched files

- text/template/exec.go: the hooks ExecuteFuncs needs: a
  per-execution function map on state, execute delegating to
  executeFuncs, and overlay lookup in evalFunction.
- text/template/exec_test.go: errors.AsType (added in Go 1.26)
  replaced with errors.As, so the module builds with the previous
  release.
- text/template/link_test.go: the probe program gets a go.mod with a
  replace directive pointing at this checkout; the standard library's
  copy resolves through GOROOT and needs none. The temp module derives its
  go directive from the root go.mod so turnovers require no manual update.
