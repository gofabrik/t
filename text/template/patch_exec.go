// The execution addition: the per-execution function overlay accepted
// in golang/go#54450, matching the pending implementation (CL 510738).

package template

import (
	"fmt"
	"io"
	"reflect"
)

// ExecuteFuncs applies the template t to data, writing the output to
// wr. During execution, functions listed in funcs take precedence over
// the ones registered with [Template.Funcs], for this execution only.
// Every name in funcs must already be registered: the overlay rebinds
// names, it never introduces them, so the parsed template and its
// escape analysis stay valid for every execution. The template is not
// modified; executions with different overlays may run concurrently.
//
// Except for function lookup, ExecuteFuncs behaves exactly like
// [Template.Execute].
func (t *Template) ExecuteFuncs(wr io.Writer, data any, funcs FuncMap) error {
	overlay, err := t.overlayFuncs(funcs)
	if err != nil {
		return err
	}
	return t.executeFuncs(wr, data, overlay)
}

func (t *Template) overlayFuncs(funcs FuncMap) (map[string]reflect.Value, error) {
	if len(funcs) == 0 {
		return nil, nil
	}
	if t.common != nil {
		t.muFuncs.RLock()
		defer t.muFuncs.RUnlock()
	}
	m := make(map[string]reflect.Value, len(funcs))
	for name, fn := range funcs {
		if t.common == nil {
			return nil, fmt.Errorf("template: overlay function %q is not registered", name)
		}
		if _, ok := t.parseFuncs[name]; !ok {
			return nil, fmt.Errorf("template: overlay function %q is not registered", name)
		}
		v := reflect.ValueOf(fn)
		if v.Kind() != reflect.Func {
			return nil, fmt.Errorf("template: overlay value for %q is not a function", name)
		}
		if err := goodFunc(name, v.Type()); err != nil {
			return nil, err
		}
		m[name] = v
	}
	return m, nil
}

// executeFuncs is the body of both Execute and ExecuteFuncs.
func (t *Template) executeFuncs(wr io.Writer, data any, funcs map[string]reflect.Value) (err error) {
	defer errRecover(&err)
	value, ok := data.(reflect.Value)
	if !ok {
		value = reflect.ValueOf(data)
	}
	state := &state{
		tmpl:  t,
		wr:    wr,
		vars:  []variable{{"$", value}},
		funcs: funcs,
	}
	if t.Tree == nil || t.Root == nil {
		state.errorf("%q is an incomplete or empty template", t.Name())
	}
	state.walk(value, t.Root)
	return
}
