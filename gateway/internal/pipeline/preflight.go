package pipeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"github.com/zeebo/xxh3"
)

type PreflightCheck struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

type PreflightReport struct {
	Passed             bool             `json:"passed"`
	PipelineID         string           `json:"pipelineId"`
	PromptVersionID    string           `json:"promptVersionId,omitempty"`
	PromptSHA256       string           `json:"promptSha256"`
	PromptBytes        int              `json:"promptBytes"`
	StoryboardRevision int64            `json:"storyboardRevision"`
	ApprovalID         string           `json:"approvalId,omitempty"`
	CapabilityID       string           `json:"capabilityId"`
	Checks             []PreflightCheck `json:"checks"`
}

type CompileInput struct {
	RequestID          string            `json:"requestId,omitempty"`
	PipelineID         string            `json:"pipelineId"`
	PromptVersionID    string            `json:"promptVersionId,omitempty"`
	Prompt             string            `json:"prompt,omitempty"`
	ExpectedRevision   *int64            `json:"expectedRevision,omitempty"`
	StoryboardRevision *int64            `json:"storyboardRevision,omitempty"`
	CapabilityID       string            `json:"capabilityId"`
	Params             map[string]any    `json:"params,omitempty"`
	References         []string          `json:"references,omitempty"`
	Placeholders       []string          `json:"placeholders,omitempty"`
	Audio              *AudioRequirement `json:"audio,omitempty"`
	FromTaskID         string            `json:"fromTaskId,omitempty"`
	FromContractID     string            `json:"fromContractId,omitempty"`
}

type ExecutionCheck struct {
	ContractID           string
	CapabilityID         string
	Prompt               string
	Params               json.RawMessage
	References           []string
	CompositionName      string
	CompositionTypeID    string
	CompositionTypeLabel string
	BaseVersionIDs       []string
}

var (
	mustachePattern      = regexp.MustCompile(`\{\{[^{}\r\n]+\}\}`)
	dollarVarPattern     = regexp.MustCompile(`\$\{[^{}\r\n]+\}`)
	todoBracketPattern   = regexp.MustCompile(`(?i)(\[\s*(?:待填|待补充|TODO|TBD|PLACEHOLDER)[^\]\r\n]*\]|【\s*(?:待填|待补充|TODO|TBD|PLACEHOLDER)[^】\r\n]*】|<\s*(?:待填|待补充|TODO|TBD|PLACEHOLDER)[^>\r\n]*>|__TODO__)`)
	aspectRatioWhitelist = []string{"16:9", "9:16", "1:1", "4:3", "3:4", "21:9"}
)

func DetectPlaceholders(prompt string, explicit []string) []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(token string) {
		token = strings.TrimSpace(token)
		if token != "" && !seen[token] {
			seen[token] = true
			out = append(out, token)
		}
	}
	for _, item := range explicit {
		add(item)
	}
	for _, m := range mustachePattern.FindAllString(prompt, -1) {
		add(m)
	}
	for _, m := range dollarVarPattern.FindAllString(prompt, -1) {
		add(m)
	}
	for _, m := range todoBracketPattern.FindAllString(prompt, -1) {
		add(m)
	}
	return out
}

func ResolveAudioRequirement(audioIntent string, explicit *AudioRequirement) (AudioRequirement, error) {
	trimmed := strings.TrimSpace(audioIntent)
	if trimmed == "" {
		return AudioRequirement{}, invalid("Storyboard timeline audioIntent is required")
	}
	lower := strings.ToLower(trimmed)
	silent := lower == "none" || trimmed == "无" || lower == "silent" || trimmed == "静音" || lower == "mute"
	if silent {
		if explicit != nil && (explicit.RequiresAudio || explicit.RequiresNarration || explicit.RequiresMusic) {
			return AudioRequirement{}, invalid("Audio contract enables audio while storyboard audioIntent is silent")
		}
		return AudioRequirement{
			AudioIntent:       audioIntent,
			RequiresAudio:     false,
			RequiresNarration: false,
			RequiresMusic:     false,
		}, nil
	}
	inferredNarration := strings.Contains(lower, "narration") || strings.Contains(lower, "oral") || strings.Contains(lower, "speech") || strings.Contains(lower, "voice") || strings.Contains(lower, "dialogue") || strings.Contains(trimmed, "口播") || strings.Contains(trimmed, "旁白") || strings.Contains(trimmed, "人声") || strings.Contains(trimmed, "对白") || strings.Contains(trimmed, "配音")
	inferredMusic := strings.Contains(lower, "music") || strings.Contains(lower, "bgm") || strings.Contains(lower, "score") || strings.Contains(trimmed, "配乐") || strings.Contains(trimmed, "音乐") || strings.Contains(trimmed, "背景乐")
	if explicit != nil {
		req := AudioRequirement{
			AudioIntent:       audioIntent,
			RequiresAudio:     explicit.RequiresAudio || explicit.RequiresNarration || explicit.RequiresMusic,
			RequiresNarration: explicit.RequiresNarration,
			RequiresMusic:     explicit.RequiresMusic,
		}
		if !req.RequiresAudio {
			return AudioRequirement{}, invalid("Audio contract disables audio while storyboard audioIntent requires audio")
		}
		if (inferredNarration && !req.RequiresNarration) || (inferredMusic && !req.RequiresMusic) {
			return AudioRequirement{}, invalid("Audio contract omits narration or music required by storyboard audioIntent")
		}
		return req, nil
	}
	return AudioRequirement{
		AudioIntent:       audioIntent,
		RequiresAudio:     true,
		RequiresNarration: inferredNarration,
		RequiresMusic:     inferredMusic,
	}, nil
}

