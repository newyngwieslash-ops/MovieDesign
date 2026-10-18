package providers

import (
	"encoding/json"
	"testing"
)

func TestTextQuoteUsesFrozenTokenCeilingAndRejectsUnknownFields(t *testing.T) {
	f, e := TextProvider(json.RawMessage(`{"protocol":"openai-compatible","base_url":"http://127.0.0.1:11434","model_id":"local","context":4096,"output":512,"pricing":{"currency":"USD","unit":"token","unit_amount":1,"minor_unit_scale":6,"credit_numerator":1,"credit_denominator":1,"source":"user declaration","version":"1","effective_at":0}}`), "")
	if e != nil {
		t.Fatal(e)
	}
	in := GenerationInput{Prompt: " exact ", Params: map[string]any{}, Count: 1}
	q, e := Estimate(in, f)
	if e != nil || q.EstimatedAmount != 4096 || q.Basis == "" {
		t.Fatal(q, e)
	}
	in.Params["temperature"] = 1
	if _, e = Estimate(in, f); e == nil {
		t.Fatal("undeclared native option accepted")
	}
}
