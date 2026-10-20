package mcp

import (
	"context"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/agent"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/delivery"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/pipeline"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/questions"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/workspace"
)

type readInput struct {
	VersionID string `json:"versionId,omitempty"`
	FilePath  string `json:"file_path,omitempty"`
	Offset    int    `json:"offset,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}
type analyseInput struct {
	References []string `json:"references"`
	Type       string   `json:"type"`
}
type rulesInput struct {
	Action           string  `json:"action"`
	RequestID        string  `json:"requestId,omitempty"`
	Content          string  `json:"content,omitempty"`
	ExpectedRevision *string `json:"expectedRevision,omitempty"`
}

func (s *Server) documents(server *sdk.Server, scope agent.ToolScope) {
	add(s, server, scope, "design_read", "Read managed md/txt/srt/ass or provider knowledge; exactly one versionId/file_path. Zero-based line offset; coverage describes all lines. No PDF/Word/contentRevision.", func(ctx context.Context, i readInput, _ []byte) (any, error) {
		if (i.VersionID == "") == (i.FilePath == "") {
			return nil, invalid("Exactly one versionId or file_path is required")
		}
		if i.Limit == 0 {
			i.Limit = 200
		}
		if i.FilePath == "knowledge/providers/openai-images.md" {
			return workspace.TextPage(providers.ImageKnowledgeCard, i.Offset, i.Limit)
		}
		if i.FilePath == "knowledge/providers/text.md" {
			return workspace.TextPage(providers.TextKnowledgeCard, i.Offset, i.Limit)
		}
		if i.FilePath == "knowledge/providers/ark-video.md" {
			return workspace.TextPage(providers.VideoKnowledgeCard, i.Offset, i.Limit)
		}
		if s.services.Files == nil {
			return nil, invalid("Workspace unavailable")
		}
		v := i.FilePath
		if i.VersionID != "" {
			var e error
			v, e = s.services.Files.VersionPath(ctx, scope.ProjectID, i.VersionID)
			if e != nil {
				return nil, e
			}
		}
		return s.services.Files.ReadText(ctx, scope.ProjectID, v, i.Offset, i.Limit)
	})
	add(s, server, scope, "design_analyse_media", "Objective image/video/audio dimensions, duration and audio-track count only; semantic/both modes are unsupported.", func(ctx context.Context, i analyseInput, _ []byte) (any, error) {
		if i.Type != "metadata" || len(i.References) < 1 || len(i.References) > 16 {
			return nil, invalid("Only metadata analysis is supported")
		}
		if s.services.Files == nil {
			return nil, invalid("Workspace unavailable")
		}
		items := []any{}
		for _, id := range i.References {
			v, e := s.services.Files.AssetPath(ctx, scope.ProjectID, id)
			if e != nil {
				return nil, e
			}
			metadata, e := s.services.Files.AnalyseMedia(ctx, scope.ProjectID, v, s.services.FFprobe)
			if e != nil {
				return nil, e
			}
			items = append(items, map[string]any{"assetId": id, "metadata": metadata})
		}
		return map[string]any{"items": items}, nil
	})
	add(s, server, scope, "design_document_write", "Create/replace/atomic exact-anchored patch of md/txt/srt/ass through shared Delivery. For intent/script use typedDocument {kind:intent|script,pipelineId,expectedRevision,content}; script requires approved current intent, storyboard requires approved current script. Successful typed writes publish a version-bound human question in the current session; output {document,approvalProposal,question?} reports document commit separately from proposal. Human approval is never inferred from text. For semantic storyboard_review.md use typedDocument {kind:storyboard,pipelineId,expectedRevision,storyboard:{panels,shots,timeline}}; all times integer milliseconds, Shots are narrative chapters, not generated clips. Typed writes render deterministic Markdown; optional complete Markdown content must match typed JSON. Semantic revisions require replace/new_version/baseVersionId of current revision and preserve Resource. Text never grants approval.", func(ctx context.Context, i delivery.DocumentRequest, raw []byte) (any, error) {
		if s.services.Delivery == nil {
			return nil, invalid("Delivery unavailable")
		}
		r, e := s.services.Delivery.WriteDocument(ctx, scope.ProjectID, raw)
		if e != nil || i.TypedDocument == nil || r.Delivery.Status != "completed" {
			return r, e
		}
		if i.TypedDocument.Kind == "prompt" {
			if s.services.Questions != nil && s.services.Questions.DB != nil {
				if view, err := pipeline.ReadPromptVersion(ctx, s.services.Questions.DB, scope.ProjectID, r.Delivery.Versions[0].VersionID); err == nil {
					return map[string]any{"document": r, "prompt": view, "contract": view.Contract}, nil
				}
			}
			return map[string]any{"document": r}, nil
		}
		if s.services.Questions == nil {
			return map[string]any{"document": r, "approvalProposal": "pending", "reason": "Question service unavailable; retry the original document request"}, nil
		}
		b, e := pipeline.BindingForVersion(ctx, s.services.Questions.DB, scope.ProjectID, r.Delivery.Versions[0].VersionID)
		if e != nil {
			return map[string]any{"document": r, "approvalProposal": "blocked", "reason": e.Error()}, nil
		}
		q, e := s.services.Questions.ProposeCreative(ctx, scope.ProjectID, scope.SessionID, i.RequestID, b)
		if e != nil {
			return map[string]any{"document": r, "approvalProposal": "pending", "reason": e.Error()}, nil
		}
		return map[string]any{"document": r, "approvalProposal": "proposed", "question": q}, nil
	})
	add(s, server, scope, "design_project_rules", "Read long-term rules or propose a complete replacement only when user explicitly requested persistence. Save returns a human card; only its human receipt commits. Read/merge current opaque revision first.", func(ctx context.Context, i rulesInput, _ []byte) (any, error) {
		if s.services.Questions == nil {
			return nil, invalid("Questions unavailable")
		}
		if i.Action == "read" {
			if i.RequestID != "" || i.Content != "" || i.ExpectedRevision != nil {
				return nil, invalid("Read accepts action only")
			}
			return s.services.Questions.ReadRules(ctx, scope.ProjectID)
		}
		if i.Action != "save" {
			return nil, invalid("Expected read or save")
		}
		return s.services.Questions.ProposeRules(ctx, scope.ProjectID, scope.SessionID, i.RequestID, questions.RulesChange{Content: i.Content, ExpectedRevision: i.ExpectedRevision})
	})
}
