package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	json "encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		v1   string
		v2   string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.1.0", "1.0.0", 1},
		{"1.0.0", "1.1.0", -1},
		{"2.0.0", "1.9.9", 1},
		{"1.0.1", "1.0.0", 1},
		{"v1.2.3", "1.2.3", 0},
		{"v1.2.4", "v1.2.3", 1},
		{"v1.8.6-rc1", "1.8.5", 1},
		{"1.8.5", "1.8.6", -1},
	}

	for _, tt := range tests {
		got := CompareVersions(tt.v1, tt.v2)
		if got != tt.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tt.v1, tt.v2, got, tt.want)
		}
	}
}

func TestGetCachedInfo(t *testing.T) {
	info := GetCachedInfo()
	if info == nil {
		t.Fatal("expected non-nil UpdateInfo")
	}
	if info.CurrentVersion == "" {
		t.Error("expected non-empty CurrentVersion")
	}
}

// A restart must not re-resolve its own executable after the swap. On Linux
// os.Executable() then reports the path the replaced inode had, which has since
// been renamed to .old and deleted, so resolution fails and the restart is
// abandoned while the freshly installed binary sits unused on disk. This is the
// regression from a container whose auto-update logged "resolve symlink for
// restart failed error=lstat /usr/local/bin/9router-go.old: no such file or
// directory" and kept serving the old version.
func TestExecutableTargetPrefersTheInstalledPath(t *testing.T) {
	installedPathMu.Lock()
	before := installedPath
	installedPath = ""
	installedPathMu.Unlock()
	t.Cleanup(func() { rememberInstalledPath(before) })

	fallback, err := executableTarget()
	if err != nil {
		t.Fatalf("executableTarget before any update: %v", err)
	}
	if _, err := os.Stat(fallback); err != nil {
		t.Fatalf("the path before any update must exist: %v", err)
	}

	rememberInstalledPath("/usr/local/bin/9router-go")
	got, err := executableTarget()
	if err != nil {
		t.Fatalf("executableTarget after a swap: %v", err)
	}
	if got != "/usr/local/bin/9router-go" {
		t.Fatalf("executableTarget after a swap = %q, want the installed path", got)
	}
}

func TestCheckUpdate_Manifest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		manifest := map[string]any{
			"latestVersion": "2.0.0",
			"downloadUrl":   "https://github.com/wicahma/9router-go/releases/download/v2.0.0/9router-go.tar.gz",
			"releaseNotes":  "Major release 2.0.0",
			"sha256":        "abcdef123456",
		}
		w.Header().Set("Content-Type", "application/json")
		json.MarshalWrite(w, manifest)
	}))
	defer server.Close()

	os.Setenv("UPDATE_URL", server.URL)
	defer os.Unsetenv("UPDATE_URL")

	info, err := CheckUpdate(context.Background())
	if err != nil {
		t.Fatalf("CheckUpdate failed: %v", err)
	}

	if !info.HasUpdate {
		t.Errorf("expected hasUpdate=true for version 2.0.0 vs %s", CurrentVersion)
	}
	if info.LatestVersion != "2.0.0" {
		t.Errorf("expected latestVersion 2.0.0, got %s", info.LatestVersion)
	}
	if info.DownloadURL != "https://github.com/wicahma/9router-go/releases/download/v2.0.0/9router-go.tar.gz" {
		t.Errorf("expected downloadUrl, got %s", info.DownloadURL)
	}
}

