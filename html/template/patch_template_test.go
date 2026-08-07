package template

import (
	stdtemplate "html/template"
	"io"
	"reflect"
	"strings"
	"testing"
)

const escapeText = `<a href="{{link .}}" title="{{title .}}">{{title .}}</a><script>var x = {{title .}}</script>`

var escapeInputs = []string{
	"plain",
	"<script>alert(1)</script>",
	`javascript:alert(1)`,
	`" onmouseover="alert(1)`,
	"a&b <c>",
}

func escapeFuncs(link, title func(string) string) FuncMap {
	return FuncMap{"link": link, "title": title}
}

var identity = func(s string) string { return s }

// Overlaid functions must feed the same escapers as registered ones:
// for every input, a template executing an overlay equals a template
// whose registered functions return the overlay's values, and both
// equal the standard library.
func TestExecuteFuncsEscapingEquivalence(t *testing.T) {
	overlaid := Must(New("t").Funcs(escapeFuncs(
		func(string) string { return "unused" },
		func(string) string { return "unused" },
	)).Parse(escapeText))
	registered := Must(New("t").Funcs(escapeFuncs(identity, identity)).Parse(escapeText))
	std := stdtemplate.Must(stdtemplate.New("t").Funcs(stdtemplate.FuncMap{
		"link": identity, "title": identity,
	}).Parse(escapeText))

	for _, input := range escapeInputs {
		var viaOverlay, viaRegistered, viaStdlib strings.Builder
		err := overlaid.ExecuteFuncs(&viaOverlay, input,
			FuncMap{"link": identity, "title": identity})
		if err != nil {
			t.Fatal(err)
		}
		if err := registered.Execute(&viaRegistered, input); err != nil {
			t.Fatal(err)
		}
		if err := std.Execute(&viaStdlib, input); err != nil {
			t.Fatal(err)
		}
		if viaOverlay.String() != viaRegistered.String() {
			t.Errorf("input %q: overlay %q != registered %q", input, viaOverlay.String(), viaRegistered.String())
		}
		if viaOverlay.String() != viaStdlib.String() {
			t.Errorf("input %q: overlay %q != stdlib %q", input, viaOverlay.String(), viaStdlib.String())
		}
	}
}

func TestExecuteFuncsUnsafeValueEscaped(t *testing.T) {
	tmpl := Must(New("t").Funcs(FuncMap{
		"link": func() string { return "https://example.com" },
	}).Parse(`<a href="{{link}}">x</a>`))

	var b strings.Builder
	err := tmpl.ExecuteFuncs(&b, nil, FuncMap{
		"link": func() string { return "javascript:alert(1)" },
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := b.String(), `<a href="#ZgotmplZ">x</a>`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExecuteFuncsRejectsInternalNames(t *testing.T) {
	tmpl := Must(New("t").Parse(`<b>{{.}}</b>`))
	err := tmpl.ExecuteFuncs(io.Discard, "x", FuncMap{
		"_html_template_urlescaper": func(args ...any) string { return "" },
	})
	if err == nil {
		t.Fatal("no error overlaying an internal function")
	}
}

func TestExecuteFuncsUnregisteredName(t *testing.T) {
	tmpl := Must(New("t").Parse(`<b>{{.}}</b>`))
	err := tmpl.ExecuteFuncs(io.Discard, "x", FuncMap{
		"nope": func() string { return "" },
	})
	if err == nil {
		t.Fatal("no error overlaying an unregistered function")
	}
}

func TestAPISuperset(t *testing.T) {
	fork := reflect.TypeFor[*Template]()
	std := reflect.TypeFor[*stdtemplate.Template]()
	for i := range std.NumMethod() {
		name := std.Method(i).Name
		if _, ok := fork.MethodByName(name); !ok {
			t.Errorf("stdlib method %s is missing", name)
		}
	}
}
