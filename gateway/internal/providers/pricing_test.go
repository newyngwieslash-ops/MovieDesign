package providers

import (
	"context"
	"encoding/json"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"testing"
	"time"
)

func TestQuoteIntegerBindingAndReplay(t *testing.T) {
	s, _ := testService(t)
	ctx := context.Background()
	p, e := (store.Objects{DB: s.DB}).CreateProject(ctx, "quote-project", "Quote")
	if e != nil {
		t.Fatal(e)
	}
	f := FrozenProvider{Config: validConfig("one", "http://127.0.0.1:11434/v1"), CredentialVersion: "version-one"}
	f.Config.Capability.MaxCount = 3
	f.Config.Pricing.UnitAmount = 7
	f.Config.Pricing.CreditNumerator = 2
	f.Config.Pricing.CreditDenominator = 3
	in := GenerationInput{Prompt: " x\n", Params: map[string]any{}, Count: 2, Output: json.RawMessage(`{"output_key":"a"}`)}
	q, e := Estimate(in, f)
	if e != nil {
		t.Fatal(e)
	}
	if q.EstimatedAmount != 14 || q.EstimatedCredits != 10 {
		t.Fatalf("integer rational roundup: %+v", q)
	}
	q2, _ := Estimate(in, f)
	if q.InputHash != q2.InputHash {
		t.Fatal("unstable hash")
	}
	in.Prompt = "x\n"
	q2, _ = Estimate(in, f)
	if q.InputHash == q2.InputHash {
		t.Fatal("prompt rewritten")
	}
	in.Prompt = " x\n"
	wire := []byte(`{"requestId":"quote-one","prompt":" x\\n"}`)
	q, e = s.SaveQuote(ctx, p.ID, "quote-one", wire, in, f)
	if e != nil {
		t.Fatal(e)
	}
	f.Config.Pricing.UnitAmount = 99
	saved, found, e := s.ReplayQuote(ctx, p.ID, "quote-one", wire)
	if e != nil || !found || saved.QuoteID != q.QuoteID || saved.EstimatedAmount != 14 {
		t.Fatal("quote replay repriced", saved, e)
	}
	if _, _, e = s.ReplayQuote(ctx, p.ID, "quote-one", []byte(`{"requestId":"quote-one","prompt":null}`)); e == nil {
		t.Fatal("changed receipt input accepted")
	}
	if _, e = s.QuoteBinding(ctx, p.ID, q.QuoteID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.QuoteBinding(ctx, "foreign", q.QuoteID); e == nil {
		t.Fatal("foreign quote exposed")
	}
	f.Config.Pricing = nil
	if _, e = Estimate(in, f); e == nil {
		t.Fatal("unknown pricing made free")
	}
}

func TestQuoteEffectiveAtSeconds(t *testing.T) {
	f := FrozenProvider{Config: validConfig("one", "http://127.0.0.1:11434/v1")}
	in := GenerationInput{Prompt: "x", Count: 1, Params: map[string]any{}}
	f.Config.Pricing.EffectiveAt = time.Now().Add(time.Hour).Unix()
	if _, e := Estimate(in, f); e == nil {
		t.Fatal("future seconds-based price accepted")
	}
	f.Config.Pricing.EffectiveAt = time.Now().Unix()
	if _, e := Estimate(in, f); e != nil {
		t.Fatal(e)
	}
}
func TestQuoteHashBindsFrozenIntent(t *testing.T) {
	f := FrozenProvider{Config: validConfig("one", "http://127.0.0.1:11434/v1"), CredentialVersion: "one"}
	f.Config.Capability.Edit = true
	f.Config.Capability.MaxReferences = 2
	in := GenerationInput{ProjectID: "project", Prompt: "x", Count: 1, Params: map[string]any{}, References: []string{"asset-a", "asset-b"}, Output: json.RawMessage(`{"key":"a"}`)}
	original, e := Estimate(in, f)
	if e != nil {
		t.Fatal(e)
	}
	for _, change := range []string{"order", "output", "credential", "model", "price", "capability", "bytes"} {
		nextInput := in
		nextProvider := f
		price := *f.Config.Pricing
		nextProvider.Config.Pricing = &price
		switch change {
		case "order":
			nextInput.References = []string{"asset-b", "asset-a"}
		case "output":
			nextInput.Output = json.RawMessage(`{"key":"b"}`)
		case "credential":
			nextProvider.CredentialVersion = "two"
		case "model":
			nextProvider.Config.ModelID = "other"
		case "price":
			price.Version = "v2"
		case "capability":
			nextProvider.Config.Capability.MaxCount = 2
		case "bytes":
			nextInput.Assets = []ReferenceAsset{{SHA256: "changed"}}
		}
		q, e := Estimate(nextInput, nextProvider)
		if e != nil {
			t.Fatal(e)
		}
		if q.InputHash == original.InputHash {
			t.Fatal("unbound quote intent", change)
		}
	}
}
