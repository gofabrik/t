package main

import (
	"fmt"
	"regexp"
	"strconv"
)

type goRelease struct {
	major, minor, patch int
}

var releaseRE = regexp.MustCompile(`^go(\d+)\.(\d+)(\.(\d+))?$`)

// parseRelease accepts only Go release versions, not prereleases or suffixes.
func parseRelease(s string) (goRelease, error) {
	m := releaseRE.FindStringSubmatch(s)
	if m == nil {
		return goRelease{}, fmt.Errorf("unrecognized release %q", s)
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	patch := 0
	if m[4] != "" {
		patch, _ = strconv.Atoi(m[4])
	}
	return goRelease{major: major, minor: minor, patch: patch}, nil
}

func (r goRelease) newerThan(b goRelease) bool {
	if r.major != b.major {
		return r.major > b.major
	}
	if r.minor != b.minor {
		return r.minor > b.minor
	}
	return r.patch > b.patch
}

// isTurnover identifies syncs that require support-window edits.
func (current goRelease) isTurnover(next goRelease) bool {
	return next.major != current.major || next.minor != current.minor
}

func (r goRelease) String() string {
	return fmt.Sprintf("go%d.%d.%d", r.major, r.minor, r.patch)
}
