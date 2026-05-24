package handler

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"serv/internal/logging"
	"serv/internal/security"
)

type dirEntry struct {
	Name      string
	Href      string
	Size      string
	ModTime   string
	IconClass string
}

type dirListingData struct {
	Title         string
	Path          string
	ParentHref    string
	HasParent     bool
	Entries       []dirEntry
	GeneratedAt   string
	UploadEnabled bool
	ShowListing   bool
}

var dirListingTemplate = template.Must(template.New("dir-listing").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Title}}</title>
  <style>
    :root {
      --bg: #0c0f14;
      --window: #151922;
      --border: #272d39;
      --titlebar: linear-gradient(#1b212d, #141a24);
      --text: #e7ecf3;
      --muted: #9aa4b2;
      --row: #151a24;
      --row-alt: #121722;
      --row-hover: #1e2432;
      --link: #c6b0ff;
      --folder-top: #c9b6ff;
      --folder-bottom: #7a57c6;
      --file: #2a3140;
      --file-fold: #3b4356;
    }

    * { box-sizing: border-box; }

    body {
      margin: 0;
      font-family: "SF Pro Text", "Helvetica Neue", Helvetica, Arial, sans-serif;
      background: radial-gradient(circle at top, #1b2230 0%, #0c1016 55%, #07090d 100%);
      color: var(--text);
      min-height: 100vh;
      padding: 32px 18px 48px;
    }

    .window {
      max-width: 980px;
      margin: 0 auto;
      border-radius: 14px;
      background: var(--window);
      border: 1px solid var(--border);
      box-shadow: 0 24px 70px rgba(0, 0, 0, 0.45), 0 6px 18px rgba(0, 0, 0, 0.35);
      overflow: hidden;
    }

    .titlebar {
      height: 44px;
      padding: 0 16px;
      display: flex;
      align-items: center;
      border-bottom: 1px solid var(--border);
      background: var(--titlebar);
    }

    .titlebar .path {
      font-size: 13px;
      color: var(--muted);
      letter-spacing: 0.02em;
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }

    .toolbar {
      display: flex;
      justify-content: space-between;
      align-items: center;
      padding: 14px 18px;
      border-bottom: 1px solid var(--border);
      background: #121722;
    }

    .toolbar h1 {
      font-size: 18px;
      margin: 0;
      font-weight: 600;
    }

    .toolbar .meta {
      font-size: 12px;
      color: var(--muted);
    }

    table {
      width: 100%;
      border-collapse: collapse;
      font-size: 14px;
    }

    thead th {
      text-align: left;
      font-size: 11px;
      text-transform: uppercase;
      letter-spacing: 0.12em;
      color: var(--muted);
      padding: 10px 18px;
      border-bottom: 1px solid var(--border);
      background: #111622;
    }

    tbody tr {
      background: var(--row);
    }

    tbody tr:nth-child(even) {
      background: var(--row-alt);
    }

    tbody tr:hover {
      background: var(--row-hover);
    }

    tbody td {
      padding: 10px 18px;
      border-bottom: 1px solid #202636;
      vertical-align: middle;
    }

    .name-cell {
      display: flex;
      align-items: center;
      gap: 12px;
    }

    .icon {
      width: 18px;
      height: 14px;
      border-radius: 3px;
      background: linear-gradient(180deg, var(--folder-top) 0%, var(--folder-bottom) 100%);
      box-shadow: inset 0 -2px 0 rgba(0, 0, 0, 0.25);
      flex-shrink: 0;
    }

    .icon.file {
      width: 14px;
      height: 16px;
      border-radius: 2px;
      background: var(--file);
      position: relative;
      box-shadow: inset 0 -2px 0 rgba(0, 0, 0, 0.25);
    }

    .icon.file::after {
      content: "";
      position: absolute;
      top: 0;
      right: 0;
      border-top: 6px solid var(--file-fold);
      border-left: 6px solid transparent;
    }

    a {
      color: var(--text);
      text-decoration: none;
    }

    a:hover {
      color: var(--link);
    }

    .muted {
      color: var(--muted);
      font-size: 12px;
    }

    .upload-panel {
      padding: 16px 18px;
      border-bottom: 1px solid var(--border);
      background: #101522;
    }

    .upload-form {
      display: grid;
      gap: 10px;
    }

    .upload-zone {
      display: flex;
      flex-direction: column;
      gap: 6px;
      padding: 16px;
      border: 1px dashed #4a5672;
      border-radius: 10px;
      background: #0f1522;
      cursor: pointer;
      transition: border-color 120ms ease, background-color 120ms ease;
    }

    .upload-zone.dragging {
      border-color: #8eb5ff;
      background: #1a2234;
    }

    .upload-zone.uploading {
      opacity: 0.7;
      pointer-events: none;
    }

    .upload-zone strong {
      font-size: 14px;
      font-weight: 600;
    }

    .upload-zone span {
      color: var(--muted);
      font-size: 12px;
    }

    .upload-actions {
      display: flex;
      align-items: center;
      gap: 10px;
      flex-wrap: wrap;
    }

    .upload-input {
      max-width: 100%;
      color: var(--muted);
      font-size: 12px;
    }

    .upload-submit {
      border: 1px solid #3f4f72;
      border-radius: 7px;
      background: #1d2b45;
      color: #dce8ff;
      padding: 8px 12px;
      font-size: 13px;
      cursor: pointer;
    }

    .upload-submit[disabled] {
      opacity: 0.6;
      cursor: wait;
    }

    .upload-status {
      margin: 0;
    }

    .upload-results {
      margin: 0;
      padding-left: 18px;
      color: var(--muted);
      font-size: 12px;
      display: grid;
      gap: 4px;
    }

    .upload-results .ok {
      color: #82d4a5;
    }

    .upload-results .error {
      color: #ff9a9a;
    }

    @media (max-width: 640px) {
      body {
        padding: 18px 10px 32px;
      }

      .toolbar {
        flex-direction: column;
        align-items: flex-start;
        gap: 6px;
      }

      thead th:nth-child(2),
      thead th:nth-child(3),
      tbody td:nth-child(2),
      tbody td:nth-child(3) {
        display: none;
      }
    }
  </style>
</head>
<body>
  <div class="window">
    <div class="titlebar">
      <div class="path">{{.Path}}</div>
    </div>
    <div class="toolbar">
      <h1>{{.Title}}</h1>
      <div class="meta">Generated {{.GeneratedAt}}</div>
    </div>
    {{if .UploadEnabled}}
    <div class="upload-panel">
      <form id="upload-form" class="upload-form" method="post" enctype="multipart/form-data">
        <label id="upload-zone" class="upload-zone" for="upload-input">
          <strong>Drop files to upload</strong>
          <span>or choose files from disk</span>
        </label>
        <div class="upload-actions">
          <input id="upload-input" class="upload-input" type="file" name="files" multiple>
          <button id="upload-submit" class="upload-submit" type="submit">Upload selected files</button>
        </div>
        <p id="upload-status" class="upload-status muted">No files selected.</p>
        <ul id="upload-results" class="upload-results"></ul>
      </form>
    </div>
    {{end}}
    {{if .ShowListing}}<table>
      <thead>
        <tr>
          <th>Name</th>
          <th>Modified</th>
          <th>Size</th>
        </tr>
      </thead>
      <tbody>
        {{if .HasParent}}
        <tr>
          <td>
            <div class="name-cell">
              <span class="icon"></span>
              <a href="{{.ParentHref}}">..</a>
            </div>
          </td>
          <td class="muted">Parent directory</td>
          <td class="muted">--</td>
        </tr>
        {{end}}
        {{range .Entries}}
        <tr>
          <td>
            <div class="name-cell">
              <span class="icon {{.IconClass}}"></span>
              <a href="{{.Href}}">{{.Name}}</a>
            </div>
          </td>
          <td class="muted">{{.ModTime}}</td>
          <td class="muted">{{.Size}}</td>
        </tr>
        {{end}}
      </tbody>
    </table>{{end}}
  </div>
  {{if .UploadEnabled}}
  <script>
    (function () {
      var form = document.getElementById("upload-form");
      if (!form) {
        return;
      }

      var zone = document.getElementById("upload-zone");
      var input = document.getElementById("upload-input");
      var submit = document.getElementById("upload-submit");
      var status = document.getElementById("upload-status");
      var results = document.getElementById("upload-results");

      function renderResults(files) {
        results.innerHTML = "";
        for (var i = 0; i < files.length; i++) {
          var item = files[i];
          var li = document.createElement("li");
          li.className = item.status === "uploaded" ? "ok" : "error";
          var message = item.name || "file";
          if (item.message) {
            message += ": " + item.message;
          } else if (item.status === "uploaded") {
            message += ": uploaded";
          }
          li.textContent = message;
          results.appendChild(li);
        }
      }

      function setSelectionMessage(files) {
        var count = files ? files.length : 0;
        if (count === 0) {
          status.textContent = "No files selected.";
          return;
        }
        status.textContent = count + " file(s) selected.";
      }

      function buildUploadURL(fileName) {
        var basePath = window.location.pathname;
        if (basePath.slice(-1) !== "/") {
          basePath += "/";
        }
        return basePath + encodeURIComponent(fileName);
      }

      async function upload(files) {
        if (!files || files.length === 0) {
          setSelectionMessage(files);
          return;
        }

        submit.disabled = true;
        zone.classList.add("uploading");
        status.textContent = "Uploading 0/" + files.length + " file(s)...";
        results.innerHTML = "";

        try {
          var uploaded = 0;
          var failed = 0;
          var fileResults = [];

          for (var i = 0; i < files.length; i++) {
            var file = files[i];
            status.textContent = "Uploading " + (i + 1) + "/" + files.length + " file(s)...";

            try {
              var response = await fetch(buildUploadURL(file.name), {
                method: "POST",
                body: file
              });
              var text = await response.text();
              var payload = null;
              try {
                payload = JSON.parse(text);
              } catch (e) {
                payload = null;
              }

              if (!payload || !payload.files || payload.files.length === 0) {
                fileResults.push({
                  name: file.name,
                  status: "error",
                  message: "upload failed (HTTP " + response.status + ")"
                });
                failed++;
                continue;
              }

              uploaded += payload.uploaded || 0;
              failed += payload.failed || 0;
              fileResults.push(payload.files[0]);
            } catch (err) {
              fileResults.push({
                name: file.name,
                status: "error",
                message: err.message || "upload failed"
              });
              failed++;
            }
          }

          renderResults(fileResults);
          status.textContent = "Uploaded " + uploaded + ", failed " + failed + ".";
          if (uploaded > 0) {
            setTimeout(function () {
              window.location.reload();
            }, 600);
          }
        } catch (err) {
          status.textContent = err.message || "Upload failed.";
        } finally {
          submit.disabled = false;
          zone.classList.remove("uploading");
        }
      }

      form.addEventListener("submit", function (event) {
        event.preventDefault();
        upload(input.files);
      });

      input.addEventListener("change", function () {
        setSelectionMessage(input.files);
      });

      ["dragenter", "dragover"].forEach(function (eventName) {
        zone.addEventListener(eventName, function (event) {
          event.preventDefault();
          event.stopPropagation();
          zone.classList.add("dragging");
        });
      });

      ["dragleave", "drop"].forEach(function (eventName) {
        zone.addEventListener(eventName, function (event) {
          event.preventDefault();
          event.stopPropagation();
          zone.classList.remove("dragging");
        });
      });

      zone.addEventListener("drop", function (event) {
        upload(event.dataTransfer.files);
      });
    })();
  </script>
  {{end}}
</body>
</html>`))

func (h *Handler) serveDir(rw *logging.ResponseWriter, r *http.Request, fullPath string, relPath string, ac bool) {
	if !strings.HasSuffix(r.URL.Path, "/") {
		target := r.URL.Path + "/"
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(rw, r, target, http.StatusMovedPermanently)
		logging.LogRequest(h.Logger, r, rw.Size, rw.StatusCode)
		return
	}

	entries, err := os.ReadDir(fullPath)
	if err != nil {
		h.logAndReturnError(rw, r, ac, "500 internal server error", http.StatusInternalServerError)
		return
	}

	filters := h.EntryFilters
	if len(filters) == 0 {
		filters = security.DefaultEntryFilters()
	}
	hideListing := h.isOneTimeUploadDir(fullPath)
	listing := make([]dirEntry, 0, len(entries))
	if !hideListing {
		for _, entry := range entries {
			name := entry.Name()
			entryRel := name
			if relPath != "" {
				entryRel = path.Join(relPath, name)
			}
			entryCtx := security.EntryContext{
				Dir:           h.Dir,
				RelPath:       entryRel,
				Name:          name,
				AllowDotFiles: h.AllowDotFiles,
				Sensitive:     h.Sensitive,
				FilterGlobs:   h.FilterGlobs,
			}
			if !security.ApplyEntryFilters(filters, &entryCtx) {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			href := path.Join(r.URL.Path, url.PathEscape(name))
			iconClass := "file"
			size := formatBytes(info.Size())
			if entry.IsDir() {
				name += "/"
				href += "/"
				iconClass = "dir"
				size = "--"
			}

			listing = append(listing, dirEntry{
				Name:      name,
				Href:      href,
				Size:      size,
				ModTime:   info.ModTime().Format("Jan 02, 2006 15:04"),
				IconClass: iconClass,
			})
		}
	}

	sort.Slice(listing, func(i, j int) bool {
		leftDir := strings.HasSuffix(listing[i].Name, "/")
		rightDir := strings.HasSuffix(listing[j].Name, "/")
		if leftDir != rightDir {
			return leftDir
		}
		return strings.ToLower(listing[i].Name) < strings.ToLower(listing[j].Name)
	})

	data := dirListingData{
		Title:         "Index of " + r.URL.Path,
		Path:          r.URL.Path,
		Entries:       listing,
		GeneratedAt:   time.Now().Format("Jan 02, 2006 15:04"),
		UploadEnabled: h.UploadEnabled || hideListing,
		ShowListing:   !hideListing,
	}

	if r.URL.Path != "/" && !hideListing {
		parent := path.Dir(strings.TrimSuffix(r.URL.Path, "/"))
		if parent == "." {
			parent = "/"
		}
		if !strings.HasSuffix(parent, "/") {
			parent += "/"
		}
		data.HasParent = true
		data.ParentHref = parent
	}

	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := dirListingTemplate.Execute(rw, data); err != nil {
		h.logAndReturnError(rw, r, ac, "500 internal server error", http.StatusInternalServerError)
		return
	}

	logging.LogRequest(h.Logger, r, rw.Size, rw.StatusCode)
}

func formatBytes(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}

	units := []string{"KB", "MB", "GB", "TB", "PB"}
	value := float64(size)
	for _, unit := range units {
		value /= 1024
		if value < 1024 {
			if value < 10 {
				return fmt.Sprintf("%.1f %s", value, unit)
			}
			return fmt.Sprintf("%.0f %s", value, unit)
		}
	}

	return fmt.Sprintf("%.0f PB", value)
}
