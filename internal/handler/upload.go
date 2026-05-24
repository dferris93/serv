package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"serv/internal/logging"
	"serv/internal/security"
)

type uploadFileResult struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Message    string `json:"message,omitempty"`
	HTTPStatus int    `json:"-"`
}

type uploadResponse struct {
	Uploaded int                `json:"uploaded"`
	Failed   int                `json:"failed"`
	Files    []uploadFileResult `json:"files"`
}

func (h *Handler) handleUpload(rw *logging.ResponseWriter, r *http.Request, ctx *security.RequestContext) {
	target, err := h.resolveUploadTarget(ctx.RelPath)
	if err != nil {
		http.Error(rw, "404 not found", http.StatusNotFound)
		return
	}

	if !h.UploadEnabled && !target.OneTimeUpload {
		http.Error(rw, "405 method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.UploadMaxBytes > 0 {
		r.Body = http.MaxBytesReader(rw, r.Body, h.UploadMaxBytes)
	}

	if target.FileName != "" {
		h.handleSingleFileUpload(rw, r, target)
		return
	}
	h.handleDirectoryUpload(rw, r, target)
}

type uploadTarget struct {
	TargetDir     string
	RelDir        string
	FileName      string
	OneTimeUpload bool
}

func (h *Handler) resolveUploadTarget(relPath string) (uploadTarget, error) {
	targetDir := filepath.Join(h.Dir, filepath.FromSlash(relPath))
	if info, err := os.Stat(targetDir); err == nil {
		if info.IsDir() {
			return h.withUploadPolicy(uploadTarget{
				TargetDir: targetDir,
				RelDir:    relPath,
			}), nil
		}
		relDir := path.Dir(relPath)
		if relDir == "." {
			relDir = ""
		}
		return h.withUploadPolicy(uploadTarget{
			TargetDir: filepath.Dir(targetDir),
			RelDir:    relDir,
			FileName:  path.Base(relPath),
		}), nil
	} else if !os.IsNotExist(err) {
		return uploadTarget{}, err
	}

	if relPath == "" {
		return uploadTarget{}, os.ErrNotExist
	}

	relDir := path.Dir(relPath)
	if relDir == "." {
		relDir = ""
	}
	parentDir := filepath.Join(h.Dir, filepath.FromSlash(relDir))
	parentInfo, err := os.Stat(parentDir)
	if err != nil {
		return uploadTarget{}, err
	}
	if !parentInfo.IsDir() {
		return uploadTarget{}, os.ErrNotExist
	}

	return h.withUploadPolicy(uploadTarget{
		TargetDir: parentDir,
		RelDir:    relDir,
		FileName:  path.Base(relPath),
	}), nil
}

func (h *Handler) withUploadPolicy(target uploadTarget) uploadTarget {
	target.OneTimeUpload = h.isOneTimeUploadDir(target.TargetDir)
	return target
}

func (h *Handler) handleDirectoryUpload(rw *logging.ResponseWriter, r *http.Request, target uploadTarget) {
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(rw, "413 request entity too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(rw, "400 bad request", http.StatusBadRequest)
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}

	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		files = r.MultipartForm.File["file"]
	}
	if len(files) == 0 {
		h.writeUploadResponse(rw, http.StatusBadRequest, uploadResponse{
			Failed: 1,
			Files: []uploadFileResult{{
				Status:  "error",
				Message: "no files selected",
			}},
		})
		return
	}

	resp := uploadResponse{Files: make([]uploadFileResult, 0, len(files))}
	for _, fileHeader := range files {
		result := h.storeUploadedFile(target, fileHeader)
		resp.Files = append(resp.Files, result)
		if result.Status == "uploaded" {
			resp.Uploaded++
		} else {
			resp.Failed++
		}
	}

	status := http.StatusCreated
	if resp.Uploaded == 0 {
		status = http.StatusBadRequest
	} else if resp.Failed > 0 {
		status = http.StatusMultiStatus
	}
	h.writeUploadResponse(rw, status, resp)
}

func (h *Handler) handleSingleFileUpload(rw *logging.ResponseWriter, r *http.Request, target uploadTarget) {
	fileName := target.FileName
	if _, err := sanitizeUploadFilename(fileName); err != nil {
		h.writeUploadResponse(rw, http.StatusBadRequest, uploadResponse{
			Failed: 1,
			Files: []uploadFileResult{{
				Name:    fileName,
				Status:  "error",
				Message: err.Error(),
			}},
		})
		return
	}

	contentType := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(16 << 20); err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				http.Error(rw, "413 request entity too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(rw, "400 bad request", http.StatusBadRequest)
			return
		}
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}

		files := r.MultipartForm.File["files"]
		if len(files) == 0 {
			files = r.MultipartForm.File["file"]
		}
		if len(files) != 1 {
			h.writeUploadResponse(rw, http.StatusBadRequest, uploadResponse{
				Failed: 1,
				Files: []uploadFileResult{{
					Name:    fileName,
					Status:  "error",
					Message: "exactly one file is required for filename-in-URL uploads",
				}},
			})
			return
		}

		src, err := files[0].Open()
		if err != nil {
			h.writeUploadResponse(rw, http.StatusBadRequest, uploadResponse{
				Failed: 1,
				Files: []uploadFileResult{{
					Name:    fileName,
					Status:  "error",
					Message: "failed to read uploaded file",
				}},
			})
			return
		}
		defer src.Close()

		result := h.storeUploadedContent(target, fileName, src)
		status := http.StatusCreated
		resp := uploadResponse{
			Files: []uploadFileResult{result},
		}
		if result.Status == "uploaded" {
			resp.Uploaded = 1
		} else {
			resp.Failed = 1
			status = uploadFailureStatus(result)
		}
		h.writeUploadResponse(rw, status, resp)
		return
	}

	result := h.storeUploadedContent(target, fileName, r.Body)
	status := http.StatusCreated
	resp := uploadResponse{
		Files: []uploadFileResult{result},
	}
	if result.Status == "uploaded" {
		resp.Uploaded = 1
	} else {
		resp.Failed = 1
		status = uploadFailureStatus(result)
	}
	h.writeUploadResponse(rw, status, resp)
}