func parseIntParam(v any) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int64:
		return int(x), true
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) || math.Trunc(x) != x {
			return 0, false
		}
		return int(x), true
	case json.Number:
		i, err := strconv.Atoi(string(x))
		return i, err == nil
	default:
		return 0, false
	}
}

func sizeMatchesAspect(size, aspect string) bool {
	if size == aspect {
		return true
	}
	parts := strings.Split(size, "x")
	rParts := strings.Split(aspect, ":")
	if len(parts) != 2 || len(rParts) != 2 {
		return false
	}
	w, e1 := strconv.Atoi(parts[0])
	h, e2 := strconv.Atoi(parts[1])
	rw, e3 := strconv.Atoi(rParts[0])
	rh, e4 := strconv.Atoi(rParts[1])
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || w <= 0 || h <= 0 || rw <= 0 || rh <= 0 {
		return false
	}
	return w*rh == h*rw
}

func validateVideoParams(cap providers.Capability, params map[string]any) error {
	for k, v := range params {
		switch k {
		case "size":
			s, ok := v.(string)
			if !ok || s == "" {
				return invalid("Invalid video parameter: size")
			}
			if len(cap.Sizes) > 0 && !slices.Contains(cap.Sizes, s) {
				matched := false
				for _, declared := range cap.Sizes {
					if sizeMatchesAspect(declared, s) {
						matched = true
						break
					}
				}
				if !matched {
					return invalid("Unsupported video size: " + s)
				}
			}
		case "aspect_ratio":
			s, ok := v.(string)
			if !ok || !slices.Contains(aspectRatioWhitelist, s) {
				return invalid("Invalid video parameter: aspect_ratio")
			}
		case "duration":
			d, ok := parseIntParam(v)
			if !ok || d < 1 || d > 60 {
				return invalid("Invalid video parameter: duration")
			}
			if len(cap.DurationSeconds) > 0 && !slices.Contains(cap.DurationSeconds, d) {
				return invalid("Unsupported video duration: " + strconv.Itoa(d))
			}
		case "generate_audio":
			if _, ok := v.(bool); !ok {
				return invalid("Invalid video parameter: generate_audio")
			}
		case "audio_mode":
			s, ok := v.(string)
			if !ok || !slices.Contains([]string{"none", "sfx", "music", "narration", "narration+music"}, s) {
				return invalid("Invalid video parameter: audio_mode")
			}
		case "fps":
			fps, ok := parseIntParam(v)
			if !ok || !slices.Contains([]int{24, 25, 30, 60}, fps) {
				return invalid("Invalid video parameter: fps")
			}
		case "seed":
			seed, ok := parseIntParam(v)
			if !ok || seed < 0 || seed > 2147483647 {
				return invalid("Invalid video parameter: seed")
			}
		case "resolution":
			s, ok := v.(string)
			if !ok || !slices.Contains([]string{"480p", "720p", "1080p"}, s) {
				return invalid("Invalid video parameter: resolution")
			}
		case "camera_fixed", "watermark":
			if _, ok := v.(bool); !ok {
				return invalid("Invalid video parameter: " + k)
			}
		default:
			return invalid("Invalid or undeclared parameter: " + k)
		}
	}
	return nil
}

