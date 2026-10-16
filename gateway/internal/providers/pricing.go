package providers

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"math/big"
	"time"
)

type Quote struct {
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
	if e := Validate(ImageCapability(f.Config), in); e != nil {
		return q, e
	}
	p := f.Config.Pricing
	if p == nil || p.Unit != "image" || p.EffectiveAt > time.Now().Unix() {
		return q, invalid("A current explicit image price is required; unknown price cannot be free")
	}
	raw, e := json.Marshal(struct {
		Input          GenerationInput `json:"input"`
		Provider       FrozenProvider  `json:"provider"`
		AdapterVersion string          `json:"adapter_version"`
	}{in, f, "openai-images-m4.3-v1"})
	if e != nil {
		return q, invalid("Invalid quote input")
	}
	raw, e = store.CanonicalJSON(raw)
	if e != nil {
		return q, e
	}
	hash := sha256.Sum256(raw)
	amount := p.UnitAmount * int64(in.Count)
	credits := new(big.Int).Mul(big.NewInt(amount), big.NewInt(p.CreditNumerator))
	credits.Add(credits, big.NewInt(p.CreditDenominator-1))
	credits.Div(credits, big.NewInt(p.CreditDenominator))
	if !credits.IsInt64() {
		return q, invalid("Credit estimate overflow")
	}
	return Quote{QuoteID: uuid.NewString(), Currency: p.Currency, EstimatedAmount: amount, MinorUnitScale: p.MinorUnitScale, EstimatedCredits: credits.Int64(), ExpiresAt: time.Now().Add(5 * time.Minute).UnixMilli(), InputHash: hex.EncodeToString(hash[:]), Source: p.Source, Version: p.Version}, nil
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
