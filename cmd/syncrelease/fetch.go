package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type fetcher struct {
	dlJSON  string
	dlBase  string
	jsonCli *http.Client
	dlCli   *http.Client
}

func newFetcher() fetcher {
	return fetcher{
		dlJSON:  "https://go.dev/dl/?mode=json",
		dlBase:  "https://dl.google.com/go/",
		jsonCli: &http.Client{Timeout: 30 * time.Second},
		dlCli:   &http.Client{Timeout: 5 * time.Minute},
	}
}

// fetchRelease verifies the source archive before creating its extraction tree.
func fetchRelease(release string) (string, error) {
	return newFetcher().fetch(release)
}

func (f fetcher) fetch(release string) (string, error) {
	resp, err := f.jsonCli.Get(f.dlJSON)
	if err != nil {
		return "", fmt.Errorf("fetch dl JSON: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("dl JSON: %s", resp.Status)
	}
	meta, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read dl JSON: %v", err)
	}
	filename, wantSHA, err := dlSHA256(meta, release)
	if err != nil {
		return "", err
	}

	tarResp, err := f.dlCli.Get(f.dlBase + filename)
	if err != nil {
		return "", fmt.Errorf("download %s: %v", filename, err)
	}
	defer tarResp.Body.Close()
	if tarResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: %s", filename, tarResp.Status)
	}

	tmp, err := os.CreateTemp("", "syncrelease-dl-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), tarResp.Body); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return "", fmt.Errorf("download %s: %v", filename, err)
	}
	tmp.Close()

	got := hex.EncodeToString(h.Sum(nil))
	if got != wantSHA {
		os.Remove(tmpName)
		return "", fmt.Errorf("sha256 mismatch for %s: got %s, want %s", filename, got, wantSHA)
	}

	dir, err := os.MkdirTemp("", "syncrelease-goroot-*")
	if err != nil {
		os.Remove(tmpName)
		return "", err
	}

	fi, err := os.Open(tmpName)
	if err != nil {
		os.Remove(tmpName)
		os.RemoveAll(dir)
		return "", err
	}
	extractErr := hardenedExtract(fi, dir)
	fi.Close()
	os.Remove(tmpName)
	if extractErr != nil {
		os.RemoveAll(dir)
		return "", fmt.Errorf("extract %s: %v", filename, extractErr)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	if len(entries) == 1 && entries[0].IsDir() {
		return filepath.Join(dir, entries[0].Name()), nil
	}
	return dir, nil
}

// hardenedExtract permits only regular files and directories confined within
// dir. It rejects absolute paths, any ".." element, links, and special files.
func hardenedExtract(r io.Reader, dir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip: %v", err)
	}
	defer gz.Close()

	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	prefix := absDir + string(filepath.Separator)

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar: %v", err)
		}

		switch hdr.Typeflag {
		case tar.TypeReg, tar.TypeRegA, tar.TypeDir:
		default:
			return fmt.Errorf("rejected entry %q: unsupported type %d", hdr.Name, hdr.Typeflag)
		}

		if filepath.IsAbs(hdr.Name) {
			return fmt.Errorf("rejected entry %q: absolute path", hdr.Name)
		}

		// Check the raw path because filepath.Clean removes interior ".." elements.
		for _, elem := range strings.Split(hdr.Name, "/") {
			if elem == ".." {
				return fmt.Errorf("rejected entry %q: contains .. element", hdr.Name)
			}
		}

		clean := filepath.Clean(hdr.Name)
		target := filepath.Join(absDir, clean)
		absTarget, err := filepath.Abs(target)
		if err != nil {
			return err
		}
		if absTarget != absDir && !strings.HasPrefix(absTarget+string(filepath.Separator), prefix) {
			return fmt.Errorf("rejected entry %q: escapes extraction root", hdr.Name)
		}

		if hdr.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(absTarget, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(absTarget), 0o755); err != nil {
			return err
		}
		f, err := os.Create(absTarget)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(f, tr)
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