func preflightTx(ctx context.Context, tx *sql.Tx, projectID string, in CompileInput) (PromptContract, PreflightReport, error) {
	params := map[string]any{}
	for k, v := range in.Params {
		params[k] = v
	}
	refs := slices.Clone(in.References)
	if refs == nil {
		refs = []string{}
	}
	report := PreflightReport{
		Passed:       true,
		PipelineID:   in.PipelineID,
		CapabilityID: in.CapabilityID,
		Checks:       []PreflightCheck{},
	}
	var firstErr error
	record := func(name string, passed bool, code, msg string, err error) {
		if !passed {
			report.Passed = false
			if firstErr == nil && err != nil {
				firstErr = err
			}
		}
		report.Checks = append(report.Checks, PreflightCheck{
			Name:    name,
			Passed:  passed,
			Code:    code,
			Message: msg,
		})
	}

	// 1. Approval & upstream chain check
	var sbRev int64
	var sbVersionID, sbHash, sbJSON, approvalID, receiptID, intentVersionID, scriptVersionID string
	var board Storyboard
	sourceVersion, err := ApprovedSource(ctx, tx, projectID, in.PipelineID, "prompt")
	if err != nil {
		record("approval", false, "STALE_CREATIVE_APPROVAL", err.Error(), err)
	} else {
		err = tx.QueryRowContext(ctx, `SELECT s.revision,s.document_version_id,s.semantic_hash,s.content_json,a.id,a.receipt_id
			FROM storyboard_revisions s
			JOIN pipelines p ON p.id=s.pipeline_id
			JOIN approvals a ON a.pipeline_id=s.pipeline_id AND a.gate='storyboard' AND a.revision=s.revision AND a.document_version_id=s.document_version_id AND a.semantic_hash=s.semantic_hash AND a.valid=1
			WHERE p.project_id=? AND p.id=? AND s.revision=p.revision
			ORDER BY a.rowid DESC LIMIT 1`, projectID, in.PipelineID).Scan(&sbRev, &sbVersionID, &sbHash, &sbJSON, &approvalID, &receiptID)
		if err != nil {
			e := &store.DomainError{Status: 409, Code: "waiting_user", Message: "Current storyboard artifact requires human approval"}
			record("approval", false, "STALE_CREATIVE_APPROVAL", e.Error(), e)
		} else if in.StoryboardRevision != nil && *in.StoryboardRevision != sbRev {
			e := conflict()
			record("approval", false, "STALE_CREATIVE_APPROVAL", "Storyboard revision changed from expected revision", e)
		} else if json.Unmarshal([]byte(sbJSON), &board) != nil || sbVersionID != sourceVersion {
			e := conflict()
			record("approval", false, "STALE_CREATIVE_APPROVAL", "Storyboard state does not match approved source", e)
		} else {
			_ = tx.QueryRowContext(ctx, `SELECT document_version_id FROM creative_artifacts WHERE pipeline_id=? AND gate='intent' ORDER BY revision DESC LIMIT 1`, in.PipelineID).Scan(&intentVersionID)
			_ = tx.QueryRowContext(ctx, `SELECT document_version_id FROM creative_artifacts WHERE pipeline_id=? AND gate='script' ORDER BY revision DESC LIMIT 1`, in.PipelineID).Scan(&scriptVersionID)
			report.StoryboardRevision = sbRev
			report.ApprovalID = approvalID
			record("approval", true, "", fmt.Sprintf("Approved storyboard revision %d (%s)", sbRev, approvalID), nil)
		}
	}

	// 2. Prompt bytes & final_video_prompt.md version binding check
	var promptRev, promptSBRev int64
	var promptVersionID, promptResourceID, promptAssetID, promptText, promptSHA, promptSourceVersion, promptApprovalID string
	var assetSize int64
	var assetHash sql.NullString
	var assetStatus, assetPath, resDesc string
	err = tx.QueryRowContext(ctx, `SELECT r.revision,r.storyboard_revision,r.document_version_id,r.resource_id,v.asset_id,r.content,r.prompt_sha256,r.source_version_id,r.approval_id,a.size,a.full_hash,a.status,a.path,res.description
		FROM prompt_revisions r
		JOIN pipelines p ON p.id=r.pipeline_id
		JOIN versions v ON v.id=r.document_version_id
		JOIN assets a ON a.id=v.asset_id
		JOIN resources res ON res.id=r.resource_id
		WHERE p.project_id=? AND p.id=?
		ORDER BY r.revision DESC LIMIT 1`, projectID, in.PipelineID).Scan(
		&promptRev, &promptSBRev, &promptVersionID, &promptResourceID, &promptAssetID,
		&promptText, &promptSHA, &promptSourceVersion, &promptApprovalID,
		&assetSize, &assetHash, &assetStatus, &assetPath, &resDesc,
	)
	if err != nil {
		e := &store.DomainError{Status: 409, Code: "waiting_user", Message: "final_video_prompt.md has not been written for this pipeline"}
		record("prompt_bytes", false, "MISSING_PROMPT_DOCUMENT", e.Error(), e)
	} else {
		report.PromptVersionID = promptVersionID
		report.PromptSHA256 = promptSHA
		report.PromptBytes = len(promptText)
		if in.PromptVersionID != "" && in.PromptVersionID != promptVersionID {
			e := conflict()
			record("prompt_bytes", false, "STALE_PROMPT_VERSION", "Prompt version is not the current head revision", e)
		} else if in.ExpectedRevision != nil && *in.ExpectedRevision != promptRev {
			e := conflict()
			record("prompt_bytes", false, "STALE_PROMPT_VERSION", "Prompt revision does not match expectedRevision", e)
		} else if in.Prompt != "" && (in.Prompt != promptText || PromptDigest(in.Prompt) != promptSHA) {
			e := &store.DomainError{Status: 409, Code: "invalid_input", Message: "Prompt bytes differ from locked final_video_prompt.md"}
			record("prompt_bytes", false, "PROMPT_BYTES_MISMATCH", e.Error(), e)
		} else if sbRev > 0 && (promptSBRev != sbRev || promptSourceVersion != sbVersionID || promptApprovalID != approvalID) {
			e := &store.DomainError{Status: 409, Code: "waiting_user", Message: "final_video_prompt.md belongs to an older storyboard revision; rewrite prompt against current approved storyboard"}
			record("prompt_bytes", false, "STALE_PROMPT_REVISION", e.Error(), e)
		} else if validatePromptContent(promptText) != nil || PromptDigest(promptText) != promptSHA || assetStatus != "active" || assetSize != int64(len(promptText)) || !assetHash.Valid || assetHash.String != fmt.Sprintf("%016x", xxh3.HashString(promptText)) || resDesc != "final_video_prompt.md" || !strings.HasSuffix(assetPath, ".md") {
			e := invalid("Locked final_video_prompt.md bytes failed integrity verification")
			record("prompt_bytes", false, "PROMPT_INTEGRITY_FAILED", e.Error(), e)
		} else {
			record("prompt_bytes", true, "", fmt.Sprintf("Locked %d UTF-8 bytes (%s)", len(promptText), promptSHA), nil)
		}
	}

	// 3. Unresolved placeholders check
	unresolved := DetectPlaceholders(promptText, in.Placeholders)
	if len(unresolved) > 0 {
		e := invalid("Prompt contains unresolved placeholders: " + strings.Join(unresolved, ", "))
		record("placeholders", false, "UNRESOLVED_PLACEHOLDER", e.Error(), e)
	} else {
		record("placeholders", true, "", "Zero unresolved placeholders", nil)
	}

	// 4. Capability & parameter schema check
	var providerID, expectedKind string
	if strings.HasPrefix(in.CapabilityID, "image:") && len(in.CapabilityID) > 6 {
		providerID = strings.TrimPrefix(in.CapabilityID, "image:")
		expectedKind = "image"
	} else if strings.HasPrefix(in.CapabilityID, "video:") && len(in.CapabilityID) > 6 {
		providerID = strings.TrimPrefix(in.CapabilityID, "video:")
		expectedKind = "video"
	}
	var cfg providers.Config
	var cap providers.Capability
	var capLoaded bool
	if expectedKind == "" {
		e := invalid("Expected an explicit image:<id> or video:<id> capabilityId")
		record("capability_params", false, "INVALID_CAPABILITY", e.Error(), e)
	} else {
		var rawCfg string
		var deleted bool
		err := tx.QueryRowContext(ctx, `SELECT config_json,deleted FROM provider_configs WHERE id=?`, providerID).Scan(&rawCfg, &deleted)
		if err != nil || deleted || json.Unmarshal([]byte(rawCfg), &cfg) != nil || !cfg.Enabled || cfg.Kind != expectedKind {
			e := invalid("Configured capability is unavailable or disabled: " + in.CapabilityID)
			record("capability_params", false, "CAPABILITY_UNAVAILABLE", e.Error(), e)
		} else {
			capLoaded = true
			if expectedKind == "image" {
				cap = providers.ImageCapability(cfg)
				valText := promptText
				if valText == "" {
					valText = "placeholder"
				}
				if err := providers.Validate(cap, providers.GenerationInput{
					ProjectID:  projectID,
					Prompt:     valText,
					Params:     params,
					References: refs,
					Count:      1,
				}); err != nil {
					e := invalid(err.Error())
					record("capability_params", false, "INVALID_CAPABILITY_PARAM", e.Error(), e)
				} else {
					record("capability_params", true, "", "Parameters match declared image capability schema", nil)
				}
			} else {
				cap = providers.VideoCapability(cfg)
				chars := cap.Constraints.MaxPromptCharacters
				if chars == 0 {
					chars = 32000
				}
				if len(promptText) > cap.MaxPromptBytes || len(promptText) > 65536 || utf8.RuneCountInString(promptText) > chars {
					e := invalid("Prompt exceeds video capability UTF-8 byte or character limit")
					record("capability_params", false, "PROMPT_LIMIT_EXCEEDED", e.Error(), e)
				} else if err := validateVideoParams(cap, params); err != nil {
					record("capability_params", false, "INVALID_CAPABILITY_PARAM", err.Error(), err)
				} else {
					record("capability_params", true, "", "Parameters match declared video capability schema", nil)
				}
			}
		}
	}

	// 5. References availability, limits & ordering check
	resolvedRefs := make([]PromptReference, 0, len(refs))
	if !capLoaded {
		e := invalid("Cannot validate references without an active capability")
		record("references", false, "CAPABILITY_UNAVAILABLE", e.Error(), e)
	} else if len(refs) > cap.MaxReferences || len(refs) > 16 || (expectedKind == "image" && len(refs) > 0 && !cap.Edit) || (len(cap.ReferenceRoles) > 0 && len(refs) != len(cap.ReferenceRoles)) {
		e := invalid(fmt.Sprintf("Reference count %d exceeds capability limit %d", len(refs), cap.MaxReferences))
		record("references", false, "REFERENCE_LIMIT_EXCEEDED", e.Error(), e)
	} else {
		refErr := error(nil)
		for idx, assetID := range refs {
			if (store.Scope{ProjectID: "ref", Operation: "ref", RequestID: assetID}).Validate() != nil {
				refErr = invalid("Invalid reference asset ID: " + assetID)
				break
			}
			if err := store.CheckAssetFilePending(ctx, tx, assetID); err != nil {
				refErr = err
				break
			}
			var fullHash, mimeType sql.NullString
			err := tx.QueryRowContext(ctx, `SELECT a.full_hash, a.mime_type FROM assets a JOIN asset_projects ap ON ap.asset_id=a.id WHERE a.id=? AND ap.project_id=? AND a.status='active'`, assetID, projectID).Scan(&fullHash, &mimeType)
			if err != nil {
				refErr = invalid("Reference asset not active in project: " + assetID)
				break
			}
			rawMime := strings.ToLower(strings.TrimSpace(mimeType.String))
			mediaKind := ""
			if strings.HasPrefix(rawMime, "image/") {
				mediaKind = "image"
			} else if strings.HasPrefix(rawMime, "video/") {
				mediaKind = "video"
			} else {
				refErr = invalid(fmt.Sprintf("Reference asset %s has unsupported media mime_type %q", assetID, mimeType.String))
				break
			}
			var refVersionID sql.NullString
			_ = tx.QueryRowContext(ctx, `SELECT v.id FROM versions v JOIN resources r ON r.id=v.resource_id JOIN compositions c ON c.id=r.composition_id WHERE v.asset_id=? AND c.project_id=? ORDER BY v.created_at DESC, v.id DESC LIMIT 1`, assetID, projectID).Scan(&refVersionID)
			role := providers.ReferenceRole(cap, idx)
			if expectedKind == "video" && len(cap.ReferenceRoles) <= idx {
				role = fmt.Sprintf("reference_%d", idx)
			}
			resolvedRefs = append(resolvedRefs, PromptReference{
				Ordinal:   idx,
				AssetID:   assetID,
				VersionID: refVersionID.String,
				Role:      role,
				MediaKind: mediaKind,
				MIMEType:  mimeType.String,
				SHA256:    fullHash.String,
			})
		}
		if refErr != nil {
			record("references", false, "INVALID_REFERENCE", refErr.Error(), refErr)
		} else {
			record("references", true, "", fmt.Sprintf("Validated %d ordered reference(s)", len(resolvedRefs)), nil)
		}
	}

	// 6. Duration & aspect ratio consistency check
	var durationSec int
	var sizeStr, aspectStr string
	if s, ok := params["size"].(string); ok {
		sizeStr = s
	}
	if s, ok := params["aspect_ratio"].(string); ok {
		aspectStr = s
	}
	if !capLoaded || board.Timeline.DurationMS <= 0 {
		e := invalid("Cannot validate duration and aspect ratio without approved storyboard and capability")
		record("duration_aspect", false, "DURATION_ASPECT_INVALID", e.Error(), e)
	} else {
		durErr := error(nil)
		if board.Timeline.DurationMS%1000 == 0 {
			durationSec = int(board.Timeline.DurationMS / 1000)
		}
		if expectedKind == "video" || len(cap.DurationSeconds) > 0 || params["duration"] != nil {
			if board.Timeline.DurationMS%1000 != 0 || durationSec <= 0 {
				durErr = invalid("Storyboard timeline durationMs must be whole seconds for video capability")
			} else if len(cap.DurationSeconds) > 0 && !slices.Contains(cap.DurationSeconds, durationSec) {
				durErr = invalid(fmt.Sprintf("Storyboard duration %ds is not supported by capability durations %v", durationSec, cap.DurationSeconds))
			} else if rawDur, hasDur := params["duration"]; hasDur {
				d, ok := parseIntParam(rawDur)
				if !ok || d != durationSec {
					durErr = invalid(fmt.Sprintf("Parameter duration (%v) disagrees with storyboard timeline duration (%ds)", rawDur, durationSec))
				}
			}
		}
		if durErr == nil && sizeStr != "" && len(cap.Sizes) > 0 && !slices.Contains(cap.Sizes, sizeStr) {
			matched := false
			for _, declared := range cap.Sizes {
				if sizeMatchesAspect(declared, sizeStr) {
					matched = true
					break
				}
			}
			if !matched {
				durErr = invalid("Requested size " + sizeStr + " is not supported by capability")
			}
		}
		if durErr == nil && aspectStr != "" {
			if !slices.Contains(aspectRatioWhitelist, aspectStr) {
				durErr = invalid("Unsupported aspect_ratio: " + aspectStr)
			} else if sizeStr != "" && !sizeMatchesAspect(sizeStr, aspectStr) {
				durErr = invalid(fmt.Sprintf("Size %s and aspect_ratio %s disagree", sizeStr, aspectStr))
			} else if len(cap.Sizes) > 0 {
				matched := false
				for _, declared := range cap.Sizes {
					if sizeMatchesAspect(declared, aspectStr) {
						matched = true
						break
					}
				}
				if !matched {
					durErr = invalid("Requested aspect_ratio " + aspectStr + " is not supported by capability sizes")
				}
			}
		}
		if durErr != nil {
			record("duration_aspect", false, "DURATION_ASPECT_MISMATCH", durErr.Error(), durErr)
		} else {
			record("duration_aspect", true, "", fmt.Sprintf("Duration %dms and aspect/size verified", board.Timeline.DurationMS), nil)
		}
	}

	// 7. Audio / narration / music contract check
	var audioReq AudioRequirement
	if !capLoaded || strings.TrimSpace(board.Timeline.AudioIntent) == "" {
		e := invalid("Cannot validate audio contract without approved storyboard and capability")
		record("audio", false, "AUDIO_CONTRACT_INVALID", e.Error(), e)
	} else {
		req, audioErr := ResolveAudioRequirement(board.Timeline.AudioIntent, in.Audio)
		audioReq = req
		if audioErr == nil {
			genAudio, hasGenAudio := params["generate_audio"].(bool)
			audioMode, hasAudioMode := params["audio_mode"].(string)
			if !req.RequiresAudio || expectedKind == "image" {
				if (hasGenAudio && genAudio) || (hasAudioMode && audioMode != "none") {
					audioErr = invalid("Still image or silent storyboard cannot enable audio generation parameters")
				}
			} else {
				if !cap.Audio {
					audioErr = invalid("Storyboard requires audio (" + board.Timeline.AudioIntent + "), but capability does not support audio")
				} else if hasGenAudio && !genAudio {
					audioErr = invalid("Storyboard requires audio, but generate_audio=false was supplied")
				} else if hasAudioMode && audioMode == "none" {
					audioErr = invalid("Storyboard requires audio, but audio_mode=none was supplied")
				} else if req.RequiresNarration && req.RequiresMusic {
					if hasAudioMode && audioMode != "narration+music" {
						audioErr = invalid("Storyboard requires both narration and music; audio_mode must be narration+music")
					} else if !hasAudioMode && (in.Audio == nil || !in.Audio.RequiresNarration || !in.Audio.RequiresMusic) {
						audioErr = invalid("Combined narration and music cannot rely on generic generate_audio=true alone; bind explicit narration+music contract or audio_mode=narration+music")
					}
				} else if req.RequiresNarration && hasAudioMode && audioMode != "narration" && audioMode != "narration+music" {
					audioErr = invalid("Storyboard requires narration; audio_mode=" + audioMode + " cannot satisfy narration")
				} else if req.RequiresMusic && hasAudioMode && audioMode != "music" && audioMode != "narration+music" {
					audioErr = invalid("Storyboard requires music; audio_mode=" + audioMode + " cannot satisfy music")
				}
			}
			if hasAudioMode && audioMode != "" {
				audioReq.Mode = audioMode
			} else if !audioReq.RequiresAudio {
				audioReq.Mode = "none"
			} else if audioReq.RequiresNarration && audioReq.RequiresMusic {
				audioReq.Mode = "narration+music"
			} else if audioReq.RequiresNarration {
				audioReq.Mode = "narration"
			} else if audioReq.RequiresMusic {
				audioReq.Mode = "music"
			} else {
				audioReq.Mode = "audio"
			}
		}
		if audioErr != nil {
			record("audio", false, "AUDIO_CONTRACT_MISMATCH", audioErr.Error(), audioErr)
		} else {
			record("audio", true, "", fmt.Sprintf("Audio requirement verified (%s)", audioReq.AudioIntent), nil)
		}
	}

	contract := PromptContract{
		ProjectID:           projectID,
		PipelineID:          in.PipelineID,
		Revision:            sbRev,
		PromptRevision:      promptRev,
		Status:              "ready",
		PromptResourceID:    promptResourceID,
		PromptVersionID:     promptVersionID,
		PromptAssetID:       promptAssetID,
		PromptFileName:      "final_video_prompt.md",
		Prompt:              promptText,
		PromptSHA256:        promptSHA,
		PromptBytes:         len(promptText),
		StoryboardVersionID: sbVersionID,
		StoryboardRevision:  sbRev,
		StoryboardHash:      sbHash,
		ApprovalID:          approvalID,
		QuestionReceiptID:   receiptID,
		IntentVersionID:     intentVersionID,
		ScriptVersionID:     scriptVersionID,
		CapabilityID:        in.CapabilityID,
		ProviderID:          providerID,
		ModelID:             cfg.ModelID,
		DurationMS:          board.Timeline.DurationMS,
		DurationSeconds:     durationSec,
		AspectRatio:         aspectStr,
		Size:                sizeStr,
		Count:               1,
		Params:              params,
		References:          resolvedRefs,
		Placeholders:        unresolved,
		Audio:               audioReq,
		Preflight:           report,
	}
	return contract, report, firstErr
}

