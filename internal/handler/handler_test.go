package handler

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"serv/internal/security"
)

func newTestHandler(dir string) *Handler {
	return &Handler{
		Dir:    dir,
		Logger: log.New(io.Discard, "", 0),
	}
}

type uploadFixture struct {
	Name    string
	Content string
}

func newUploadRequest(t *testing.T, target string, files []uploadFixture) *http.Request {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, file := range files {
		part, err := writer.CreateFormFile("files", file.Name)
		if err != nil {
			t.Fatalf("create form file: %v", err)
		}
		if _, err := io.WriteString(part, file.Content); err != nil {
			t.Fatalf("write form file: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, target, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func newRawUploadRequest(target string, body string) *http.Request {
	return httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
}

func parseUploadResponse(t *testing.T, rr *httptest.ResponseRecorder) uploadResponse {
	t.Helper()
	var payload uploadResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("parse upload response: %v body=%q", err, rr.Body.String())
	}
	return payload
}

func sha256Hex(content string) string {
	sum := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x", sum)
}

func TestHandlerServesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	h := newTestHandler(dir)
	req := httptest.NewRequest(http.MethodGet, "/hello.txt", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if body := rr.Body.String(); body != "hello" {
		t.Fatalf("unexpected body: %q", body)
	}
}

func TestHandlerDeletesOneTimeDownloadAfterSuccessfulGet(t *testing.T) {
	dir := t.TempDir()
	otdDir := filepath.Join(dir, "otd")
	if err := os.Mkdir(otdDir, 0o700); err != nil {
		t.Fatalf("mkdir otd: %v", err)
	}
	path := filepath.Join(otdDir, "once.txt")
	if err := os.WriteFile(path, []byte("once"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	otdDirs, err := ResolveOneTimeDownloadDirs(dir, []string{"otd"})
	if err != nil {
		t.Fatalf("resolve one-time dirs: %v", err)
	}

	h := newTestHandler(dir)
	h.OneTimeDownloadDirs = otdDirs

	req := httptest.NewRequest(http.MethodGet, "/otd/once.txt", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if body := rr.Body.String(); body != "once" {
		t.Fatalf("unexpected body: %q", body)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected one-time file to be deleted, stat err=%v", err)
	}

	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after one-time download, got %d", rr.Code)
	}
}

func TestHandlerOneTimeDownloadIgnoresHeadAndRange(t *testing.T) {
	dir := t.TempDir()
	otdDir := filepath.Join(dir, "otd")
	if err := os.Mkdir(otdDir, 0o700); err != nil {
		t.Fatalf("mkdir otd: %v", err)
	}
	path := filepath.Join(otdDir, "once.txt")
	if err := os.WriteFile(path, []byte("once"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	otdDirs, err := ResolveOneTimeDownloadDirs(dir, []string{"otd"})
	if err != nil {
		t.Fatalf("resolve one-time dirs: %v", err)
	}

	h := newTestHandler(dir)
	h.OneTimeDownloadDirs = otdDirs

	req := httptest.NewRequest(http.MethodHead, "/otd/once.txt", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected HEAD 200, got %d", rr.Code)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file to remain after HEAD: %v", err)
	}

	req = httptest.NewRequest(http.MethodGet, "/otd/once.txt", nil)
	req.Header.Set("Range", "bytes=0-1")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusPartialContent {
		t.Fatalf("expected range 206, got %d", rr.Code)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file to remain after partial content: %v", err)
	}
}

func TestResolveOneTimeDownloadDirsRejectsOutsideRoot(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "served")
	outside := filepath.Join(parent, "outside")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("mkdir served: %v", err)
	}
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}

	if _, err := ResolveOneTimeDownloadDirs(dir, []string{outside}); err == nil {
		t.Fatalf("expected absolute outside directory to be rejected")
	}
	if _, err := ResolveOneTimeDownloadDirs(dir, []string{"../outside"}); err == nil {
		t.Fatalf("expected relative outside directory to be rejected")
	}
}

func TestValidateOneTimeDirSeparationRejectsOverlaps(t *testing.T) {
	root := t.TempDir()
	downloads := []string{filepath.Join(root, "once")}
	uploads := []string{filepath.Join(root, "once")}
	if err := ValidateOneTimeDirSeparation(downloads, uploads); err == nil {
		t.Fatalf("expected same directory overlap to be rejected")
	}

	uploads = []string{filepath.Join(root, "once", "drop")}
	if err := ValidateOneTimeDirSeparation(downloads, uploads); err == nil {
		t.Fatalf("expected nested upload directory overlap to be rejected")
	}

	uploads = []string{filepath.Join(root, "drop")}
	if err := ValidateOneTimeDirSeparation(downloads, uploads); err != nil {
		t.Fatalf("expected separate directories to be allowed: %v", err)
	}
}

func TestHandlerRequiresAuth(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	h := newTestHandler(dir)
	h.Username = "user"
	h.Password = "pass"

	req := httptest.NewRequest(http.MethodGet, "/hello.txt", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	if rr.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("expected WWW-Authenticate header")
	}
}

func TestHandlerHtaccessOverridesCredentials(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	content := "username: ht\npassword: pass\n"
	if err := os.WriteFile(filepath.Join(dir, ".htaccess"), []byte(content), 0o600); err != nil {
		t.Fatalf("write htaccess: %v", err)
	}

	h := newTestHandler(dir)
	h.Username = "cli"
	h.Password = "cli"

	req := httptest.NewRequest(http.MethodGet, "/hello.txt", nil)
	req.SetBasicAuth("ht", "pass")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestHandlerDotfileAccess(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".hidden"), []byte("secret"), 0o600); err != nil {
		t.Fatalf("write dotfile: %v", err)
	}

	h := newTestHandler(dir)
	req := httptest.NewRequest(http.MethodGet, "/.hidden", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}

	h.AllowDotFiles = true
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestHandlerSensitivePathNotFound(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "secret.pem"), []byte("secret"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	sensitive, err := security.ResolveSensitiveFiles([]string{filepath.Join(dir, "secret.pem")})
	if err != nil {
		t.Fatalf("resolve sensitive files: %v", err)
	}

	h := newTestHandler(dir)
	h.Sensitive = sensitive
	req := httptest.NewRequest(http.MethodGet, "/secret.pem", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestHandlerHtaccessNotFound(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".htaccess"), []byte("username: x\npassword: y\n"), 0o600); err != nil {
		t.Fatalf("write htaccess: %v", err)
	}
	h := newTestHandler(dir)
	req := httptest.NewRequest(http.MethodGet, "/.htaccess", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestHandlerAllowsTLSLikeFilesWhenNotSensitive(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "server.key"), []byte("secret"), 0o600); err != nil {
		t.Fatalf("write tls file: %v", err)
	}

	h := newTestHandler(dir)
	req := httptest.NewRequest(http.MethodGet, "/server.key", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if body := rr.Body.String(); body != "secret" {
		t.Fatalf("unexpected body: %q", body)
	}
}

func TestHandlerFilterGlobsBlockAccess(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.log"), []byte("log"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	h := newTestHandler(dir)
	h.FilterGlobs = []string{"*.log"}
	req := httptest.NewRequest(http.MethodGet, "/app.log", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestHandlerListingFiltersEntries(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".htaccess"), []byte("username: x\npassword: y\n"), 0o600); err != nil {
		t.Fatalf("write htaccess: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".hidden"), []byte("secret"), 0o600); err != nil {
		t.Fatalf("write dotfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "server.key"), []byte("secret"), 0o600); err != nil {
		t.Fatalf("write tls file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "visible.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatalf("write visible file: %v", err)
	}
	sensitive, err := security.ResolveSensitiveFiles([]string{filepath.Join(dir, "server.key")})
	if err != nil {
		t.Fatalf("resolve sensitive files: %v", err)
	}

	h := newTestHandler(dir)
	h.Sensitive = sensitive
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("x", "y")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "visible.txt") {
		t.Fatalf("expected visible.txt to appear in listing")
	}
	if strings.Contains(body, ".htaccess") {
		t.Fatalf("did not expect .htaccess in listing")
	}
	if strings.Contains(body, ".hidden") {
		t.Fatalf("did not expect .hidden in listing")
	}
	if strings.Contains(body, "server.key") {
		t.Fatalf("did not expect server.key in listing")
	}
}

func TestHandlerUploadMultipleFiles(t *testing.T) {
	dir := t.TempDir()
	h := newTestHandler(dir)
	h.UploadEnabled = true
	h.UploadMaxBytes = 1024 * 1024

	req := newUploadRequest(t, "/", []uploadFixture{
		{Name: "first.txt", Content: "one"},
		{Name: "second.txt", Content: "two"},
	})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%q", rr.Code, rr.Body.String())
	}

	payload := parseUploadResponse(t, rr)
	if payload.Uploaded != 2 || payload.Failed != 0 {
		t.Fatalf("unexpected upload summary: %+v", payload)
	}

	for _, file := range []uploadFixture{{Name: "first.txt", Content: "one"}, {Name: "second.txt", Content: "two"}} {
		data, err := os.ReadFile(filepath.Join(dir, file.Name))
		if err != nil {
			t.Fatalf("read uploaded file %s: %v", file.Name, err)
		}
		if string(data) != file.Content {
			t.Fatalf("uploaded file %s content = %q, want %q", file.Name, string(data), file.Content)
		}
	}
}

func TestHandlerUploadSerializesSameDestination(t *testing.T) {
	dir := t.TempDir()
	h := newTestHandler(dir)
	h.UploadOverwrite = true

	firstReader, firstWriter := io.Pipe()
	firstStarted := make(chan struct{})
	firstDone := make(chan uploadFileResult, 1)
	var closeOnce sync.Once
	closeFirst := func() {
		closeOnce.Do(func() {
			_ = firstWriter.Close()
		})
	}
	defer closeFirst()

	go func() {
		close(firstStarted)
		firstDone <- h.storeUploadedContent(uploadTarget{
			TargetDir: dir,
			FileName:  "same.txt",
		}, "same.txt", firstReader)
	}()

	<-firstStarted
	second := h.storeUploadedContent(uploadTarget{
		TargetDir: dir,
		FileName:  "same.txt",
	}, "same.txt", strings.NewReader("second"))

	if second.Status != "error" || second.Message != "upload failed" {
		t.Fatalf("expected concurrent upload to fail generically, got %+v", second)
	}

	if _, err := firstWriter.Write([]byte("first")); err != nil {
		t.Fatalf("write first upload: %v", err)
	}
	closeFirst()

	first := <-firstDone
	if first.Status != "uploaded" {
		t.Fatalf("expected first upload to succeed, got %+v", first)
	}
	data, err := os.ReadFile(filepath.Join(dir, "same.txt"))
	if err != nil {
		t.Fatalf("read uploaded file: %v", err)
	}
	if string(data) != "first" {
		t.Fatalf("expected first upload content, got %q", string(data))
	}
}

func TestHandlerOneTimeUploadAllowsUploadWithoutDownload(t *testing.T) {
	dir := t.TempDir()
	dropDir := filepath.Join(dir, "drop")
	if err := os.Mkdir(dropDir, 0o700); err != nil {
		t.Fatalf("mkdir drop: %v", err)
	}
	oneTimeUploadDirs, err := ResolveOneTimeUploadDirs(dir, []string{"drop"})
	if err != nil {
		t.Fatalf("resolve one-time upload dirs: %v", err)
	}

	h := newTestHandler(dir)
	h.OneTimeUploadDirs = oneTimeUploadDirs

	content := "payload"
	expectedName := "new_" + sha256Hex(content) + ".txt"
	req := newRawUploadRequest("/drop/new.txt", content)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%q", rr.Code, rr.Body.String())
	}
	payload := parseUploadResponse(t, rr)
	if payload.Uploaded != 1 || payload.Failed != 0 {
		t.Fatalf("unexpected upload summary: %+v", payload)
	}
	if len(payload.Files) != 1 || payload.Files[0].Name != expectedName || payload.Files[0].Status != "uploaded" {
		t.Fatalf("unexpected upload file result: %+v", payload.Files)
	}
	if data, err := os.ReadFile(filepath.Join(dropDir, expectedName)); err != nil || string(data) != content {
		t.Fatalf("expected uploaded file content, err=%v data=%q", err, string(data))
	}
	if _, err := os.Stat(filepath.Join(dropDir, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected original upload name to be absent, stat err=%v", err)
	}

	req = httptest.NewRequest(http.MethodGet, "/drop/"+expectedName, nil)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected uploaded file download to be blocked with 404, got %d", rr.Code)
	}
}

func TestHandlerOneTimeUploadUsesHashNameWithoutExtension(t *testing.T) {
	dir := t.TempDir()
	dropDir := filepath.Join(dir, "drop")
	if err := os.Mkdir(dropDir, 0o700); err != nil {
		t.Fatalf("mkdir drop: %v", err)
	}
	oneTimeUploadDirs, err := ResolveOneTimeUploadDirs(dir, []string{"drop"})
	if err != nil {
		t.Fatalf("resolve one-time upload dirs: %v", err)
	}

	h := newTestHandler(dir)
	h.OneTimeUploadDirs = oneTimeUploadDirs

	content := "payload without extension"
	expectedName := "name_" + sha256Hex(content)
	req := newRawUploadRequest("/drop/name", content)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%q", rr.Code, rr.Body.String())
	}
	payload := parseUploadResponse(t, rr)
	if payload.Uploaded != 1 || payload.Failed != 0 || payload.Files[0].Name != expectedName {
		t.Fatalf("unexpected upload response: %+v", payload)
	}
	if data, err := os.ReadFile(filepath.Join(dropDir, expectedName)); err != nil || string(data) != content {
		t.Fatalf("expected uploaded file content, err=%v data=%q", err, string(data))
	}
}

func TestHandlerOneTimeUploadAllowsSameOriginalNameWithDifferentHash(t *testing.T) {
	dir := t.TempDir()
	dropDir := filepath.Join(dir, "drop")
	if err := os.Mkdir(dropDir, 0o700); err != nil {
		t.Fatalf("mkdir drop: %v", err)
	}
	existing := filepath.Join(dropDir, "same.txt")
	if err := os.WriteFile(existing, []byte("original"), 0o600); err != nil {
		t.Fatalf("write existing file: %v", err)
	}
	oneTimeUploadDirs, err := ResolveOneTimeUploadDirs(dir, []string{"drop"})
	if err != nil {
		t.Fatalf("resolve one-time upload dirs: %v", err)
	}

	h := newTestHandler(dir)
	h.UploadOverwrite = true
	h.OneTimeUploadDirs = oneTimeUploadDirs

	content := "replacement"
	expectedName := "same_" + sha256Hex(content) + ".txt"
	req := newRawUploadRequest("/drop/same.txt", content)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%q", rr.Code, rr.Body.String())
	}
	payload := parseUploadResponse(t, rr)
	if payload.Uploaded != 1 || payload.Failed != 0 || payload.Files[0].Name != expectedName {
		t.Fatalf("unexpected upload response: %+v", payload)
	}
	data, err := os.ReadFile(existing)
	if err != nil {
		t.Fatalf("read existing file: %v", err)
	}
	if string(data) != "original" {
		t.Fatalf("expected existing file to remain unchanged, got %q", string(data))
	}
	if data, err := os.ReadFile(filepath.Join(dropDir, expectedName)); err != nil || string(data) != content {
		t.Fatalf("expected hashed upload content, err=%v data=%q", err, string(data))
	}
}

func TestHandlerOneTimeUploadRejectsDuplicateSHA256(t *testing.T) {
	dir := t.TempDir()
	dropDir := filepath.Join(dir, "drop")
	if err := os.Mkdir(dropDir, 0o700); err != nil {
		t.Fatalf("mkdir drop: %v", err)
	}
	oneTimeUploadDirs, err := ResolveOneTimeUploadDirs(dir, []string{"drop"})
	if err != nil {
		t.Fatalf("resolve one-time upload dirs: %v", err)
	}

	h := newTestHandler(dir)
	h.OneTimeUploadDirs = oneTimeUploadDirs

	content := "duplicate payload"
	firstName := "first_" + sha256Hex(content) + ".jpg"
	req := newRawUploadRequest("/drop/first.jpg", content)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected first upload 201, got %d body=%q", rr.Code, rr.Body.String())
	}

	req = newRawUploadRequest("/drop/second.png", content)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected duplicate upload 400, got %d body=%q", rr.Code, rr.Body.String())
	}
	payload := parseUploadResponse(t, rr)
	if payload.Uploaded != 0 || payload.Failed != 1 || payload.Files[0].Message != "upload failed" {
		t.Fatalf("unexpected duplicate response: %+v", payload)
	}
	if data, err := os.ReadFile(filepath.Join(dropDir, firstName)); err != nil || string(data) != content {
		t.Fatalf("expected first upload content, err=%v data=%q", err, string(data))
	}
	if _, err := os.Stat(filepath.Join(dropDir, "second_"+sha256Hex(content)+".png")); !os.IsNotExist(err) {
		t.Fatalf("expected duplicate upload file to be absent, stat err=%v", err)
	}
}

