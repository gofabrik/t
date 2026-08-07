// Package godebug is a minimal stand-in for the standard library's
// internal/godebug, which is tied to the runtime and cannot be
// vendored. Settings are read from the GODEBUG environment variable on
// each use; non-default counting is not implemented.
package godebug

import (
	"os"
	"strings"
)

// A Setting is a single GODEBUG setting.
type Setting struct {
	name string
}

// New returns a Setting for the given name.
func New(name string) *Setting {
	return &Setting{name: name}
}

// Value returns the current value of the setting, or the empty string
// if unset.
func (s *Setting) Value() string {
	for _, kv := range strings.Split(os.Getenv("GODEBUG"), ",") {
		if k, v, ok := strings.Cut(kv, "="); ok && k == s.name {
			return v
		}
	}
	return ""
}

// IncNonDefault does nothing. The standard library counts non-default
// uses in runtime metrics; there is no equivalent to report to here.
func (s *Setting) IncNonDefault() {}