func TestCheckUpdate_GitHubReleasesFallback(t *testing.T) {
	// Mock failing manifest endpoint
	manifestServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer manifestServer.Close()

	os.Setenv("UPDATE_URL", manifestServer.URL)
	defer os.Unsetenv("UPDATE_URL")

	// Directly test checkGitHubReleases
	ghServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"tag_name": "v3.0.0",
			"name":     "Release 3.0.0",
			"body":     "Awesome new features",
			"assets": []map[string]any{
				{
					"name":                 "9router-go_darwin_arm64.tar.gz",
					"browser_download_url": "https://github.com/releases/9router-go_darwin_arm64.tar.gz",
				},
				{
					"name":                 "9router-go_linux_amd64.tar.gz",
					"browser_download_url": "https://github.com/releases/9router-go_linux_amd64.tar.gz",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.MarshalWrite(w, resp)
	}))
	defer ghServer.Close()

	info, err := checkGitHubReleases(context.Background(), ghServer.URL)
	if err != nil {
		t.Fatalf("checkGitHubReleases failed: %v", err)
	}

	if info.LatestVersion != "3.0.0" {
		t.Errorf("expected latestVersion 3.0.0, got %s", info.LatestVersion)
	}
	if !info.HasUpdate {
		t.Errorf("expected hasUpdate=true")
	}
	if info.Source != "github_releases" {
		t.Errorf("expected source github_releases, got %s", info.Source)
	}
}