func TestHandlerOneTimeUploadRespectsMaxBytes(t *testing.T) {
	dir := t.TempDir()
	dropDir := filepath.Join(dir, "drop")
	if err := os.Mkdir(dropDir, 0o700); err != nil {
		t.Fatalf("mkdir drop: %v", err)
	}
	oneTimeUploadDirs, err := ResolveOneTimeUploadDirs(dir, []string{"drop"})
	if err != nil {
		t.Fatalf("resolve one-time upload dirs: %v", err)
	}

	h := newTestHandler(dir)
	h.OneTimeUploadDirs = oneTimeUploadDirs
	h.UploadMaxBytes = 4

	req := newRawUploadRequest("/drop/too-large.txt", "larger than four bytes")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d body=%q", rr.Code, rr.Body.String())
	}
	payload := parseUploadResponse(t, rr)
	if payload.Uploaded != 0 || payload.Failed != 1 || payload.Files[0].Message != "request entity too large" {
		t.Fatalf("unexpected max bytes response: %+v", payload)
	}
	if _, err := os.Stat(filepath.Join(dropDir, "too-large.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected oversized upload to leave no file, stat err=%v", err)
	}
}

func TestHandlerUploadSingleFileFromURLPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "uploads"), 0o700); err != nil {
		t.Fatalf("mkdir uploads: %v", err)
	}
	h := newTestHandler(dir)
	h.UploadEnabled = true
	h.UploadMaxBytes = 1024 * 1024

	req := newRawUploadRequest("/uploads/from-url.txt", "one")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%q", rr.Code, rr.Body.String())
	}

	payload := parseUploadResponse(t, rr)
	if payload.Uploaded != 1 || payload.Failed != 0 {
		t.Fatalf("unexpected upload summary: %+v", payload)
	}
	if len(payload.Files) != 1 || payload.Files[0].Name != "from-url.txt" || payload.Files[0].Status != "uploaded" {
		t.Fatalf("unexpected upload file result: %+v", payload.Files)
	}

	data, err := os.ReadFile(filepath.Join(dir, "uploads", "from-url.txt"))
	if err != nil {
		t.Fatalf("read uploaded file: %v", err)
	}
	if string(data) != "one" {
		t.Fatalf("uploaded file content = %q, want %q", string(data), "one")
	}
}