func Preflight(ctx context.Context, db *sql.DB, projectID string, in CompileInput) (PreflightReport, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return PreflightReport{}, err
	}
	defer tx.Rollback()
	_, report, preflightErr := preflightTx(ctx, tx, projectID, in)
	return report, preflightErr
}

func compileTx(ctx context.Context, tx *sql.Tx, projectID string, in CompileInput) (PromptContract, PreflightReport, error) {
	contract, report, err := preflightTx(ctx, tx, projectID, in)
	if err != nil {
		return PromptContract{}, report, err
	}
	now := time.Now().UnixMilli()
	contract.ID = uuid.NewString()
	contract.CreatedAt = now
	latestRec := LatestPipelineRecovery(ctx, tx, projectID, in.PipelineID)
	fromTaskID := strings.TrimSpace(in.FromTaskID)
	fromContractID := strings.TrimSpace(in.FromContractID)
	if fromTaskID == "" && latestRec != nil && latestRec.SelectedAction == "modify_params" {
		fromTaskID = latestRec.FromTaskID
		if fromContractID == "" {
			fromContractID = latestRec.FromContractID
		}
	}
	if fromTaskID != "" || fromContractID != "" {
		reqID := in.RequestID
		receiptID := ""
		stage := FailureParameter
		action := "modify_params"
		if latestRec != nil {
			if reqID == "" {
				reqID = latestRec.RequestID
			}
			receiptID = latestRec.ReceiptID
			if latestRec.Stage != "" {
				stage = latestRec.Stage
			}
			if latestRec.SelectedAction != "" {
				action = latestRec.SelectedAction
			}
		}
		if receiptID == "" && reqID != "" {
			receiptID = "pipeline.compile." + in.PipelineID + ":" + reqID
		}
		contract.Recovery = &RecoveryLink{
			Stage:          stage,
			Action:         action,
			RequestID:      reqID,
			ReceiptID:      receiptID,
			FromTaskID:     fromTaskID,
			FromContractID: fromContractID,
			CreatedAt:      now,
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE prompt_contracts SET status='invalidated' WHERE pipeline_id=? AND status IN ('pending','ready')`, in.PipelineID); err != nil {
		return PromptContract{}, report, err
	}
	encoded, err := json.Marshal(contract)
	if err != nil {
		return PromptContract{}, report, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO prompt_contracts(id,pipeline_id,revision,document_version_id,content_json,status,prompt_revision,prompt_sha256,approval_id,created_at) VALUES(?,?,?,?,?,'ready',?,?,?,?)`,
		contract.ID, contract.PipelineID, contract.StoryboardRevision, contract.PromptVersionID, string(encoded),
		contract.PromptRevision, contract.PromptSHA256, contract.ApprovalID, now); err != nil {
		return PromptContract{}, report, err
	}
	for _, ref := range contract.References {
		if ref.VersionID != "" && ref.VersionID != contract.PromptVersionID {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO version_dependencies(version_id,source_version_id,kind) VALUES(?,?,?)`,
				contract.PromptVersionID, ref.VersionID, fmt.Sprintf("reference:%d", ref.Ordinal)); err != nil {
				return PromptContract{}, report, err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE pipelines SET prompt_status='ready' WHERE id=? AND project_id=?`, contract.PipelineID, projectID); err != nil {
		return PromptContract{}, report, err
	}
	return contract, report, nil
}

func Compile(ctx context.Context, db *sql.DB, projectID string, in CompileInput) (PromptContract, PreflightReport, error) {
	if (store.Scope{ProjectID: projectID, Operation: "pipeline.compile", RequestID: "check"}).Validate() != nil || projectID == "_app" {
		return PromptContract{}, PreflightReport{}, invalid("Invalid project_id")
	}
	if in.RequestID != "" {
		scope := store.Scope{ProjectID: projectID, Operation: "pipeline.compile." + in.PipelineID, RequestID: in.RequestID}
		if err := scope.Validate(); err != nil {
			return PromptContract{}, PreflightReport{}, invalid("Invalid requestId")
		}
		rawIn, _ := json.Marshal(in)
		canonical, err := store.CanonicalJSON(rawIn)
		if err != nil {
			return PromptContract{}, PreflightReport{}, invalid("Invalid compile input")
		}
		var outReport PreflightReport
		rawOut, err := store.WriteRequest(ctx, db, scope, canonical, func(tx *sql.Tx) ([]byte, error) {
			c, rep, e := compileTx(ctx, tx, projectID, in)
			outReport = rep
			if e != nil {
				return nil, e
			}
			return json.Marshal(c)
		})
		if err != nil {
			return PromptContract{}, outReport, err
		}
		var contract PromptContract
		if err := json.Unmarshal(rawOut, &contract); err != nil {
			return PromptContract{}, outReport, err
		}
		return contract, contract.Preflight, nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return PromptContract{}, PreflightReport{}, err
	}
	defer tx.Rollback()
	contract, report, err := compileTx(ctx, tx, projectID, in)
	if err != nil {
		return PromptContract{}, report, err
	}
	if err := tx.Commit(); err != nil {
		return PromptContract{}, report, err
	}
	return contract, report, nil
}

func isCompositionPipelineBound(ctx context.Context, tx *sql.Tx, projectID, compID string) (bool, error) {
	if compID == "" {
		return false, nil
	}
	var n int
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM resources r
		WHERE r.composition_id=? AND (
			EXISTS(SELECT 1 FROM pipelines p WHERE p.project_id=? AND p.resource_id=r.id) OR
			EXISTS(SELECT 1 FROM creative_artifacts a JOIN pipelines p ON p.id=a.pipeline_id WHERE p.project_id=? AND a.resource_id=r.id) OR
			EXISTS(SELECT 1 FROM prompt_revisions pr JOIN pipelines p ON p.id=pr.pipeline_id WHERE p.project_id=? AND pr.resource_id=r.id)
		)`, compID, projectID, projectID, projectID).Scan(&n)
	return n > 0, err
}

func isDeliveredMediaRevisionEdit(ctx context.Context, tx *sql.Tx, projectID string, in ExecutionCheck) bool {
	if len(in.BaseVersionIDs) == 0 || len(in.References) == 0 || strings.HasPrefix(in.CapabilityID, "video:") {
		return false
	}
	for _, baseVerID := range in.BaseVersionIDs {
		if baseVerID == "" {
			return false
		}
		var docCount int
		if err := tx.QueryRowContext(ctx, `SELECT
			(SELECT count(*) FROM storyboard_revisions WHERE document_version_id=?) +
			(SELECT count(*) FROM prompt_revisions WHERE document_version_id=?) +
			(SELECT count(*) FROM creative_artifacts WHERE document_version_id=?)`,
			baseVerID, baseVerID, baseVerID).Scan(&docCount); err != nil || docCount > 0 {
			return false
		}
		var deliveredCount int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM versions v
			JOIN resources r ON r.id=v.resource_id
			JOIN compositions c ON c.id=r.composition_id
			WHERE v.id=? AND c.project_id=? AND EXISTS(
				SELECT 1 FROM versions v2
				JOIN task_outputs o ON o.version_id=v2.id
				JOIN tasks t ON t.id=o.task_id
				JOIN prompt_contracts pc ON pc.task_id=t.id
				WHERE v2.resource_id=v.resource_id AND o.state='delivered' AND pc.status='submitted'
			)`, baseVerID, projectID).Scan(&deliveredCount); err != nil || deliveredCount == 0 {
			return false
		}
	}
	return true
}

