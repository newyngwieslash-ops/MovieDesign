package mcp

import (
	"context"
	"encoding/json"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/agent"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"io"
	"strings"
)

type searchInput struct {
	Query  string `json:"query"`
	Offset int    `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}
type Voice struct {
	ID          string `json:"voice_id"`
	Provider    string `json:"provider"`
	Description string `json:"description"`
	Language    string `json:"language"`
	Gender      string `json:"gender,omitempty"`
	SampleAudio string `json:"sample_audio,omitempty"`
}

func (s *Server) catalog(server *sdk.Server, scope agent.ToolScope) {
	for _, kind := range []string{"image", "voice"} {
		add(s, server, scope, "design_"+kind+"_search", "Search real owned local assets or user-uploaded voice-catalog.json entries. Local catalog adaptation; no web search/download, no invented IDs. Explicit empty coverage when no catalog.", func(ctx context.Context, i searchInput, _ []byte) (any, error) {
			if i.Limit == 0 {
				i.Limit = 30
			}
			if i.Offset < 0 || i.Limit < 1 || i.Limit > 100 || len(i.Query) > 512 {
				return nil, invalid("Invalid search pagination/query")
			}
			if s.services.Files == nil {
				return nil, invalid("Workspace unavailable")
			}
			items := []any{}
			query := strings.ToLower(i.Query)
			rows, e := s.services.Objects.DB.QueryContext(ctx, `SELECT a.id,a.name,a.path,coalesce(a.mime_type,'') FROM assets a JOIN asset_projects ap ON ap.asset_id=a.id WHERE ap.project_id=? AND a.status='active' ORDER BY a.id`, scope.ProjectID)
			if e != nil {
				return nil, e
			}
			type asset struct{ id, name, path, mime string }
			assets := []asset{}
			for rows.Next() {
				var a asset
				if e = rows.Scan(&a.id, &a.name, &a.path, &a.mime); e != nil {
					rows.Close()
					return nil, e
				}
				assets = append(assets, a)
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return nil, e
			}
			for _, a := range assets {
				if kind == "image" {
					if strings.HasPrefix(a.mime, "image/") && strings.Contains(strings.ToLower(a.name), query) {
						items = append(items, map[string]any{"assetId": a.id, "name": a.name, "source": "project_asset", "mime": a.mime})
					}
					continue
				}
				if a.name != "voice-catalog.json" {
					continue
				}
				f, e := s.services.Files.OpenContent(ctx, scope.ProjectID, a.path)
				if e != nil {
					return nil, e
				}
				b, e := io.ReadAll(io.LimitReader(f, 1<<20+1))
				f.Close()
				if e != nil || len(b) > 1<<20 {
					return nil, invalid("Voice catalog exceeds 1 MiB")
				}
				var entries []Voice
				if json.Unmarshal(b, &entries) != nil || len(entries) > 10000 {
					return nil, invalid("Invalid user voice catalog")
				}
				for _, v := range entries {
					if strings.TrimSpace(v.ID) == "" || strings.TrimSpace(v.Provider) == "" || len(v.ID) > 256 || len(v.Description) > 4096 {
						return nil, invalid("Voice catalog requires real provider and voice_id")
					}
					if strings.Contains(strings.ToLower(v.ID+" "+v.Description+" "+v.Language), query) {
						items = append(items, map[string]any{"voice": v, "source": "user_registered_catalog", "catalogAssetId": a.id})
					}
				}
			}
			start := min(i.Offset, len(items))
			end := min(start+i.Limit, len(items))
			return map[string]any{"items": items[start:end], "coverage": store.Coverage{Total: len(items), NextOffset: end, Exhausted: end == len(items)}, "source": "local_project_catalog", "remoteSearched": false}, nil
		})
	}
}
