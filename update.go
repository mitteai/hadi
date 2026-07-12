package main

// hadi update: replace this binary with the latest GitHub release.
// Laptop convenience only — CI pins a version on purpose, and no other
// command ever checks for updates (a deploy tool that phones home before
// deploying adds a failure mode for zero value).

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/mitteai/hadi/internal/ui"
)

const releaseAPI = "https://api.github.com/repos/mitteai/hadi/releases/latest"

type ghRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func cmdUpdate() {
	client := &http.Client{Timeout: 30 * time.Second}

	resp, err := client.Get(releaseAPI)
	if err != nil {
		ui.Fail("checking latest release: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		ui.Fail("checking latest release: GitHub answered %s (is the repo public?)", resp.Status)
	}
	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		ui.Fail("parsing release: %v", err)
	}

	latest := strings.TrimPrefix(rel.TagName, "v")
	current := strings.TrimPrefix(version, "v")
	if current == latest {
		ui.Say("hadi %s · already current", current)
		return
	}

	assetName := fmt.Sprintf("hadi-%s-%s", runtime.GOOS, runtime.GOARCH)
	binURL := assetURL(&rel, assetName)
	sumURL := assetURL(&rel, "sha256sums.txt")
	if binURL == "" {
		ui.Fail("release %s has no binary for %s/%s", rel.TagName, runtime.GOOS, runtime.GOARCH)
	}
	if sumURL == "" {
		ui.Fail("release %s has no sha256sums.txt; refusing to install unverified binaries", rel.TagName)
	}

	download := func(url string) []byte {
		r, err := client.Get(url)
		if err != nil {
			ui.Fail("download: %v", err)
		}
		defer r.Body.Close()
		raw, err := io.ReadAll(r.Body)
		if err != nil || r.StatusCode != 200 {
			ui.Fail("download %s: status %s, %v", url, r.Status, err)
		}
		return raw
	}

	sums := download(sumURL)
	bin := download(binURL)
	if err := verifySum(string(sums), assetName, bin); err != nil {
		ui.Fail("%v", err)
	}

	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		ui.Fail("locating current binary: %v", err)
	}
	tmp := exe + ".new"
	if err := os.WriteFile(tmp, bin, 0o755); err != nil {
		ui.Fail("writing %s: %v", tmp, err)
	}
	// Atomic on the same filesystem; the running process keeps its inode.
	if err := os.Rename(tmp, exe); err != nil {
		_ = os.Remove(tmp)
		ui.Fail("replacing %s: %v", exe, err)
	}
	ui.Say("hadi %s → %s · updated (%s)", current, latest, exe)
}

// assetURL finds a release asset's download URL by exact name.
func assetURL(rel *ghRelease, name string) string {
	for _, a := range rel.Assets {
		if a.Name == name {
			return a.URL
		}
	}
	return ""
}

// verifySum checks data against a "sha256sum" style sums file.
func verifySum(sums, name string, data []byte) error {
	got := hex.EncodeToString(func() []byte { h := sha256.Sum256(data); return h[:] }())
	for _, line := range strings.Split(sums, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == name {
			if fields[0] == got {
				return nil
			}
			return fmt.Errorf("checksum mismatch for %s: sums file says %s, downloaded %s", name, fields[0], got)
		}
	}
	return fmt.Errorf("%s not listed in sha256sums.txt", name)
}