func hasTargetPipeline(ctx context.Context, tx *sql.Tx, projectID string, in ExecutionCheck) (bool, error) {
	var totalPipelines int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pipelines WHERE project_id=?`, projectID).Scan(&totalPipelines); err != nil || totalPipelines == 0 {
		return false, err
	}
	if strings.HasPrefix(in.CapabilityID, "video:") {
		return true, nil
	}
	if in.ContractID == "" && isDeliveredMediaRevisionEdit(ctx, tx, projectID, in) {
		return false, nil
	}
	for _, baseVerID := range in.BaseVersionIDs {
		if baseVerID == "" {
			continue
		}
		var compID string
		if err := tx.QueryRowContext(ctx, `SELECT r.composition_id FROM versions v JOIN resources r ON r.id=v.resource_id JOIN compositions c ON c.id=r.composition_id WHERE v.id=? AND c.project_id=?`, baseVerID, projectID).Scan(&compID); err == nil {
			if bound, err := isCompositionPipelineBound(ctx, tx, projectID, compID); err != nil || bound {
				return bound, err
			}
		}
	}
	if in.CompositionName != "" {
		rows, err := tx.QueryContext(ctx, `SELECT id FROM compositions WHERE project_id=? AND name=?`, projectID, in.CompositionName)
		if err != nil {
			return false, err
		}
		var compIDs []string
		for rows.Next() {
			var cid string
			if err := rows.Scan(&cid); err == nil {
				compIDs = append(compIDs, cid)
			}
		}
		rows.Close()
		for _, cid := range compIDs {
			if bound, err := isCompositionPipelineBound(ctx, tx, projectID, cid); err != nil || bound {
				return bound, err
			}
		}
	}
	return false, nil
}

// AuthorizeExecution enforces that any media generation targeting an active pipeline composition
// (or any video generation in a pipeline project, or any request carrying a contractId)
// matches a ready/submitted PromptContract byte-for-byte and passes live Preflight in the same SQLite transaction.
func AuthorizeExecution(ctx context.Context, tx *sql.Tx, projectID, expectedTaskID string, in ExecutionCheck) (*PromptContract, error) {
	if in.CapabilityID == "text:configured-llm" && in.ContractID == "" {
		return nil, nil
	}
	hasPipe, err := hasTargetPipeline(ctx, tx, projectID, in)
	if err != nil {
		return nil, err
	}
	if in.ContractID == "" {
		if hasPipe {
			return nil, &store.DomainError{Status: 409, Code: "waiting_user", Message: "Pipeline composition requires a ready prompt contractId; omitting contractId is not permitted"}
		}
		return nil, nil
	}
	var pipelineID, docVersionID, rawJSON, status, sha string
	var sbRev, promptRev int64
	var approvalID, boundTask sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT c.pipeline_id,c.revision,c.prompt_revision,c.document_version_id,c.content_json,c.status,c.prompt_sha256,c.approval_id,c.task_id
		FROM prompt_contracts c JOIN pipelines p ON p.id=c.pipeline_id
		WHERE c.id=? AND p.project_id=?`, in.ContractID, projectID).Scan(
		&pipelineID, &sbRev, &promptRev, &docVersionID, &rawJSON, &status, &sha, &approvalID, &boundTask,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &store.DomainError{Status: 404, Code: "not_found", Message: "Prompt contract not found in project"}
	}
	if err != nil {
		return nil, err
	}
	if status == "invalidated" || status == "pending" {
		return nil, &store.DomainError{Status: 409, Code: "waiting_user", Message: "Prompt contract is not ready or was invalidated by a creative revision"}
	}
	if expectedTaskID == "" {
		if status != "ready" {
			return nil, &store.DomainError{Status: 409, Code: "invalid_input", Message: "Prompt contract was already submitted"}
		}
		if boundTask.Valid && boundTask.String != "" {
			return nil, &store.DomainError{Status: 409, Code: "invalid_input", Message: "Prompt contract is already bound to a task"}
		}
	} else {
		if !boundTask.Valid || boundTask.String != expectedTaskID {
			return nil, &store.DomainError{Status: 409, Code: "invalid_input", Message: "Prompt contract is not bound to this task"}
		}
	}
	var contract PromptContract
	if err := json.Unmarshal([]byte(rawJSON), &contract); err != nil || contract.ID == "" {
		return nil, invalid("Stored prompt contract is malformed")
	}
	contract.Status = status
	contract.TaskID = boundTask.String
	if in.Prompt != contract.Prompt || PromptDigest(in.Prompt) != contract.PromptSHA256 || PromptDigest(in.Prompt) != sha {
		return nil, &store.DomainError{Status: 409, Code: "invalid_input", Message: "Execution prompt bytes do not match locked prompt contract"}
	}
	if in.CapabilityID != contract.CapabilityID {
		return nil, &store.DomainError{Status: 409, Code: "invalid_input", Message: "Execution capabilityId does not match locked prompt contract"}
	}
	inParams := in.Params
	if len(inParams) == 0 {
		inParams = []byte(`{}`)
	}
	canonIn, err := store.CanonicalJSON(inParams)
	if err != nil {
		return nil, invalid("Execution params must be a JSON object")
	}
	contractParamsRaw, _ := json.Marshal(contract.Params)
	if len(contract.Params) == 0 {
		contractParamsRaw = []byte(`{}`)
	}
	canonContract, err := store.CanonicalJSON(contractParamsRaw)
	if err != nil || string(canonIn) != string(canonContract) {
		return nil, &store.DomainError{Status: 409, Code: "invalid_input", Message: "Execution params do not match locked prompt contract"}
	}
	if len(in.References) != len(contract.References) {
		return nil, &store.DomainError{Status: 409, Code: "invalid_input", Message: "Execution references do not match locked prompt contract"}
	}
	refIDs := make([]string, len(contract.References))
	for i, r := range contract.References {
		if in.References[i] != r.AssetID {
			return nil, &store.DomainError{Status: 409, Code: "invalid_input", Message: "Execution reference order or assetId does not match locked prompt contract"}
		}
		refIDs[i] = r.AssetID
	}
	audioCopy := contract.Audio
	_, _, preflightErr := preflightTx(ctx, tx, projectID, CompileInput{
		PipelineID:         contract.PipelineID,
		PromptVersionID:    contract.PromptVersionID,
		Prompt:             contract.Prompt,
		ExpectedRevision:   &contract.PromptRevision,
		StoryboardRevision: &contract.StoryboardRevision,
		CapabilityID:       contract.CapabilityID,
		Params:             contract.Params,
		References:         refIDs,
		Placeholders:       contract.Placeholders,
		Audio:              &audioCopy,
	})
	if preflightErr != nil {
		return nil, preflightErr
	}
	return &contract, nil
}

