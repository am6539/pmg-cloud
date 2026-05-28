package dashboard

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// githubRelease holds the fields we need from the GitHub releases API.
type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// githubPlatforms maps our internal os/arch to goreleaser asset names used by am6539/pmg.
var githubPlatforms = []struct {
	GOOS   string
	GOARCH string
	Asset  string
}{
	{"linux", "amd64", "pmg_Linux_x86_64.tar.gz"},
	{"linux", "arm64", "pmg_Linux_arm64.tar.gz"},
	{"darwin", "amd64", "pmg_Darwin_all.tar.gz"},
	{"darwin", "arm64", "pmg_Darwin_all.tar.gz"}, // universal binary
	{"windows", "amd64", "pmg_Windows_x86_64.zip"},
}

// FetchResult is the result of fetching binaries from a GitHub release.
type FetchResult struct {
	Version string       `json:"version"`
	Results []ScanResult `json:"results"`
}

// FetchFromGitHub downloads the latest release from the given GitHub repo
// (e.g. "am6539/pmg"), extracts the pmg binaries to the binaries dir,
// and registers their metadata. Returns the release tag and per-platform results.
func (us *UpdateStore) FetchFromGitHub(ctx context.Context, repo string) (FetchResult, error) {
	rel, err := fetchLatestRelease(ctx, repo)
	if err != nil {
		return FetchResult{}, fmt.Errorf("fetch release info: %w", err)
	}

	assetMap := make(map[string]githubAsset, len(rel.Assets))
	for _, a := range rel.Assets {
		assetMap[a.Name] = a
	}

	if err := os.MkdirAll(us.BinariesDir(), 0o755); err != nil {
		return FetchResult{}, fmt.Errorf("create binaries dir: %w", err)
	}

	// darwin universal binary only needs to be downloaded once.
	darwinCache := make(map[string][]byte)

	var results []ScanResult
	for _, p := range githubPlatforms {
		asset, ok := assetMap[p.Asset]
		if !ok {
			continue
		}

		dst := us.BinaryPath(p.GOOS, p.GOARCH)

		var archiveData []byte
		if cached, hit := darwinCache[p.Asset]; hit {
			archiveData = cached
		} else {
			archiveData, err = downloadArchive(ctx, asset.BrowserDownloadURL)
			if err != nil {
				continue
			}
			if p.GOOS == "darwin" {
				darwinCache[p.Asset] = archiveData
			}
		}

		binaryName := "pmg"
		if p.GOOS == "windows" {
			binaryName = "pmg.exe"
		}
		binData, err := extractBinary(archiveData, binaryName, strings.HasSuffix(p.Asset, ".zip"))
		if err != nil {
			continue
		}

		tmp := dst + ".tmp"
		if err := os.WriteFile(tmp, binData, 0o755); err != nil {
			continue
		}
		if err := os.Rename(tmp, dst); err != nil {
			os.Remove(tmp)
			continue
		}

		sum, size, err := hashFile(dst)
		if err != nil {
			continue
		}

		us.mu.Lock()
		existing, had := us.cfg.Binaries[p.GOOS+"/"+p.GOARCH]
		isNew := !had || existing.SHA256 != sum
		us.cfg.Binaries[p.GOOS+"/"+p.GOARCH] = BinaryMeta{
			SHA256:     sum,
			Size:       size,
			UploadedAt: time.Now().UTC(),
		}
		_ = us.save()
		us.mu.Unlock()

		results = append(results, ScanResult{
			Platform: p.GOOS + "/" + p.GOARCH,
			SHA256:   sum,
			Size:     size,
			New:      isNew,
		})
	}

	return FetchResult{Version: rel.TagName, Results: results}, nil
}

func fetchLatestRelease(ctx context.Context, repo string) (githubRelease, error) {
	url := "https://api.github.com/repos/" + repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return githubRelease{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return githubRelease{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return githubRelease{}, fmt.Errorf("github API returned %d", resp.StatusCode)
	}
	var rel githubRelease
	return rel, json.NewDecoder(resp.Body).Decode(&rel)
}

func downloadArchive(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download returned %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 100<<20))
}

func extractBinary(data []byte, name string, isZip bool) ([]byte, error) {
	if isZip {
		return extractFromZip(data, name)
	}
	return extractFromTarGz(data, name)
}

func extractFromTarGz(data []byte, name string) ([]byte, error) {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag == tar.TypeReg &&
			(hdr.Name == name || strings.HasSuffix(hdr.Name, "/"+name)) {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("%s not found in archive", name)
}

func extractFromZip(data []byte, name string) ([]byte, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	for _, f := range r.File {
		if f.Name == name || strings.HasSuffix(f.Name, "/"+name) {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("%s not found in zip", name)
}
