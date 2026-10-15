package server

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/workspace"
)

type contentRequest struct {
	RequestID string  `json:"requestId" validate:"required"`
	Path      string  `json:"path" validate:"required"`
	Content   *string `json:"content" validate:"required"`
	Unique    *bool   `json:"unique,omitempty"`
}

func (v *contentRequest) UnmarshalJSON(b []byte) error {
	type plain contentRequest
	var value plain
	if e := json.Unmarshal(b, &value); e != nil {
		return e
	}
	var fields map[string]json.RawMessage
	if e := json.Unmarshal(b, &fields); e != nil {
		return e
	}
	if raw, ok := fields["unique"]; ok && string(raw) == "null" {
		return errors.New("unique must be boolean when present")
	}
	*v = contentRequest(value)
	return nil
}

func fileRoute(v string) bool {
	switch v {
	case "/api/upload", "/api/upload/commit", "/api/files/list", "/api/files/content", "/api/files/mkdir", "/api/files/rename", "/api/files/copy", "/api/files/delete":
		return true
	}
	return false
}
func fileMethod(v, m string) bool {
	if v == "/api/files/list" {
		return m == "GET"
	}
	if v == "/api/files/content" {
		return m == "GET" || m == "POST"
	}
	return m == "POST"
}
func fileError(w http.ResponseWriter, e error) {
	if errors.Is(e, os.ErrNotExist) {
		reject(w, 404, "not_found", "File not found")
		return
	}
	if errors.Is(e, os.ErrExist) {
		reject(w, 409, "invalid_input", "Destination already exists")
		return
	}
	objectError(w, e)
}
func serveFiles(w http.ResponseWriter, r *http.Request, s *workspace.Service) {
	if !fileMethod(r.URL.Path, r.Method) {
		reject(w, 405, "invalid_input", "Method not allowed")
		return
	}
	q, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil {
		reject(w, 400, "invalid_input", "Malformed query")
		return
	}
	for key, values := range q {
		allowed := key == "project_id"
		if r.Method == "GET" && r.URL.Path == "/api/files/list" {
			allowed = allowed || key == "type" || key == "sort" || key == "offset" || key == "limit"
		}
		if r.Method == "GET" && r.URL.Path == "/api/files/content" {
			allowed = allowed || key == "path"
		}
		if len(values) != 1 || !allowed {
			reject(w, 400, "invalid_input", "Unknown or repeated query parameter")
			return
		}
	}
	p := q.Get("project_id")
	if e = (Scope{ProjectID: p, RequestID: "query", Operation: "files"}).Validate(); e != nil || p == "_app" {
		reject(w, 400, "invalid_input", "Invalid project_id")
		return
	}
	if s == nil || s.DB == nil {
		reject(w, 503, "upstream", "Workspace service unavailable")
		return
	}
	if r.Method == "GET" {
		if r.URL.Path == "/api/files/list" {
			offset, limit := 0, 50
			if q.Has("offset") {
				offset, e = strconv.Atoi(q.Get("offset"))
				if e != nil {
					reject(w, 400, "invalid_input", "Invalid offset")
					return
				}
			}
			if q.Has("limit") {
				limit, e = strconv.Atoi(q.Get("limit"))
				if e != nil {
					reject(w, 400, "invalid_input", "Invalid limit")
					return
				}
			}
			out, e := s.List(r.Context(), p, q.Get("type"), q.Get("sort"), offset, limit)
			if e != nil {
				fileError(w, e)
				return
			}
			for i := range out.Files {
				out.Files[i].URL = "/api/files/content?project_id=" + url.QueryEscape(p) + "&path=" + url.QueryEscape(out.Files[i].Path)
			}
			respond(w, 200, out)
			return
		}
		f, e := s.OpenContent(r.Context(), p, q.Get("path"))
		if e != nil {
			fileError(w, e)
			return
		}
		defer f.Close()
		fp, e := workspace.Fingerprint(f, false)
		if e != nil {
			fileError(w, e)
			return
		}
		w.Header().Set("Content-Type", fp.MIME)
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
		w.Header().Set("Access-Control-Expose-Headers", "Content-Range, Accept-Ranges, Content-Length")
		if strings.Contains(fp.MIME, "text/html") || strings.Contains(fp.MIME, "svg") || strings.EqualFold(strings.TrimSpace(q.Get("path")[max(0, len(q.Get("path"))-4):]), ".svg") {
			w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": f.Name()}))
		}
		st, e := f.Stat()
		if e != nil {
			fileError(w, e)
			return
		}
		http.ServeContent(&contentWriter{ResponseWriter: w}, r, f.Name(), st.ModTime(), f)
		return
	}
	if r.URL.Path == "/api/upload" {
		serveUpload(w, r, s, p)
		return
	}
	var in workspace.Input
	op := strings.TrimPrefix(r.URL.Path, "/api/files/")
	switch r.URL.Path {
	case "/api/upload/commit":
		v, err := DecodeJSON[struct {
			RequestID string `json:"requestId" validate:"required"`
			Path      string `json:"path" validate:"required"`
		}](w, r)
		if err != nil {
			reject(w, 400, "invalid_input", err.Error())
			return
		}
		a, err := s.Commit(r.Context(), p, v.RequestID, v.Path)
		if err != nil {
			fileError(w, err)
			return
		}
		respond(w, 200, map[string]any{"ok": true, "relative": a.Path})
		return
	case "/api/files/rename":
		v, err := DecodeJSON[struct {
			RequestID string `json:"requestId" validate:"required"`
			Path      string `json:"path" validate:"required"`
			NewName   string `json:"new_name" validate:"required"`
		}](w, r)
		e = err
		in = workspace.Input{RequestID: v.RequestID, Path: v.Path, NewName: v.NewName}
	case "/api/files/copy":
		v, err := DecodeJSON[struct {
			RequestID string   `json:"requestId" validate:"required"`
			Paths     []string `json:"paths" validate:"required,min=1,max=100"`
			Target    string   `json:"target" validate:"required"`
		}](w, r)
		e = err
		in = workspace.Input{RequestID: v.RequestID, Paths: v.Paths, Target: v.Target}
	case "/api/files/delete":
		v, err := DecodeJSON[struct {
			RequestID string   `json:"requestId" validate:"required"`
			Paths     []string `json:"paths" validate:"required,min=1,max=100"`
		}](w, r)
		e = err
		in = workspace.Input{RequestID: v.RequestID, Paths: v.Paths}
	case "/api/files/mkdir":
		v, err := DecodeJSON[struct {
			RequestID string `json:"requestId" validate:"required"`
			Path      string `json:"path" validate:"required"`
		}](w, r)
		e = err
		in = workspace.Input{RequestID: v.RequestID, Path: v.Path}
	case "/api/files/content":
		v, err := DecodeJSON[contentRequest](w, r)
		e = err
		in = workspace.Input{RequestID: v.RequestID, Path: v.Path, Unique: v.Unique}
		if v.Content != nil {
			in.Content = *v.Content
		}
	}
	if e != nil {
		reject(w, 400, "invalid_input", e.Error())
		return
	}
	out, e := s.Mutate(r.Context(), p, op, in)
	if e != nil {
		fileError(w, e)
		return
	}
	respond(w, 200, out)
}

