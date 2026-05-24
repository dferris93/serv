package handler

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"serv/internal/logging"
	"serv/internal/security"
)

type Handler struct {
	Dir                 string
	AllowInsecure       bool
	AllowDotFiles       bool
	AllowedIPs          security.IPChecker
	Sensitive           []security.SensitiveFile
	Username            string
	Password            string
	Headers             map[string]string
	Redirects           map[string]string
	FilterGlobs         []string
	RequestChecks       []security.RequestCheck
	EntryFilters        []security.EntryFilter
	UploadEnabled       bool
	UploadMaxBytes      int64
	UploadOverwrite     bool
	OneTimeDownloadDirs []string
	OneTimeUploadDirs   []string
	Logger              *log.Logger

	oneTimeMu     sync.Mutex
	oneTimeActive map[string]struct{}

	uploadMu     sync.Mutex
	uploadActive map[string]struct{}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rw := logging.NewResponseWriter(w)

	ctx := security.RequestContext{
		Req:           r,
		Dir:           h.Dir,
		AllowInsecure: h.AllowInsecure,
		AllowDotFiles: h.AllowDotFiles,
		AllowedIPs:    h.AllowedIPs,
		Sensitive:     h.Sensitive,
		FilterGlobs:   h.FilterGlobs,
		Username:      h.Username,
		Password:      h.Password,
	}
	checks := h.RequestChecks
	if len(checks) == 0 {
		checks = security.DefaultRequestChecks()
	}
	if result := security.RunRequestChecks(checks, &ctx); result != nil {
		h.logAndReturnError(rw, r, result.Auth, result.Public, result.Status)
		return
	}

	if url, ok := h.Redirects[r.URL.Path]; ok {
		http.Redirect(rw, r, url, http.StatusFound)
		logging.LogRequest(h.Logger, r, rw.Size, rw.StatusCode)
		return
	}

	if r.Method == http.MethodPost {
		h.handleUpload(rw, r, &ctx)
		logging.LogRequest(h.Logger, r, rw.Size, rw.StatusCode)
		return
	}

	fullPath := filepath.Join(h.Dir, filepath.FromSlash(ctx.RelPath))
	info, err := os.Stat(fullPath)
	if err != nil {
		h.logAndReturnError(rw, r, ctx.Authed, "404 not found", http.StatusNotFound)
		return
	}

	if !info.IsDir() && h.isOneTimeUploadPath(fullPath) {
		h.logAndReturnError(rw, r, ctx.Authed, "404 not found", http.StatusNotFound)
		return
	}

	for key, value := range h.Headers {
		rw.Header().Set(key, value)
	}

	if info.IsDir() {
		indexFile := filepath.Join(fullPath, "index.html")
		if !h.isOneTimeUploadDir(fullPath) {
			if _, err := os.Stat(indexFile); err == nil {
				http.ServeFile(rw, r, indexFile)
				logging.LogRequest(h.Logger, r, rw.Size, rw.StatusCode)
				return
			}
		}
		h.serveDir(rw, r, fullPath, ctx.RelPath, ctx.Authed)
		return
	}

	if key, ok := h.oneTimeDownloadKey(fullPath, info); ok && r.Method == http.MethodGet {
		h.serveOneTimeDownload(rw, r, fullPath, info, key, ctx.Authed)
		return
	}

	http.ServeFile(rw, r, fullPath)
	logging.LogRequest(h.Logger, r, rw.Size, rw.StatusCode)
}

func (h *Handler) logAndReturnError(rw *logging.ResponseWriter, r *http.Request, ac bool, errorMsg string, errorCode int) {
	if !ac {
		rw.Header().Set("WWW-Authenticate", `Basic realm="Enter username and password"`)
	}
	http.Error(rw, errorMsg, errorCode)
	logging.LogRequest(h.Logger, r, rw.Size, rw.StatusCode)
}
