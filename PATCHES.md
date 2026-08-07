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
- internal/testenv: minimal reimplementation providing MustHaveGoBuild
  and GoToolPath, the two functions the template tests use. The
  standard library's version pulls internal/cfg, internal/goarch and
  internal/platform.

## Rewritten declarations

- html/template/content.go: CSS, HTML, HTMLAttr, JS, JSStr, URL and
  Srcset are declared as aliases of the standard library's types, so
  values typed by either package interoperate. cmd/sync performs the
  rewrite.

## Patched files

None yet.