func TestHandlerUploadSingleFileFromURLPathMissingParent(t *testing.T) {
	dir := t.TempDir()
	h := newTestHandler(dir)
	h.UploadEnabled = true

	req := newRawUploadRequest("/uploads/missing.txt", "one")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%q", rr.Code, rr.Body.String())
	}
}

func TestHandlerUploadRespectsFilterAndHtaccessBlock(t *testing.T) {
	dir := t.TempDir()
	h := newTestHandler(dir)
	h.UploadEnabled = true
	h.FilterGlobs = []string{"*.log"}

	req := newUploadRequest(t, "/", []uploadFixture{
		{Name: "visible.log", Content: "blocked"},
		{Name: ".htaccess", Content: "username: a\npassword: b\n"},
		{Name: "ok.txt", Content: "ok"},
	})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusMultiStatus {
		t.Fatalf("expected 207, got %d body=%q", rr.Code, rr.Body.String())
	}

	payload := parseUploadResponse(t, rr)
	if payload.Uploaded != 1 || payload.Failed != 2 {
		t.Fatalf("unexpected upload summary: %+v", payload)
	}
	if _, err := os.Stat(filepath.Join(dir, "visible.log")); !os.IsNotExist(err) {
		t.Fatalf("expected filtered file to be blocked, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".htaccess")); !os.IsNotExist(err) {
		t.Fatalf("expected .htaccess upload to be blocked, stat err=%v", err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "ok.txt")); err != nil || string(data) != "ok" {
		t.Fatalf("expected ok.txt upload success, err=%v data=%q", err, string(data))
	}
}

func TestHandlerUploadRequiresAuth(t *testing.T) {
	dir := t.TempDir()
	h := newTestHandler(dir)
	h.UploadEnabled = true
	h.Username = "user"
	h.Password = "pass"

	req := newUploadRequest(t, "/", []uploadFixture{{Name: "first.txt", Content: "one"}})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestHandlerUploadDisabled(t *testing.T) {
	dir := t.TempDir()
	h := newTestHandler(dir)

	req := newUploadRequest(t, "/", []uploadFixture{{Name: "first.txt", Content: "one"}})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rr.Code)
	}
}

