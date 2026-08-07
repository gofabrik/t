package template

import (
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	stdtemplate "text/template"
)

func TestExecuteFuncsPrecedence(t *testing.T) {
	tmpl := Must(New("t").Funcs(FuncMap{
		"greet": func() string { return "registered" },
	}).Parse(`{{greet}}`))

	var b strings.Builder
	err := tmpl.ExecuteFuncs(&b, nil, FuncMap{
		"greet": func() string { return "overlay" },
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := b.String(); got != "overlay" {
		t.Errorf("ExecuteFuncs: got %q, want %q", got, "overlay")
	}

	b.Reset()
	if err := tmpl.Execute(&b, nil); err != nil {
		t.Fatal(err)
	}
	if got := b.String(); got != "registered" {
		t.Errorf("Execute after overlay: got %q, want %q", got, "registered")
	}
}

func TestExecuteFuncsPipeline(t *testing.T) {
	tmpl := Must(New("t").Funcs(FuncMap{
		"tag": func(s string) string { return "registered:" + s },
	}).Parse(`{{"x" | tag}} {{tag "y"}}`))

	var b strings.Builder
	err := tmpl.ExecuteFuncs(&b, nil, FuncMap{
		"tag": func(s string) string { return "overlay:" + s },
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := b.String(); got != "overlay:x overlay:y" {
		t.Errorf("got %q, want %q", got, "overlay:x overlay:y")
	}
}

func TestExecuteFuncsVariadic(t *testing.T) {
	tmpl := Must(New("t").Funcs(FuncMap{
		"join": func(parts ...string) string { return strings.Join(parts, "+") },
	}).Parse(`{{join "a" "b" "c"}}`))

	var b strings.Builder
	err := tmpl.ExecuteFuncs(&b, nil, FuncMap{
		"join": func(parts ...string) string { return strings.Join(parts, "-") },
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := b.String(); got != "a-b-c" {
		t.Errorf("got %q, want %q", got, "a-b-c")
	}
}

func TestExecuteFuncsErrors(t *testing.T) {
	tmpl := Must(New("t").Funcs(FuncMap{
		"greet": func() string { return "" },
	}).Parse(`{{greet}}`))
	bare := Must(New("bare").Parse(`text`))

	for _, tc := range []struct {
		name  string
		tmpl  *Template
		funcs FuncMap
	}{
		{"unregistered name", tmpl, FuncMap{"nope": func() string { return "" }}},
		{"not a function", tmpl, FuncMap{"greet": 42}},
		{"bad signature", tmpl, FuncMap{"greet": func() (int, int) { return 0, 0 }}},
		{"no registered functions", bare, FuncMap{"greet": func() string { return "" }}},
	} {
		if err := tc.tmpl.ExecuteFuncs(io.Discard, nil, tc.funcs); err == nil {
			t.Errorf("%s: no error", tc.name)
		}
	}
}

func TestExecuteFuncsConcurrent(t *testing.T) {
	tmpl := Must(New("t").Funcs(FuncMap{
		"id": func() string { return "" },
	}).Parse(`{{range .}}{{id}}{{end}}`))

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			want := fmt.Sprint(i)
			var b strings.Builder
			err := tmpl.ExecuteFuncs(&b, make([]int, 100), FuncMap{
				"id": func() string { return want },
			})
			if err != nil {
				t.Error(err)
				return
			}
			if b.String() != strings.Repeat(want, 100) {
				t.Errorf("overlay %s leaked into another execution", want)
			}
		}()
	}
	wg.Wait()
}

type funcError struct{ code int }

func (e *funcError) Error() string { return fmt.Sprintf("func error %d", e.code) }

func TestExecuteFuncsErrorWrapping(t *testing.T) {
	tmpl := Must(New("t").Funcs(FuncMap{
		"fail": func() (string, error) { return "", nil },
	}).Parse(`{{fail}}`))

	err := tmpl.ExecuteFuncs(io.Discard, nil, FuncMap{
		"fail": func() (string, error) { return "", &funcError{code: 7} },
	})
	var fe *funcError
	if !errors.As(err, &fe) || fe.code != 7 {
		t.Errorf("error %v does not wrap the overlay function's error", err)
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

type benchData struct {
	Title string
	Items []string
}

var benchInput = benchData{
	Title: "benchmark",
	Items: []string{"one", "two", "three", "four", "five", "six", "seven", "eight"},
}

const benchText = `{{title .Title}}{{range .Items}} {{.}}{{sep}}{{end}}`

func BenchmarkExecute(b *testing.B) {
	tmpl := Must(New("t").Funcs(FuncMap{
		"title": strings.ToUpper,
		"sep":   func() string { return ";" },
	}).Parse(benchText))
	b.ResetTimer()
	for range b.N {
		if err := tmpl.Execute(io.Discard, benchInput); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkExecuteStdlib(b *testing.B) {
	tmpl := stdtemplate.Must(stdtemplate.New("t").Funcs(stdtemplate.FuncMap{
		"title": strings.ToUpper,
		"sep":   func() string { return ";" },
	}).Parse(benchText))
	b.ResetTimer()
	for range b.N {
		if err := tmpl.Execute(io.Discard, benchInput); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkExecuteFuncs(b *testing.B) {
	tmpl := Must(New("t").Funcs(FuncMap{
		"title": strings.ToUpper,
		"sep":   func() string { return ";" },
	}).Parse(benchText))
	overlay := FuncMap{"sep": func() string { return "," }}
	b.ResetTimer()
	for range b.N {
		if err := tmpl.ExecuteFuncs(io.Discard, benchInput, overlay); err != nil {
			b.Fatal(err)
		}
	}
}