// ServeContent remains responsible for RFC Range semantics; buffer only its small error body.
type contentWriter struct {
	http.ResponseWriter
	failed bool
}

func (w *contentWriter) WriteHeader(status int) {
	if status >= 400 {
		w.failed = true
		w.Header().Del("Content-Length")
		w.Header().Del("Content-Disposition")
		reject(w.ResponseWriter, status, "invalid_input", "Invalid content range or precondition")
		return
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *contentWriter) Write(b []byte) (int, error) {
	if w.failed {
		return len(b), nil
	}
	return w.ResponseWriter.Write(b)
}
func serveUpload(w http.ResponseWriter, r *http.Request, s *workspace.Service, p string) {
	r.Body = http.MaxBytesReader(w, r.Body, s.MaxUpload+(1<<20))
	mr, e := r.MultipartReader()
	if e != nil {
		reject(w, 400, "invalid_input", "Expected multipart upload")
		return
	}
	var key, name string
	var file io.Reader

	seen := map[string]bool{} // Multipart field order is arbitrary; spool through confined staging after requestId is known.
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			reject(w, 400, "invalid_input", "Interrupted multipart upload")
			return
		}
		field := part.FormName()
		if seen[field] || (field != "file" && field != "requestId") {
			part.Close()
			reject(w, 400, "invalid_input", "Expected one file and requestId")
			return
		}
		seen[field] = true
		if field == "requestId" {
			b, err := io.ReadAll(io.LimitReader(part, 129))
			if err != nil || len(b) > 128 {
				part.Close()
				reject(w, 400, "invalid_input", "Invalid requestId")
				return
			}
			key = string(b)
		} else {
			_, params, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
			if err != nil {
				part.Close()
				reject(w, 400, "invalid_input", "Invalid filename")
				return
			}
			name = params["filename"]
			spool, cleanup, err := s.Intake(r.Context(), p, part)
			if err != nil {
				part.Close()
				fileError(w, err)
				return
			}
			defer cleanup()
			file = spool
		}
		part.Close()
	}
	if !seen["file"] || !seen["requestId"] {
		reject(w, 400, "invalid_input", "Expected file and requestId")
		return
	}
	v, e := s.Stage(r.Context(), p, key, name, file)
	if e != nil {
		fileError(w, e)
		return
	}
	respond(w, 200, map[string]string{"path": v})
}
