package agent

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"strings"
	"sync"
)

type View struct {
	Config *LLMConfig `json:"config"`
	HasKey bool       `json:"has_key"`
}
type Receipt struct {
	Settings View                   `json:"settings"`
	Probe    *providers.ProbeResult `json:"probe,omitempty"`
}
type Settings struct {
	DB      *sql.DB
	Secrets providers.SecretStore
	mu      sync.Mutex
}

func (s *Settings) Read(ctx context.Context) (View, string, error) {
	var raw, version string
	err := s.DB.QueryRowContext(ctx, `SELECT config_json,credential_version FROM llm_settings WHERE id=1`).Scan(&raw, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return View{}, "", nil
	}
	if err != nil {
		return View{}, "", err
	}
	var c LLMConfig
	if json.Unmarshal([]byte(raw), &c) != nil {
		return View{}, "", errors.New("LLM settings unavailable")
	}
	return View{Config: &c, HasKey: version != ""}, version, nil
}
func (s *Settings) Receipt(ctx context.Context, id string) (Receipt, error) {
	var v Receipt
	if (store.Scope{ProjectID: "_app", Operation: "llm_settings", RequestID: id}).Validate() != nil {
		return v, ErrInvalid
	}
	var raw string
	err := s.DB.QueryRowContext(ctx, `SELECT response_json FROM write_requests WHERE project_id='_app' AND operation='llm_settings' AND request_id=? AND state='completed'`, id).Scan(&raw)
	if err != nil {
		return v, err
	}
	return v, json.Unmarshal([]byte(raw), &v)
}
func (s *Settings) Execute(ctx context.Context, raw []byte) (Receipt, error) {
	var result Receipt
	m, err := store.DecodeValue[struct {
		RequestID string     `json:"requestId"`
		Action    string     `json:"action"`
		Config    *LLMConfig `json:"config,omitempty"`
		Key       *string    `json:"key,omitempty"`
		RemoveKey bool       `json:"remove_key,omitempty"`
	}](raw)
	scope := store.Scope{ProjectID: "_app", Operation: "llm_settings", RequestID: m.RequestID}
	if err != nil || scope.Validate() != nil {
		return result, ErrInvalid
	}
	if m.Action == "save" {
		if m.Config == nil || Validate(*m.Config) != nil {
			return result, ErrInvalid
		}
		if m.Key != nil && (m.RemoveKey || len(*m.Key) == 0 || len(*m.Key) > 8192 || strings.IndexFunc(*m.Key, func(r rune) bool { return r < 33 || r > 126 }) >= 0 || strings.ContainsAny(*m.Key, "{}")) {
			return result, ErrInvalid
		}
	} else if m.Action == "probe" {
		var fields map[string]any
		_ = json.Unmarshal(raw, &fields)
		if len(fields) != 2 {
			return result, ErrInvalid
		}
	} else {
		return result, ErrInvalid
	}
	canonical, _ := store.CanonicalJSON(raw)
	digest := sha256.Sum256(canonical)
	input, _ := json.Marshal(map[string]string{"sha256": hex.EncodeToString(digest[:])})
	// ponytail: serialize app settings/probes; per-connection locks only if multiple active LLM profiles are added.
	s.mu.Lock()
	defer s.mu.Unlock()
	var previous, response string
	err = s.DB.QueryRowContext(ctx, `SELECT input_json,response_json FROM write_requests WHERE project_id='_app' AND operation='llm_settings' AND request_id=?`, m.RequestID).Scan(&previous, &response)
	if err == nil {
		if previous != string(input) {
			return result, store.ErrRequestConflict
		}
		return result, json.Unmarshal([]byte(response), &result)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	v, version, err := s.Read(ctx)
	if err != nil {
		return result, err
	}
	if m.Action == "save" {
		v.Config = m.Config
		if m.RemoveKey {
			version = ""
		}
		if m.Key != nil {
			version = uuid.NewString()
			if err = s.Secrets.Put(version, *m.Key); err != nil {
				return result, err
			}
		}
		v.HasKey = version != ""
	}
	if v.Config == nil {
		return result, ErrInvalid
	}
	result.Settings = v
	if m.Action == "probe" {
		p := providers.ProbeResult{LocalValid: true, Message: "配置校验通过；此协议只做本地校验，未远端验证或执行生成。"}
		if v.Config.Protocol != "anthropic" {
			svc := providers.NewService(s.DB, s.Secrets)
			p = svc.Probe(ctx, providers.FrozenProvider{Config: providers.Config{Protocol: "openai-images", BaseURL: v.Config.BaseURL, ModelID: v.Config.ModelID}, CredentialVersion: version})
		}
		result.Probe = &p
	}
	encoded, err := store.WriteRequest(ctx, s.DB, scope, input, func(tx *sql.Tx) ([]byte, error) {
		if m.Action == "save" {
			config, _ := json.Marshal(v.Config)
			if _, err := tx.ExecContext(ctx, `INSERT INTO llm_settings(id,config_json,credential_version) VALUES(1,?,?) ON CONFLICT(id) DO UPDATE SET config_json=excluded.config_json,credential_version=excluded.credential_version`, string(config), version); err != nil {
				return nil, err
			}
		}
		return json.Marshal(result)
	})
	if err != nil {
		return result, err
	}
	return result, json.Unmarshal(encoded, &result)
}
