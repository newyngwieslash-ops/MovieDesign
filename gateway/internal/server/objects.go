package server

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/workspace"
)

func objectKind(path string) string {
	switch path {
	case "/api/projects":
		return "projects"
	case "/api/composition-types":
		return "composition_types"
	case "/api/compositions":
		return "compositions"
	case "/api/resources":
		return "resources"
	case "/api/versions":
		return "versions"
	case "/api/assets":
		return "assets"
	}
	return ""
}
func objectError(w http.ResponseWriter, err error) {
	var domain *store.DomainError
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &domain):
		reject(w, domain.Status, domain.Code, domain.Message)
	case errors.As(err, &tooLarge):
		reject(w, 413, "invalid_input", "JSON body exceeds 1 MiB")
	case errors.Is(err, store.ErrRequestConflict):
		reject(w, 409, "invalid_input", err.Error())
	case errors.Is(err, store.ErrRequestPending):
		reject(w, 409, "waiting_user", err.Error())
	default:
		reject(w, 502, "upstream", "Database operation failed")
	}
}
func serveObjects(w http.ResponseWriter, r *http.Request, s store.Objects, kind string, files *workspace.Service) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		reject(w, 405, "invalid_input", "Method not allowed")
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		reject(w, 400, "invalid_input", "Malformed query parameters")
		return
	}
	for key, values := range query {
		if len(values) != 1 {
			err = errors.New("Query parameters cannot be repeated")
			break
		}
		if key != "project_id" && (r.Method != "GET" || (key != "id" && key != "offset" && key != "limit")) {
			err = errors.New("Unknown query parameter")
			break
		}
	}
	if err != nil {
		reject(w, 400, "invalid_input", err.Error())
		return
	}
	p := query.Get("project_id")
	if kind != "projects" || query.Has("project_id") {
		if err = (store.Scope{ProjectID: p, RequestID: "query", Operation: kind}).Validate(); err != nil || p == "_app" {
			reject(w, 400, "invalid_input", "Invalid project_id")
			return
		}
	}
	if r.Method == http.MethodPost {
		input, e := DecodeJSON[store.Mutation](w, r)
		if e != nil {
			var large *http.MaxBytesError
			if errors.As(e, &large) {
				objectError(w, e)
			} else {
				reject(w, 400, "invalid_input", e.Error())
			}
			return
		}
		if kind == "projects" && input.Action == "create" {
			if p != "" {
				reject(w, 400, "invalid_input", "Project creation does not accept project_id")
				return
			}
			p = "_app"
		} else if p == "" {
			reject(w, 400, "invalid_input", "project_id is required")
			return
		}
		if kind == "assets" && input.Action == "create" {
			if input.ID != "" {
				reject(w, 400, "invalid_input", "create does not accept id")
				return
			}
			value, e := store.DecodeValue[struct {
				Path string `json:"path" validate:"required"`
			}](input.Value)
			if e != nil {
				reject(w, 400, "invalid_input", e.Error())
				return
			}
			if files == nil {
				reject(w, 503, "upstream", "Workspace unavailable")
				return
			}
			out, e := files.Register(r.Context(), p, input.RequestID, value.Path)
			if e != nil {
				fileError(w, e)
				return
			}
			respond(w, 200, out)
			return
		}
		out, e := s.Mutate(r.Context(), p, kind, input)
		if e != nil {
			objectError(w, e)
			return
		}
		respond(w, 200, out)
		return
	}
	if id, exists := query["id"]; exists {
		if query.Has("offset") || query.Has("limit") {
			reject(w, 400, "invalid_input", "id cannot be combined with pagination")
			return
		}
		if kind == "projects" && p == "" {
			p = id[0]
		}
		out, e := s.Get(r.Context(), p, kind, id[0])
		if e != nil {
			objectError(w, e)
			return
		}
		respond(w, 200, out)
		return
	}
	offset, limit := 0, 50
	if query.Has("offset") {
		offset, err = strconv.Atoi(query.Get("offset"))
		if err != nil || offset < 0 {
			reject(w, 400, "invalid_input", "Invalid offset")
			return
		}
	}
	if query.Has("limit") {
		limit, err = strconv.Atoi(query.Get("limit"))
		if err != nil || limit < 1 || limit > 200 {
			reject(w, 400, "invalid_input", "limit must be 1–200")
			return
		}
	}
	if kind == "projects" && p == "" {
		p = "_app"
	}
	out, e := s.List(r.Context(), p, kind, offset, limit)
	if e != nil {
		objectError(w, e)
		return
	}
	respond(w, 200, out)
}