func TestExtractExecutableBytes_TarGz(t *testing.T) {
	// Create a dummy .tar.gz containing a 9router-go binary payload
	binaryContent := bytes.Repeat([]byte("BINARY_PAYLOAD_CONTENT_TEST_EXEC_DATA"), 100)

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	hdr := &tar.Header{
		Name: "9router-go",
		Mode: 0755,
		Size: int64(len(binaryContent)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := tw.Write(binaryContent); err != nil {
		t.Fatalf("write tar content: %v", err)
	}
	tw.Close()
	gw.Close()

	extracted, err := extractExecutableBytes(buf.Bytes(), "9router-go_darwin_arm64.tar.gz")
	if err != nil {
		t.Fatalf("extractExecutableBytes failed: %v", err)
	}

	if !bytes.Equal(extracted, binaryContent) {
		t.Errorf("extracted content does not match expected payload")
	}
}

func TestExtractExecutableBytes_Zip(t *testing.T) {
	binaryContent := bytes.Repeat([]byte("ZIP_BINARY_PAYLOAD_TEST_DATA"), 100)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	f, err := zw.Create("9router-go.exe")
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	if _, err := f.Write(binaryContent); err != nil {
		t.Fatalf("write zip content: %v", err)
	}
	zw.Close()

	extracted, err := extractExecutableBytes(buf.Bytes(), "9router-go_windows_amd64.zip")
	if err != nil {
		t.Fatalf("extract zip failed: %v", err)
	}

	if !bytes.Equal(extracted, binaryContent) {
		t.Errorf("extracted zip content does not match payload")
	}
}

func TestMatchReleaseAsset(t *testing.T) {
	assets := []releaseAsset{
		{Name: "9router-go_linux_amd64.tar.gz", BrowserDownloadURL: "url-linux-amd64"},
		{Name: "9router-go_darwin_arm64.tar.gz", BrowserDownloadURL: "url-darwin-arm64"},
		{Name: "9router-go_windows_amd64.zip", BrowserDownloadURL: "url-windows-amd64"},
		{Name: "checksums.txt", BrowserDownloadURL: "url-checksums"},
	}

	if url := matchReleaseAsset(assets, "darwin", "arm64"); url != "url-darwin-arm64" {
		t.Errorf("expected url-darwin-arm64, got %s", url)
	}
	if url := matchReleaseAsset(assets, "linux", "amd64"); url != "url-linux-amd64" {
		t.Errorf("expected url-linux-amd64, got %s", url)
	}
	if url := matchReleaseAsset(assets, "windows", "amd64"); url != "url-windows-amd64" {
		t.Errorf("expected url-windows-amd64, got %s", url)
	}
}

func TestIsDirectBinaryURL(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"https://github.com/wicahma/9router-go/releases/download/v1.9.5/9router-go-linux-arm64", true},
		{"https://example.com/9router-go-windows-amd64.exe", true},
		{"https://example.com/bundle.tar.gz", true},
		{"https://mirror.example.com/downloads/9router-go", true},
		{"https://github.com/wicahma/9router-go/releases/latest", false},
		{"https://github.com/wicahma/9router-go/releases", false},
		{"https://github.com/wicahma/9router-go/releases/tag/v1.9.5", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isDirectBinaryURL(tc.url); got != tc.want {
			t.Errorf("isDirectBinaryURL(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
}

// The manifest once shipped the /releases/latest page as its downloadUrl.
// Downloading that page yields HTML, which the old updater wrote over the
// running binary — the update "succeeded" but nothing changed and the install
// was bricked. The check must repair the URL from the GitHub Releases API
// before anything is fetched.
func TestCheckUpdate_RepairsNonAssetManifestURL(t *testing.T) {
	assetURL := ""
	var gh *httptest.Server
	gh = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/wicahma/9router-go/releases/latest":
			assetURL = gh.URL + "/downloads/v2.0.0/9router-go-" + runtime.GOOS + "-" + runtime.GOARCH
			resp := map[string]any{
				"tag_name": "v2.0.0",
				"assets": []map[string]any{
					{"name": assetNameFromURL(assetURL), "browser_download_url": assetURL},
					{"name": "SHA256SUMS.txt", "browser_download_url": gh.URL + "/downloads/v2.0.0/SHA256SUMS.txt"},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.MarshalWrite(w, resp)
		case "/downloads/v2.0.0/SHA256SUMS.txt":
			sum := strings.Repeat("ab", 32)
			fmt.Fprintf(w, "%s  %s\n", sum, assetNameFromURL(assetURL))
		default:
			http.NotFound(w, r)
		}
	}))
	defer gh.Close()

	manifest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"latestVersion": "2.0.0",
			"downloadUrl":   "https://github.com/wicahma/9router-go/releases/latest",
		}
		w.Header().Set("Content-Type", "application/json")
		json.MarshalWrite(w, resp)
	}))
	defer manifest.Close()

	os.Setenv("UPDATE_URL", manifest.URL)
	defer os.Unsetenv("UPDATE_URL")
	oldBase := githubReleasesAPIBase
	githubReleasesAPIBase = gh.URL
	t.Cleanup(func() { githubReleasesAPIBase = oldBase })

	info, err := CheckUpdate(context.Background())
	if err != nil {
		t.Fatalf("CheckUpdate failed: %v", err)
	}
	if !strings.HasSuffix(info.DownloadURL, "/9router-go-"+runtime.GOOS+"-"+runtime.GOARCH) {
		t.Errorf("expected the repaired release asset URL, got %q", info.DownloadURL)
	}
	if info.SHA256 != strings.Repeat("ab", 32) {
		t.Errorf("expected the SHA256SUMS.txt checksum for the asset, got %q", info.SHA256)
	}
}

// PerformSelfUpdate must refuse to replace the running binary with a payload
// that cannot execute on this platform (an HTML page, a wrong-OS asset).
func TestPerformSelfUpdate_RefusesNonExecutable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<!DOCTYPE html><html><body>releases</body></html>"))
	}))
	defer srv.Close()

	err := PerformSelfUpdate(srv.URL, "")
	if err == nil {
		t.Fatal("expected PerformSelfUpdate to reject a non-executable payload")
	}
	if !strings.Contains(err.Error(), "not an executable") {
		t.Fatalf("expected a not-an-executable error, got %v", err)
	}
}

func TestAutoUpdate_StatusAndToggle(t *testing.T) {
	SetAutoUpdate(true)
	if !IsAutoUpdateEnabled() {
		t.Errorf("expected autoUpdate to be true")
	}

	status := GetStatus()
	if !status.AutoUpdateEnabled {
		t.Errorf("expected status.AutoUpdateEnabled to be true")
	}
	if status.CurrentVersion != CurrentVersion {
		t.Errorf("expected currentVersion %s, got %s", CurrentVersion, status.CurrentVersion)
	}

	SetAutoUpdate(false)
	if IsAutoUpdateEnabled() {
		t.Errorf("expected autoUpdate to be false")
	}
}
