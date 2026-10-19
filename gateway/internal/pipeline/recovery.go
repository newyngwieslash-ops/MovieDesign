package pipeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
)

type FailureStage string

const (
	FailureParameter FailureStage = "parameter"
	FailureSemantic  FailureStage = "semantic"
	FailureUpstream  FailureStage = "upstream"
	FailureUnknown   FailureStage = "unknown"
)

type FailureProblem struct {
	Status       string `json:"status,omitempty"`
	Source       string `json:"source"`
	Code         string `json:"code"`
	Message      string `json:"message"`
	UpstreamCode string `json:"upstreamCode,omitempty"`
	Retryable    bool   `json:"retryable"`
}

type RecoveryLink struct {
	Stage          FailureStage `json:"stage"`
	Action         string       `json:"action"`
	RequestID      string       `json:"requestId"`
	ReceiptID      string       `json:"receiptId"`
	FromTaskID     string       `json:"fromTaskId"`
	FromContractID string       `json:"fromContractId,omitempty"`
	CreatedAt      int64        `json:"createdAt"`
}

type RecoveryState struct {
	Stage               FailureStage `json:"stage"`
	RecommendedAction   string       `json:"recommendedAction"`
	SelectedAction      string       `json:"selectedAction,omitempty"`
	TargetStage         string       `json:"targetStage,omitempty"`
	RequestID           string       `json:"requestId,omitempty"`
	ReceiptID           string       `json:"receiptId,omitempty"`
	FromTaskID          string       `json:"fromTaskId"`
	FromContractID      string       `json:"fromContractId,omitempty"`
	PipelineID          string       `json:"pipelineId,omitempty"`
	StoryboardRevision  int64        `json:"storyboardRevision,omitempty"`
	StoryboardVersionID string       `json:"storyboardVersionId,omitempty"`
	PromptRevision      int64        `json:"promptRevision,omitempty"`
	PromptVersionID     string       `json:"promptVersionId,omitempty"`
	ApprovalID          string       `json:"approvalId,omitempty"`
	UpdatedAt           int64        `json:"updatedAt"`
}

// ClassifyFailure maps a task/preflight/provider problem to a recommended return stage
// (parameter | semantic | upstream | unknown). It is purely advisory/routing metadata and
// never triggers an automatic retry.
func ClassifyFailure(problem FailureProblem) FailureStage {
	status := strings.ToLower(strings.TrimSpace(problem.Status))
	code := strings.ToUpper(strings.TrimSpace(problem.Code))
	upCode := strings.ToLower(strings.TrimSpace(problem.UpstreamCode))
	source := strings.ToLower(strings.TrimSpace(problem.Source))

	if status == "unknown" {
		return FailureUnknown
	}
	switch code {
	case "PROVIDER_OUTCOME_UNKNOWN", "OUTCOME_UNKNOWN", "UNKNOWN_ACCEPTANCE", "SUBMISSION_OUTCOME_UNKNOWN":
		return FailureUnknown
	}
	switch upCode {
	case "unknown", "outcome_unknown", "timeout_after_accept", "submission_outcome_unknown":
		return FailureUnknown
	}
	if status == "" && code == "" && upCode == "" && source == "" {
		return FailureUnknown
	}

	switch code {
	case "STALE_CREATIVE_APPROVAL", "MISSING_APPROVAL", "STALE_APPROVAL", "CREATIVE_APPROVAL_INVALID",
		"UNRESOLVED_PLACEHOLDER", "PLACEHOLDER_UNRESOLVED",
		"CONTENT_POLICY_REJECTED", "SAFETY_REJECTED", "SEMANTIC_CONFLICT", "NARRATIVE_INCONSISTENT",
		"AUDIO_INTENT_CONFLICT", "CONTENT_POLICY_VIOLATION", "MODERATION_BLOCKED", "SENSITIVE_CONTENT",
		"STORYBOARD_INVALID":
		return FailureSemantic
	}
	switch upCode {
	case "content_policy_violation", "safety_rejected", "moderation_blocked", "sensitive_content",
		"prompt_safety_blocked", "semantic_violation", "narrative_conflict", "storyboard_invalid":
		return FailureSemantic
	}

	switch code {
	case "INVALID_PARAMETER", "PARAMETER_INVALID", "PARAMETER_OUT_OF_RANGE",
		"UNSUPPORTED_SIZE", "INVALID_SIZE", "UNSUPPORTED_ASPECT_RATIO", "INVALID_ASPECT_RATIO",
		"UNSUPPORTED_DURATION", "INVALID_DURATION", "DURATION_ASPECT_MISMATCH", "DURATION_MISMATCH", "ASPECT_RATIO_MISMATCH",
		"CAPABILITY_MISMATCH", "CAPABILITY_UNAVAILABLE", "UNSUPPORTED_RESOLUTION", "MODEL_LIMIT_EXCEEDED",
		"AUDIO_CAPABILITY_UNAVAILABLE", "AUDIO_CONTRACT_MISMATCH", "AUDIO_CONTRACT_INVALID", "AUDIO_PARAMETER_MISMATCH", "UNSUPPORTED_AUDIO_MODE",
		"PROMPT_BYTE_LIMIT_EXCEEDED", "PROMPT_LIMIT_EXCEEDED",
		"REFERENCE_LIMIT_EXCEEDED", "REFERENCE_INVALID", "MISSING_REFERENCE_ASSET", "INVALID_REFERENCE_MEDIA",
		"QUOTE_EXPIRED", "PRICE_MISMATCH":
		return FailureParameter
	}
	switch upCode {
	case "invalid_parameter", "parameter_invalid", "parameter_out_of_range",
		"invalid_size", "unsupported_size", "invalid_duration", "unsupported_duration",
		"invalid_aspect_ratio", "unsupported_aspect_ratio", "unsupported_resolution",
		"model_limit_exceeded", "unsupported_audio_mode", "reference_limit_exceeded",
		"prompt_byte_limit_exceeded", "quote_expired":
		return FailureParameter
	}

	if source == "delivery" {
		return FailureUpstream
	}
	switch code {
	case "COLLECTION_INTERRUPTED", "COLLECTION_IO", "DELIVERY_PENDING", "RESULT_AWAITING_DELIVERY",
		"PROVIDER_RATE_LIMITED", "PROVIDER_UNAVAILABLE", "PROVIDER_TIMEOUT", "PROVIDER_REJECTED",
		"UPSTREAM_TEMP_FAILURE", "UPSTREAM_ERROR", "NETWORK_ERROR", "RATE_LIMIT_EXCEEDED",
		"SERVER_ERROR", "SERVICE_UNAVAILABLE", "TIMEOUT", "HTTP_429", "HTTP_500", "HTTP_502", "HTTP_503", "HTTP_504":
		return FailureUpstream
	}
	switch upCode {
	case "rate_limit_exceeded", "server_error", "service_unavailable", "timeout",
		"upstream_unavailable", "connection_refused", "bad_gateway", "gateway_timeout":
		return FailureUpstream
	}
	if problem.Retryable {
		return FailureUpstream
	}
	msg := strings.ToLower(problem.Message)
	if strings.Contains(msg, "safety") || strings.Contains(msg, "policy") || strings.Contains(msg, "moderation") || strings.Contains(msg, "storyboard") || strings.Contains(msg, "approval") {
		return FailureSemantic
	}
	if strings.Contains(msg, "parameter") || strings.Contains(msg, "duration") || strings.Contains(msg, "aspect_ratio") || strings.Contains(msg, "size") || strings.Contains(msg, "resolution") || strings.Contains(msg, "audio_mode") {
		return FailureParameter
	}
	return FailureUpstream
}

