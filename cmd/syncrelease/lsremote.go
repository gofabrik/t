package main

import (
	"bytes"
	"fmt"
	"strings"
)

// parseLsRemote returns an exact tag's peeled SHA when present, or its direct SHA.
func parseLsRemote(out []byte, tag string) (string, error) {
	peeled := "refs/tags/" + tag + "^{}"
	plain := "refs/tags/" + tag
	var plainSHA, peeledSHA string
	for _, line := range bytes.Split(out, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		parts := strings.SplitN(string(line), "\t", 2)
		if len(parts) != 2 {
			continue
		}
		sha, ref := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		switch ref {
		case peeled:
			peeledSHA = sha
		case plain:
			plainSHA = sha
		}
	}
	if peeledSHA != "" {
		return peeledSHA, nil
	}
	if plainSHA != "" {
		return plainSHA, nil
	}
	return "", fmt.Errorf("tag %s not found", tag)
}