func BindExecutionTask(ctx context.Context, tx *sql.Tx, projectID, taskID string, in ExecutionCheck) (*PromptContract, error) {
	contract, err := AuthorizeExecution(ctx, tx, projectID, "", in)
	if err != nil || contract == nil {
		return contract, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE prompt_contracts SET task_id=? WHERE id=? AND status='ready' AND (task_id IS NULL OR task_id='')`, taskID, contract.ID)
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil || n != 1 {
		return nil, conflict()
	}
	contract.TaskID = taskID
	return contract, nil
}

func ClaimExecution(ctx context.Context, tx *sql.Tx, projectID, taskID string, in ExecutionCheck) (bool, error) {
	if in.CapabilityID == "text:configured-llm" && in.ContractID == "" {
		return false, nil
	}
	hasPipe, err := hasTargetPipeline(ctx, tx, projectID, in)
	if err != nil {
		return false, err
	}
	if in.ContractID == "" && !hasPipe {
		return false, nil
	}
	contract, err := AuthorizeExecution(ctx, tx, projectID, taskID, in)
	if err != nil {
		return true, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE prompt_contracts SET status='submitted' WHERE id=? AND task_id=? AND status IN ('ready','submitted')`, contract.ID, taskID)
	if err != nil {
		return true, err
	}
	n, err := res.RowsAffected()
	if err != nil || n != 1 {
		return true, &store.DomainError{Status: 409, Code: "waiting_user", Message: "Prompt contract could not be claimed for submission"}
	}
	return true, nil
}