func RecommendedAction(stage FailureStage, retryable bool) string {
	switch stage {
	case FailureUnknown:
		return "query"
	case FailureParameter:
		return "modify_params"
	case FailureSemantic:
		return "modify_storyboard"
	default:
		if retryable {
			return "retry"
		}
		return "modify_params"
	}
}

func RecoveryActions(stage FailureStage, hasPipeline bool, source string, retryable bool) []string {
	if strings.EqualFold(source, "delivery") {
		if retryable {
			return []string{"retry"}
		}
		return []string{}
	}
	switch stage {
	case FailureUnknown:
		return []string{"query", "cancel"}
	case FailureParameter:
		if hasPipeline {
			return []string{"modify_params", "modify_storyboard"}
		}
		return []string{"modify_params"}
	case FailureSemantic:
		if hasPipeline {
			return []string{"modify_storyboard"}
		}
		return []string{}
	default:
		if hasPipeline {
			if retryable {
				return []string{"retry", "modify_params", "modify_storyboard"}
			}
			return []string{"modify_params", "modify_storyboard"}
		}
		if retryable {
			return []string{"retry"}
		}
		return []string{}
	}
}

func BuildTaskRecoveryTx(ctx context.Context, tx *sql.Tx, projectID, taskID, contractID string, problem FailureProblem, previous *RecoveryState) (*RecoveryState, []string) {
	stage := ClassifyFailure(problem)
	retryable := problem.Retryable && stage == FailureUpstream
	hasPipeline := strings.TrimSpace(contractID) != ""
	actions := RecoveryActions(stage, hasPipeline, problem.Source, retryable)

	state := &RecoveryState{
		Stage:             stage,
		RecommendedAction: RecommendedAction(stage, retryable),
		FromTaskID:        taskID,
		FromContractID:    contractID,
		UpdatedAt:         time.Now().UnixMilli(),
	}
	if contractID != "" && tx != nil {
		var rawJSON string
		if err := tx.QueryRowContext(ctx, `SELECT c.content_json FROM prompt_contracts c JOIN pipelines p ON p.id=c.pipeline_id WHERE c.id=? AND p.project_id=?`, contractID, projectID).Scan(&rawJSON); err == nil {
			var c PromptContract
			if json.Unmarshal([]byte(rawJSON), &c) == nil {
				state.PipelineID = c.PipelineID
				state.StoryboardRevision = c.StoryboardRevision
				state.StoryboardVersionID = c.StoryboardVersionID
				state.PromptRevision = c.PromptRevision
				state.PromptVersionID = c.PromptVersionID
				state.ApprovalID = c.ApprovalID
			}
		}
	}
	if previous != nil && previous.SelectedAction != "" {
		state.SelectedAction = previous.SelectedAction
		state.TargetStage = previous.TargetStage
		state.RequestID = previous.RequestID
		state.ReceiptID = previous.ReceiptID
	}
	return state, actions
}

