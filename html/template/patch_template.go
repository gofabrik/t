// The execution addition: the per-execution function overlay accepted
// in golang/go#54450, on top of the escaped package.

package template

import (
	"fmt"
	"io"
	"strings"
)

// ExecuteFuncs applies the template t to data, writing the output to
// wr. During execution, functions listed in funcs take precedence over
// the ones registered with [Template.Funcs], for this execution only.
// Every name in funcs must already be registered: the overlay rebinds
// names, it never introduces them, so escape analysis, which reasons
// about pipelines by name before execution, holds for every execution.
// The template is not modified; executions with different overlays may
// run concurrently.
//
// Except for function lookup, ExecuteFuncs behaves exactly like
// [Template.Execute].
func (t *Template) ExecuteFuncs(wr io.Writer, data any, funcs FuncMap) error {
	for name := range funcs {
		if strings.HasPrefix(name, "_html_template") {
			return fmt.Errorf("github.com/gofabrik/t/html/template: cannot overlay internal function %q", name)
		}
	}
	if err := t.escape(); err != nil {
		return err
	}
	return t.text.ExecuteFuncs(wr, data, funcs)
}
