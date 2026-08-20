package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type tarEntry struct {
	name     string
	typeflag byte
	body     string
	linkname string
}

func makeTar(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{
			Name:     e.name,
			Typeflag: e.typeflag,
			Linkname: e.linkname,
			Size:     int64(len(e.body)),
			Mode:     0o644,
		}
		if e.typeflag == tar.TypeDir {
			hdr.Mode = 0o755
			hdr.Size = 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if len(e.body) > 0 {
			if _, err := io.WriteString(tw, e.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestHardenedExtract_Clean(t *testing.T) {
	data := makeTar(t, []tarEntry{
		{name: "go/", typeflag: tar.TypeDir},
		{name: "go/src/", typeflag: tar.TypeDir},
		{name: "go/src/hello.go", typeflag: tar.TypeReg, body: "package main"},
	})
	dir := t.TempDir()
	if err := hardenedExtract(bytes.NewReader(data), dir); err != nil {
		t.Fatalf("clean extract failed: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "go", "src", "hello.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "package main" {
		t.Errorf("content = %q, want %q", got, "package main")
	}
}

func TestHardenedExtract_AbsolutePath(t *testing.T) {
	data := makeTar(t, []tarEntry{
		{name: "/etc/passwd", typeflag: tar.TypeReg, body: "root"},
	})
	dir := t.TempDir()
	if err := hardenedExtract(bytes.NewReader(data), dir); err == nil {
		t.Error("expected error for absolute path entry, got nil")
	}
}

func TestHardenedExtract_DotDotTraversal(t *testing.T) {
	data := makeTar(t, []tarEntry{
		{name: "../evil.txt", typeflag: tar.TypeReg, body: "pwned"},
	})
	dir := t.TempDir()
	if err := hardenedExtract(bytes.NewReader(data), dir); err == nil {
		t.Error("expected error for .. traversal, got nil")
	}
}

// TestHardenedExtract_DotDotNested pins rejection before path cleaning.
func TestHardenedExtract_DotDotNested(t *testing.T) {
	data := makeTar(t, []tarEntry{
		{name: "go/src/../../x", typeflag: tar.TypeReg, body: "pwned"},
	})
	dir := t.TempDir()
	if err := hardenedExtract(bytes.NewReader(data), dir); err == nil {
		t.Error("expected error for nested .. traversal (go/src/../../x), got nil")
	}
}

// TestHardenedExtract_InteriorDotDot pins rejection of non-escaping ".." paths.
func TestHardenedExtract_InteriorDotDot(t *testing.T) {
	data := makeTar(t, []tarEntry{
		{name: "go/a/../b", typeflag: tar.TypeReg, body: "safe-ish"},
	})
	dir := t.TempDir()
	if err := hardenedExtract(bytes.NewReader(data), dir); err == nil {
		t.Error("expected error for interior .. element (go/a/../b), got nil")
	}
}

func TestHardenedExtract_Symlink(t *testing.T) {
	data := makeTar(t, []tarEntry{
		{name: "link", typeflag: tar.TypeSymlink, linkname: "/etc/passwd"},
	})
	dir := t.TempDir()
	if err := hardenedExtract(bytes.NewReader(data), dir); err == nil {
		t.Error("expected error for symlink entry, got nil")
	}
}

func TestHardenedExtract_Hardlink(t *testing.T) {
	data := makeTar(t, []tarEntry{
		{name: "go/real.go", typeflag: tar.TypeReg, body: "x"},
		{name: "link", typeflag: tar.TypeLink, linkname: "go/real.go"},
	})
	dir := t.TempDir()
	if err := hardenedExtract(bytes.NewReader(data), dir); err == nil {
		t.Error("expected error for hardlink entry, got nil")
	}
}

func TestHardenedExtract_SpecialFile(t *testing.T) {
	data := makeTar(t, []tarEntry{
		{name: "device", typeflag: tar.TypeChar},
	})
	dir := t.TempDir()
	if err := hardenedExtract(bytes.NewReader(data), dir); err == nil {
		t.Error("expected error for special file entry, got nil")
	}
}

func TestFetch_HTTPPipeline(t *testing.T) {
	archiveData := makeTar(t, []tarEntry{
		{name: "go/", typeflag: tar.TypeDir},
		{name: "go/VERSION", typeflag: tar.TypeReg, body: "go1.27.0"},
	})
	sum := sha256.Sum256(archiveData)
	correctSHA := hex.EncodeToString(sum[:])
	badSum := sum
	badSum[0] ^= 0xff
	badSHA := hex.EncodeToString(badSum[:])

	type dlFileJSON struct {
		Filename string `json:"Filename"`
		Sha256   string `json:"Sha256"`
		Kind     string `json:"Kind"`
	}
	type dlEntryJSON struct {
		Version string       `json:"Version"`
		Stable  bool         `json:"Stable"`
		Files   []dlFileJSON `json:"Files"`
	}

	makeServer := func(sha string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/dl.json":
				json.NewEncoder(w).Encode([]dlEntryJSON{{
					Version: "go1.27.0",
					Stable:  true,
					Files: []dlFileJSON{{
						Filename: "go1.27.0.src.tar.gz",
						Sha256:   sha,
						Kind:     "source",
					}},
				}})
			case "/go1.27.0.src.tar.gz":
				w.Write(archiveData)
			default:
				http.NotFound(w, r)
			}
		}))
	}

	t.Run("checksum mismatch", func(t *testing.T) {
		srv := makeServer(badSHA)
		defer srv.Close()
		f := fetcher{
			dlJSON:  srv.URL + "/dl.json",
			dlBase:  srv.URL + "/",
			jsonCli: srv.Client(),
			dlCli:   srv.Client(),
		}
		dir, err := f.fetch("go1.27.0")
		if err == nil {
			t.Error("want error for checksum mismatch, got nil")
			os.RemoveAll(filepath.Dir(dir))
			return
		}
		if !strings.Contains(err.Error(), "mismatch") {
			t.Errorf("error = %q, want message containing 'mismatch'", err)
		}
	})

	t.Run("happy path", func(t *testing.T) {
		srv := makeServer(correctSHA)
		defer srv.Close()
		f := fetcher{
			dlJSON:  srv.URL + "/dl.json",
			dlBase:  srv.URL + "/",
			jsonCli: srv.Client(),
			dlCli:   srv.Client(),
		}
		dir, err := f.fetch("go1.27.0")
		if err != nil {
			t.Fatalf("fetch: %v", err)
		}
		t.Cleanup(func() { os.RemoveAll(filepath.Dir(dir)) })
		if _, err := os.Stat(filepath.Join(dir, "VERSION")); err != nil {
			t.Errorf("VERSION not found in extracted dir: %v", err)
		}
	})
}