func ApplyRecoveryActionTx(ctx context.Context, tx *sql.Tx, projectID, taskID, contractID, requestID, action string, current *RecoveryState) (*RecoveryState, []string, error) {
	if strings.TrimSpace(requestID) == "" {
		return nil, nil, invalid("Recovery requires requestId")
	}
	switch action {
	case "modify_params", "modify_storyboard", "retry", "query":
	default:
		return nil, nil, invalid("Unsupported recovery action: " + action)
	}
	now := time.Now().UnixMilli()
	state := &RecoveryState{
		FromTaskID:     taskID,
		FromContractID: contractID,
		UpdatedAt:      now,
	}
	if current != nil {
		*state = *current
		state.UpdatedAt = now
	}
	if state.FromTaskID == "" {
		state.FromTaskID = taskID
	}
	if state.FromContractID == "" {
		state.FromContractID = contractID
	}
	if contractID != "" && state.PipelineID == "" {
		var rawJSON string
		err := tx.QueryRowContext(ctx, `SELECT c.content_json FROM prompt_contracts c JOIN pipelines p ON p.id=c.pipeline_id WHERE c.id=? AND p.project_id=?`, contractID, projectID).Scan(&rawJSON)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, &store.DomainError{Status: 404, Code: "not_found", Message: "Prompt contract not found in project"}
		}
		if err != nil {
			return nil, nil, err
		}
		var c PromptContract
		if json.Unmarshal([]byte(rawJSON), &c) == nil {
			state.PipelineID = c.PipelineID
			state.StoryboardRevision = c.StoryboardRevision
			state.StoryboardVersionID = c.StoryboardVersionID
			state.PromptRevision = c.PromptRevision
			state.PromptVersionID = c.PromptVersionID
			state.ApprovalID = c.ApprovalID
		}
	}
	state.SelectedAction = action
	state.RequestID = requestID
	state.ReceiptID = "tasks.resolve." + taskID + ":" + requestID

	var nextActions []string
	switch action {
	case "modify_params":
		if state.Stage == "" {
			state.Stage = FailureParameter
		}
		state.TargetStage = string(FailureParameter)
		if state.PipelineID != "" {
			if _, err := tx.ExecContext(ctx, `UPDATE pipelines SET prompt_status='pending' WHERE id=? AND project_id=?`, state.PipelineID, projectID); err != nil {
				return nil, nil, err
			}
			nextActions = []string{"modify_storyboard"}
		} else {
			nextActions = []string{}
		}
	case "modify_storyboard":
		if state.Stage == "" {
			state.Stage = FailureSemantic
		}
		state.TargetStage = string(FailureSemantic)
		if state.PipelineID != "" {
			if _, err := tx.ExecContext(ctx, `UPDATE prompt_contracts SET status='invalidated' WHERE pipeline_id=? AND status IN ('pending','ready')`, state.PipelineID); err != nil {
				return nil, nil, err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE pipelines SET prompt_status='pending' WHERE id=? AND project_id=?`, state.PipelineID, projectID); err != nil {
				return nil, nil, err
			}
		}
		nextActions = []string{}
	case "retry":
		if state.Stage == "" {
			state.Stage = FailureUpstream
		}
		state.TargetStage = "billing"
		nextActions = []string{"cancel"}
	case "query":
		state.Stage = FailureUnknown
		state.TargetStage = string(FailureUnknown)
		nextActions = []string{"query", "cancel"}
	}
	return state, nextActions, nil
}

func LatestPipelineRecovery(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, projectID, pipelineID string) *RecoveryState {
	if projectID == "" || pipelineID == "" || q == nil {
		return nil
	}
	var raw string
	err := q.QueryRowContext(ctx, `SELECT json_extract(t.view_json,'$.recovery')
		FROM tasks t
		JOIN prompt_contracts c ON c.id=json_extract(t.frozen_input_json,'$.contract_id')
		WHERE t.project_id=? AND c.pipeline_id=? AND json_extract(t.view_json,'$.recovery') IS NOT NULL
		ORDER BY t.updated_at DESC, t.rowid DESC LIMIT 1`, projectID, pipelineID).Scan(&raw)
	if err != nil || raw == "" || raw == "null" {
		return nil
	}
	var state RecoveryState
	if json.Unmarshal([]byte(raw), &state) != nil || state.FromTaskID == "" {
		return nil
	}
	return &state
}