func (h *Handler) storeUploadedFile(target uploadTarget, fileHeader *multipart.FileHeader) uploadFileResult {
	name, err := sanitizeUploadFilename(fileHeader.Filename)
	if err != nil {
		return uploadFileResult{Name: fileHeader.Filename, Status: "error", Message: err.Error()}
	}

	src, err := fileHeader.Open()
	if err != nil {
		return uploadFileResult{Name: name, Status: "error", Message: "failed to read uploaded file"}
	}
	defer src.Close()

	return h.storeUploadedContent(target, name, src)
}

func (h *Handler) storeUploadedContent(target uploadTarget, name string, src io.Reader) uploadFileResult {
	relPath := name
	if target.RelDir != "" {
		relPath = path.Join(target.RelDir, name)
	}

	if err := h.checkUploadACLs(relPath); err != nil {
		return uploadFileResult{Name: name, Status: "error", Message: err.Error()}
	}

	dstPath := filepath.Join(target.TargetDir, name)
	key, err := filepath.Abs(dstPath)
	if err != nil {
		return uploadFileResult{Name: name, Status: "error", Message: "upload failed"}
	}
	key = filepath.Clean(key)
	if !h.claimUpload(key) {
		return uploadFileResult{Name: name, Status: "error", Message: "upload failed"}
	}
	defer h.releaseUpload(key)

	overwrite := h.UploadOverwrite && !target.OneTimeUpload
	if err := writeUploadedFile(dstPath, src, overwrite); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return uploadFileResult{Name: name, Status: "error", Message: "request entity too large", HTTPStatus: http.StatusRequestEntityTooLarge}
		}
		if errors.Is(err, os.ErrExist) {
			return uploadFileResult{Name: name, Status: "error", Message: "upload failed"}
		}
		return uploadFileResult{Name: name, Status: "error", Message: "failed to write file"}
	}

	return uploadFileResult{Name: name, Status: "uploaded"}
}

func (h *Handler) claimUpload(key string) bool {
	h.uploadMu.Lock()
	defer h.uploadMu.Unlock()

	if h.uploadActive == nil {
		h.uploadActive = make(map[string]struct{})
	}
	if _, ok := h.uploadActive[key]; ok {
		return false
	}
	h.uploadActive[key] = struct{}{}
	return true
}

func (h *Handler) releaseUpload(key string) {
	h.uploadMu.Lock()
	defer h.uploadMu.Unlock()
	delete(h.uploadActive, key)
}

func uploadFailureStatus(result uploadFileResult) int {
	if result.HTTPStatus != 0 {
		return result.HTTPStatus
	}
	return http.StatusBadRequest
}

func (h *Handler) checkUploadACLs(relPath string) error {
	reason := security.EvaluatePathACL(security.PathACLContext{
		Dir:           h.Dir,
		RelPath:       relPath,
		Name:          path.Base(relPath),
		AllowDotFiles: h.AllowDotFiles,
		Sensitive:     h.Sensitive,
		FilterGlobs:   h.FilterGlobs,
	})
	switch reason {
	case security.PathACLHtaccess:
		return fmt.Errorf(".htaccess uploads are blocked")
	case security.PathACLFiltered:
		return fmt.Errorf("blocked by filter rules")
	case security.PathACLDotfile:
		return fmt.Errorf("dotfiles are disabled")
	case security.PathACLSensitive:
		return fmt.Errorf("path is protected")
	}

	if !security.IsRequestAuthorized(h.Dir, relPath, h.AllowInsecure, h.AllowDotFiles) {
		return fmt.Errorf("path is not allowed")
	}

	return nil
}

func sanitizeUploadFilename(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", fmt.Errorf("invalid filename")
	}
	if strings.Contains(trimmed, "/") || strings.Contains(trimmed, "\\") {
		return "", fmt.Errorf("filename must not include path separators")
	}
	base := path.Base(trimmed)
	if base == "" || base == "." || base == ".." {
		return "", fmt.Errorf("invalid filename")
	}
	return base, nil
}

func writeUploadedFile(dstPath string, src io.Reader, overwrite bool) error {
	flags := os.O_CREATE | os.O_WRONLY
	if overwrite {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_EXCL
	}

	dst, err := os.OpenFile(dstPath, flags, 0o600)
	if err != nil {
		return err
	}

	copyErr := error(nil)
	if _, err := io.Copy(dst, src); err != nil {
		copyErr = err
	}
	closeErr := dst.Close()

	if copyErr != nil {
		_ = os.Remove(dstPath)
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	return nil
}

func (h *Handler) writeUploadResponse(rw *logging.ResponseWriter, status int, resp uploadResponse) {
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(status)
	_ = json.NewEncoder(rw).Encode(resp)
}
