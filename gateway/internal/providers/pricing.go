package providers

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"math/big"
	"time"
)

type Quote struct {
	Basis            string `json:"basis,omitempty"`
	QuoteID          string `json:"quote_id"`
	Currency         string `json:"currency"`
	EstimatedAmount  int64  `json:"estimated_amount"`
	MinorUnitScale   int    `json:"minor_unit_scale"`
	EstimatedCredits int64  `json:"estimated_credits"`
	ExpiresAt        int64  `json:"expires_at"`
	InputHash        string `json:"input_hash"`
	Source           string `json:"source"`
	Version          string `json:"version"`
}
type QuotedInput struct {
	Quote    Quote           `json:"quote"`
	Input    GenerationInput `json:"input"`
	Provider FrozenProvider  `json:"provider"`
}

func Estimate(in GenerationInput, f FrozenProvider) (Quote, error) {
	var q Quote
	if e := ValidateConfig(f.Config); e != nil {
		return q, e
	}
	var validate error
	switch f.Config.Kind {
	case "llm":
		validate = ValidateText(in, f)
	case "video":
		validate = Validate(VideoCapability(f.Config), in)
	default:
		validate = Validate(ImageCapability(f.Config), in)
	}
	if e := validate; e != nil {
		return q, e
	}
	p := f.Config.Pricing
	validUnit := p != nil && ((f.Config.Kind == "image" && p.Unit == "image") || (f.Config.Kind == "video" && (p.Unit == "second" || p.Unit == "video")) || (f.Config.Kind == "llm" && p.Unit == "token"))
	if !validUnit || p.EffectiveAt > time.Now().Unix() {
		return q, invalid("A current explicit price is required; unknown price cannot be free")
	}
	adapter := "openai-images-m4.3-v1"
	if f.Config.Kind == "llm" {
		adapter = "opencode-text-m5.5-v1"
	} else if f.Config.Kind == "video" {
		adapter = "ark-video-m7.1-v1"
	}
	raw, e := json.Marshal(struct {
		Input          GenerationInput `json:"input"`
		Provider       FrozenProvider  `json:"provider"`
		AdapterVersion string          `json:"adapter_version"`
	}{in, f, adapter})
	if e != nil {
		return q, invalid("Invalid quote input")
	}
	raw, e = store.CanonicalJSON(raw)
	if e != nil {
		return q, e
	}
	hash := sha256.Sum256(raw)
	amount := p.UnitAmount * int64(in.Count)
	var videoSeconds int64 = 1
	if f.Config.Kind == "llm" {
		amount = p.UnitAmount * int64(f.Config.TextContext)
	} else if f.Config.Kind == "video" {
		if rawDur, hasDur := in.Params["duration"]; hasDur {
			if d, ok := intParam(rawDur); ok && d > 0 {
				videoSeconds = d
			}
		} else if len(f.Config.Capability.DurationSeconds) > 0 {
			videoSeconds = int64(f.Config.Capability.DurationSeconds[0])
		}
		if p.Unit == "second" {
			amount = p.UnitAmount * videoSeconds * int64(in.Count)
		}
	}
	credits := new(big.Int).Mul(big.NewInt(amount), big.NewInt(p.CreditNumerator))
	credits.Add(credits, big.NewInt(p.CreditDenominator-1))
	credits.Div(credits, big.NewInt(p.CreditDenominator))
	if !credits.IsInt64() {
		return q, invalid("Credit estimate overflow")
	}
	q = Quote{QuoteID: uuid.NewString(), Currency: p.Currency, EstimatedAmount: amount, MinorUnitScale: p.MinorUnitScale, EstimatedCredits: credits.Int64(), ExpiresAt: time.Now().Add(5 * time.Minute).UnixMilli(), InputHash: hex.EncodeToString(hash[:]), Source: p.Source, Version: p.Version}
	if f.Config.Kind == "llm" {
		q.Basis = fmt.Sprintf("保守估算：上下文上限 %d token × 声明的单 token 最高价；输出最多 %d token。不是供应商实测费用。", f.Config.TextContext, f.Config.TextOutput)
	} else if f.Config.Kind == "video" {
		if p.Unit == "second" {
			q.Basis = fmt.Sprintf("用户声明视频单秒价估算：%d 秒 × %d 最小货币单位/秒。不是供应商实测账单。", videoSeconds, p.UnitAmount)
		} else {
			q.Basis = fmt.Sprintf("用户声明单条视频价估算：%d 条 × %d 最小货币单位/条。不是供应商实测账单。", in.Count, p.UnitAmount)
		}
	}
	return q, nil
}
func (s *Service) ReplayQuote(ctx context.Context, p, id string, wire []byte) (Quote, bool, error) {
	var q Quote
	if e := (store.Scope{ProjectID: p, Operation: "generate.estimate", RequestID: id}).Validate(); e != nil || p == "_app" {
		return q, false, invalid("Invalid quote scope")
	}
	canonical, e := store.CanonicalJSON(wire)
	if e != nil {
		return q, false, e
	}
	var old, state, response string
	e = s.DB.QueryRowContext(ctx, "SELECT input_json,state,coalesce(response_json,'') FROM write_requests WHERE project_id=? AND operation='generate.estimate' AND request_id=?", p, id).Scan(&old, &state, &response)
	if errors.Is(e, sql.ErrNoRows) {
		return q, false, nil
	}
	if e != nil {
		return q, false, e
	}
	if old != string(canonical) {
		return q, true, store.ErrRequestConflict
	}
	if state != "completed" {
		return q, true, store.ErrRequestPending
	}
	e = json.Unmarshal([]byte(response), &q)
	return q, true, e
}
func (s *Service) SaveQuote(ctx context.Context, p, id string, wire []byte, in GenerationInput, f FrozenProvider) (Quote, error) {
	if q, found, e := s.ReplayQuote(ctx, p, id, wire); found || e != nil {
		return q, e
	}
	q, e := Estimate(in, f)
	if e != nil {
		return q, e
	}
	canonical, e := store.CanonicalJSON(wire)
	if e != nil {
		return q, e
	}
	frozen, e := json.Marshal(QuotedInput{q, in, f})
	if e != nil {
		return q, e
	}
	out, e := store.WriteRequest(ctx, s.DB, store.Scope{ProjectID: p, Operation: "generate.estimate", RequestID: id}, canonical, func(tx *sql.Tx) ([]byte, error) {
		_, e := tx.ExecContext(ctx, "INSERT INTO price_quotes(id,project_id,input_hash,frozen_json,source,version,expires_at,created_at)VALUES(?,?,?,?,?,?,?,?)", q.QuoteID, p, q.InputHash, string(frozen), q.Source, q.Version, q.ExpiresAt, time.Now().UnixMilli())
		if e != nil {
			return nil, e
		}
		return json.Marshal(q)
	})
	if e == nil {
		e = json.Unmarshal(out, &q)
	}
	return q, e
}
func (s *Service) QuoteBinding(ctx context.Context, p, id string) (QuotedInput, error) {
	var q QuotedInput
	var raw string
	e := s.DB.QueryRowContext(ctx, "SELECT frozen_json FROM price_quotes WHERE project_id=? AND id=?", p, id).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return q, ErrNotFound
	}
	if e != nil {
		return q, e
	}
	e = json.Unmarshal([]byte(raw), &q)
	return q, e
}
