package handler

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestDirListingRendersEntries(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "sub")
	if err := os.Mkdir(subdir, 0o700); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	if err := os.Mkdir(filepath.Join(subdir, "docs"), 0o700); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	filePath := filepath.Join(subdir, "hello world.txt")
	if err := os.WriteFile(filePath, []byte("hi"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	mod := time.Date(2020, 1, 2, 3, 4, 0, 0, time.UTC)
	if err := os.Chtimes(filePath, mod, mod); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	info, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	expectedMod := info.ModTime().Format("Jan 02, 2006 15:04")

	h := newTestHandler(dir)
	req := httptest.NewRequest(http.MethodGet, "/sub/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("expected content-type text/html; charset=utf-8, got %q", got)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Index of /sub/") {
		t.Fatalf("expected title to include Index of /sub/")
	}
	if !strings.Contains(body, "href=\"/\">..</a>") {
		t.Fatalf("expected parent directory link")
	}
	if !strings.Contains(body, "href=\"/sub/docs/\">docs/</a>") {
		t.Fatalf("expected docs directory entry")
	}
	if !strings.Contains(body, "href=\"/sub/hello%20world.txt\">hello world.txt</a>") {
		t.Fatalf("expected encoded file href")
	}
	if !strings.Contains(body, "class=\"icon dir\"") {
		t.Fatalf("expected directory icon class")
	}
	if !strings.Contains(body, "class=\"icon file\"") {
		t.Fatalf("expected file icon class")
	}
	if !strings.Contains(body, expectedMod) {
		t.Fatalf("expected mod time formatted in listing")
	}
	if !strings.Contains(body, "2 B") {
		t.Fatalf("expected file size in listing")
	}
}

func TestDirListingRedirectsMissingSlash(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "sub")
	if err := os.Mkdir(subdir, 0o700); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}

	h := newTestHandler(dir)
	req := httptest.NewRequest(http.MethodGet, "/sub?foo=bar", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusMovedPermanently {
		t.Fatalf("expected 301, got %d", rr.Code)
	}
	if location := rr.Header().Get("Location"); location != "/sub/?foo=bar" {
		t.Fatalf("expected redirect to /sub/?foo=bar, got %q", location)
	}
}

func TestDirListingSortOrder(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "bdir"), 0o700); err != nil {
		t.Fatalf("mkdir bdir: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "adir"), 0o700); err != nil {
		t.Fatalf("mkdir adir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "B.txt"), []byte("b"), 0o600); err != nil {
		t.Fatalf("write B.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o600); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}

	h := newTestHandler(dir)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	body := rr.Body.String()

	adir := strings.Index(body, ">adir/</a>")
	bdir := strings.Index(body, ">bdir/</a>")
	aFile := strings.Index(body, ">a.txt</a>")
	bFile := strings.Index(body, ">B.txt</a>")

	if adir == -1 || bdir == -1 || aFile == -1 || bFile == -1 {
		t.Fatalf("expected all entries in listing")
	}
	if adir > bdir {
		t.Fatalf("expected adir/ before bdir/")
	}
	if bdir > aFile {
		t.Fatalf("expected directories before files")
	}
	if aFile > bFile {
		t.Fatalf("expected a.txt before B.txt")
	}
}

func TestDirListingUploadUIVisibility(t *testing.T) {
	dir := t.TempDir()
	h := newTestHandler(dir)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if strings.Contains(rr.Body.String(), "Drop files to upload") {
		t.Fatalf("did not expect upload UI when upload is disabled")
	}

	h.UploadEnabled = true
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	body := rr.Body.String()
	if !strings.Contains(body, "Drop files to upload") {
		t.Fatalf("expected upload UI when upload is enabled")
	}
	if !strings.Contains(body, "id=\"upload-zone\"") {
		t.Fatalf("expected upload zone element")
	}
}

func TestDirListingOneTimeUploadHidesEntriesAndShowsUploadUI(t *testing.T) {
	dir := t.TempDir()
	dropDir := filepath.Join(dir, "drop")
	if err := os.Mkdir(dropDir, 0o700); err != nil {
		t.Fatalf("mkdir drop: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dropDir, "hidden.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatalf("write hidden file: %v", err)
	}
	oneTimeUploadDirs, err := ResolveOneTimeUploadDirs(dir, []string{"drop"})
	if err != nil {
		t.Fatalf("resolve one-time upload dirs: %v", err)
	}

	h := newTestHandler(dir)
	h.OneTimeUploadDirs = oneTimeUploadDirs

	req := httptest.NewRequest(http.MethodGet, "/drop/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Drop files to upload") {
		t.Fatalf("expected upload UI")
	}
	if strings.Contains(body, "hidden.txt") {
		t.Fatalf("did not expect upload-only file in listing")
	}
	if strings.Contains(body, "<table>") {
		t.Fatalf("did not expect file listing table")
	}
}

func TestDirListingSnapshot(t *testing.T) {
	dir := t.TempDir()
	alphaDir := filepath.Join(dir, "alpha")
	if err := os.Mkdir(alphaDir, 0o700); err != nil {
		t.Fatalf("mkdir alpha: %v", err)
	}
	filePath := filepath.Join(dir, "z.txt")
	if err := os.WriteFile(filePath, []byte("hello"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	modDir := time.Date(2020, 1, 5, 11, 22, 0, 0, time.UTC)
	if err := os.Chtimes(alphaDir, modDir, modDir); err != nil {
		t.Fatalf("chtimes dir: %v", err)
	}
	modFile := time.Date(2021, 2, 10, 9, 8, 0, 0, time.UTC)
	if err := os.Chtimes(filePath, modFile, modFile); err != nil {
		t.Fatalf("chtimes file: %v", err)
	}

	h := newTestHandler(dir)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	got := normalizeListingHTML(rr.Body.String())
	snapshotPath := filepath.Join("testdata", "dir_listing.html")
	wantBytes, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	want := normalizeListingHTML(string(wantBytes))
	if got != want {
		t.Fatalf("snapshot mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		size int64
		want string
	}{
		{size: 0, want: "0 B"},
		{size: 12, want: "12 B"},
		{size: 1024, want: "1.0 KB"},
		{size: 10 * 1024, want: "10 KB"},
		{size: 1024 * 1024, want: "1.0 MB"},
		{size: 5 * 1024 * 1024, want: "5.0 MB"},
		{size: 1024 * 1024 * 1024, want: "1.0 GB"},
	}

	for _, tt := range tests {
		if got := formatBytes(tt.size); got != tt.want {
			t.Fatalf("formatBytes(%d) = %q, want %q", tt.size, got, tt.want)
		}
	}
}

func normalizeListingHTML(html string) string {
	html = strings.ReplaceAll(html, "\r\n", "\n")
	re := regexp.MustCompile(`Generated [^<]+`)
	html = re.ReplaceAllString(html, "Generated __GENERATED__")
	reMod := regexp.MustCompile(`(<td class="muted">)[A-Za-z]{3} [0-9]{2}, [0-9]{4} [0-9]{2}:[0-9]{2}(</td>)`)
	html = reMod.ReplaceAllString(html, "${1}__MODTIME__${2}")
	lines := strings.Split(html, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}