func TestHandlerUploadMaxBytesLimit(t *testing.T) {
	dir := t.TempDir()
	h := newTestHandler(dir)
	h.UploadEnabled = true
	h.UploadMaxBytes = 8

	req := newUploadRequest(t, "/", []uploadFixture{{Name: "big.txt", Content: "this is larger than eight bytes"}})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d body=%q", rr.Code, rr.Body.String())
	}
}

func TestHandlerUploadUnlimitedWhenMaxBytesZero(t *testing.T) {
	dir := t.TempDir()
	h := newTestHandler(dir)
	h.UploadEnabled = true
	h.UploadMaxBytes = 0

	req := newUploadRequest(t, "/", []uploadFixture{{Name: "big.txt", Content: strings.Repeat("x", 2048)}})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%q", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "big.txt")); err != nil {
		t.Fatalf("expected uploaded file to exist: %v", err)
	}
}

func TestHandlerUploadRootAllowedWhenServeDirHasDotPathSegment(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, ".served")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("create served dir: %v", err)
	}

	h := newTestHandler(dir)
	h.UploadEnabled = true
	h.UploadMaxBytes = 1024 * 1024

	req := newUploadRequest(t, "/", []uploadFixture{{Name: "roman2.py", Content: "print('ok')\n"}})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%q", rr.Code, rr.Body.String())
	}

	data, err := os.ReadFile(filepath.Join(dir, "roman2.py"))
	if err != nil {
		t.Fatalf("expected uploaded file to exist: %v", err)
	}
	if string(data) != "print('ok')\n" {
		t.Fatalf("unexpected uploaded content: %q", string(data))
	}
}
