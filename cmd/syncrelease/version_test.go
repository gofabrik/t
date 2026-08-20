package main

import "testing"

func TestParseRelease(t *testing.T) {
	cases := []struct {
		in      string
		wantErr bool
		major   int
		minor   int
		patch   int
	}{
		{"go1.27.0", false, 1, 27, 0},
		{"go1.26.6", false, 1, 26, 6},
		{"go1.25", false, 1, 25, 0},
		{"go2.0.0", false, 2, 0, 0},
		{"go1.27rc1", true, 0, 0, 0},
		{"go1.28rc2", true, 0, 0, 0},
		{"go1.27.0-beta1", true, 0, 0, 0},
		{"go1.27beta1", true, 0, 0, 0},
		{"go1.27.0extra", true, 0, 0, 0},
		{"notarelease", true, 0, 0, 0},
		{"go1", true, 0, 0, 0},
		{"", true, 0, 0, 0},
	}
	for _, c := range cases {
		r, err := parseRelease(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("parseRelease(%q) error = %v, wantErr %v", c.in, err, c.wantErr)
			continue
		}
		if !c.wantErr {
			if r.major != c.major || r.minor != c.minor || r.patch != c.patch {
				t.Errorf("parseRelease(%q) = {%d,%d,%d}, want {%d,%d,%d}",
					c.in, r.major, r.minor, r.patch, c.major, c.minor, c.patch)
			}
		}
	}
}

func TestNewerThan(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"go1.27.0", "go1.26.6", true},
		{"go1.26.6", "go1.27.0", false},
		{"go1.26.6", "go1.26.5", true},
		{"go1.26.5", "go1.26.6", false},
		{"go1.26.6", "go1.26.6", false},
		{"go2.0.0", "go1.27.0", true},
		{"go1.27.0", "go2.0.0", false},
	}
	for _, c := range cases {
		a, _ := parseRelease(c.a)
		b, _ := parseRelease(c.b)
		if got := a.newerThan(b); got != c.want {
			t.Errorf("%s.newerThan(%s) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestIsTurnover(t *testing.T) {
	cases := []struct {
		current, next string
		want          bool
	}{
		{"go1.26.6", "go1.27.0", true},
		{"go1.26.5", "go1.26.6", false},
		{"go1.26.0", "go1.26.6", false},
		{"go1.27.0", "go2.0.0", true},
		{"go1.26.6", "go1.26.6", false},
	}
	for _, c := range cases {
		cur, _ := parseRelease(c.current)
		nxt, _ := parseRelease(c.next)
		if got := cur.isTurnover(nxt); got != c.want {
			t.Errorf("%s.isTurnover(%s) = %v, want %v", c.current, c.next, got, c.want)
		}
	}
}

func TestReleaseString(t *testing.T) {
	r, _ := parseRelease("go1.27.0")
	if s := r.String(); s != "go1.27.0" {
		t.Errorf("String() = %q, want %q", s, "go1.27.0")
	}
}
