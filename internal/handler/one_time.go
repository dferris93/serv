package handler

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"serv/internal/logging"
)

func ResolveOneTimeDownloadDirs(root string, dirs []string) ([]string, error) {
	return resolveConfiguredDirs(root, dirs, "one-time download")
}

func ResolveOneTimeUploadDirs(root string, dirs []string) ([]string, error) {
	return resolveConfiguredDirs(root, dirs, "one-time upload")
}

func ValidateOneTimeDirSeparation(downloadDirs []string, uploadDirs []string) error {
	for _, downloadDir := range downloadDirs {
		for _, uploadDir := range uploadDirs {
			if pathsOverlap(downloadDir, uploadDir) {
				return fmt.Errorf("one-time download directory %q overlaps one-time upload directory %q", downloadDir, uploadDir)
			}
		}
	}
	return nil
}

func pathsOverlap(left string, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	return pathWithinDir(left, right, true) || pathWithinDir(right, left, true)
}

func resolveConfiguredDirs(root string, dirs []string, label string) ([]string, error) {
	if len(dirs) == 0 {
		return nil, nil
	}

	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	rootAbs = filepath.Clean(rootAbs)
	rootResolved, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return nil, err
	}
	rootResolved = filepath.Clean(rootResolved)

	seen := make(map[string]struct{})
	out := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}

		fullPath := dir
		if !filepath.IsAbs(fullPath) {
			fullPath = filepath.Join(rootAbs, fullPath)
		}
		fullPath, err = filepath.Abs(fullPath)
		if err != nil {
			return nil, err
		}
		fullPath = filepath.Clean(fullPath)

		if !pathWithinDir(rootAbs, fullPath, true) {
			return nil, fmt.Errorf("%s directory %q is outside served directory %q", label, dir, root)
		}

		info, err := os.Stat(fullPath)
		if err != nil {
			return nil, fmt.Errorf("%s directory %q: %w", label, dir, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("%s path %q is not a directory", label, dir)
		}

		resolvedPath, err := filepath.EvalSymlinks(fullPath)
		if err != nil {
			return nil, fmt.Errorf("%s directory %q: %w", label, dir, err)
		}
		resolvedPath = filepath.Clean(resolvedPath)
		if !pathWithinDir(rootResolved, resolvedPath, true) {
			return nil, fmt.Errorf("%s directory %q resolves outside served directory %q", label, dir, root)
		}
		fullPath = resolvedPath

		if _, ok := seen[fullPath]; ok {
			continue
		}
		seen[fullPath] = struct{}{}
		out = append(out, fullPath)
	}

	return out, nil
}

func (h *Handler) isOneTimeUploadDir(fullPath string) bool {
	return h.pathInDirs(fullPath, h.OneTimeUploadDirs, true)
}

func (h *Handler) isOneTimeUploadPath(fullPath string) bool {
	return h.pathInDirs(fullPath, h.OneTimeUploadDirs, false)
}

func (h *Handler) pathInDirs(fullPath string, dirs []string, allowSame bool) bool {
	if len(dirs) == 0 {
		return false
	}

	fullAbs, err := filepath.Abs(fullPath)
	if err != nil {
		return false
	}
	fullAbs = filepath.Clean(fullAbs)
	if resolved, err := filepath.EvalSymlinks(fullAbs); err == nil {
		fullAbs = filepath.Clean(resolved)
	}

	for _, dir := range dirs {
		dirAbs, err := filepath.Abs(dir)
		if err != nil {
			continue
		}
		dirAbs = filepath.Clean(dirAbs)
		if pathWithinDir(dirAbs, fullAbs, allowSame) {
			return true
		}
	}

	return false
}

func (h *Handler) oneTimeDownloadKey(fullPath string, info os.FileInfo) (string, bool) {
	if len(h.OneTimeDownloadDirs) == 0 || info == nil || info.IsDir() || !info.Mode().IsRegular() {
		return "", false
	}

	fullAbs, err := filepath.Abs(fullPath)
	if err != nil {
		return "", false
	}
	fullAbs = filepath.Clean(fullAbs)

	if h.pathInDirs(fullAbs, h.OneTimeDownloadDirs, false) {
		return fullAbs, true
	}

	return "", false
}

func pathWithinDir(dir string, path string, allowSame bool) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	if rel == "." {
		return allowSame
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (h *Handler) claimOneTimeDownload(key string) bool {
	h.oneTimeMu.Lock()
	defer h.oneTimeMu.Unlock()

	if h.oneTimeActive == nil {
		h.oneTimeActive = make(map[string]struct{})
	}
	if _, ok := h.oneTimeActive[key]; ok {
		return false
	}
	h.oneTimeActive[key] = struct{}{}
	return true
}

func (h *Handler) releaseOneTimeDownload(key string) {
	h.oneTimeMu.Lock()
	defer h.oneTimeMu.Unlock()
	delete(h.oneTimeActive, key)
}

func (h *Handler) serveOneTimeDownload(rw *logging.ResponseWriter, r *http.Request, fullPath string, info os.FileInfo, key string, authed bool) {
	if !h.claimOneTimeDownload(key) {
		h.logAndReturnError(rw, r, authed, "404 not found", http.StatusNotFound)
		return
	}
	defer h.releaseOneTimeDownload(key)

	file, err := os.Open(fullPath)
	if err != nil {
		h.logAndReturnError(rw, r, authed, "404 not found", http.StatusNotFound)
		return
	}
	defer file.Close()

	http.ServeContent(rw, r, info.Name(), info.ModTime(), file)
	logging.LogRequest(h.Logger, r, rw.Size, rw.StatusCode)

	if rw.StatusCode != http.StatusOK || rw.WriteErr != nil {
		return
	}
	if err := os.Remove(fullPath); err != nil && h.Logger != nil {
		h.Logger.Printf("Error removing one-time download %q: %v", fullPath, err)
	}
}
