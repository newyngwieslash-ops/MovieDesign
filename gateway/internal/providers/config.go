package providers

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/workspace"
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
var ErrInvalid = errors.New("invalid provider input")
var ErrNotFound = errors.New("provider not found")
var ErrUnavailable = errors.New("provider storage unavailable")

func invalid(message string) error { return errors.Join(ErrInvalid, errors.New(message)) }

// Capability contains declarations, never probe-verified generation facts.
type Capability struct {
	ID              string          `json:"id,omitempty"`
	Operation       string          `json:"operation,omitempty"`
	Protocol        string          `json:"protocol,omitempty"`
	ModelID         string          `json:"model_id,omitempty"`
	KnowledgeCard   string          `json:"knowledgeCard,omitempty"`
	ParamSchema     json.RawMessage `json:"paramSchema,omitempty"`
	Constraints     Constraints     `json:"constraints,omitempty"`
	ReferenceRoles  []string        `json:"reference_roles,omitempty"`
	MaxPromptBytes  int             `json:"max_prompt_bytes"`
	MaxCount        int             `json:"max_count"`
	MaxReferences   int             `json:"max_references"`
	Sizes           []string        `json:"sizes"`
	Edit            bool            `json:"edit"`
	DurationSeconds []int           `json:"duration_seconds"`
	Audio           bool            `json:"audio"`
}

// Normalize legacy/omitted/null optional arrays at every JSON output boundary,
// including stored receipts, so API consumers always receive arrays.
func (c Capability) MarshalJSON() ([]byte, error) {
	type plain Capability
	if c.Sizes == nil {
		c.Sizes = []string{}
	}
	if c.DurationSeconds == nil {
		c.DurationSeconds = []int{}
	}
	return json.Marshal(plain(c))
}

type Pricing struct {
	Currency          string `json:"currency"`
	Unit              string `json:"unit"`
	UnitAmount        int64  `json:"unit_amount"`
	MinorUnitScale    int    `json:"minor_unit_scale"`
	CreditNumerator   int64  `json:"credit_numerator"`
	CreditDenominator int64  `json:"credit_denominator"`
	Source            string `json:"source"`
	Version           string `json:"version"`
	EffectiveAt       int64  `json:"effective_at"`
}
type Headers struct {
	Organization string `json:"OpenAI-Organization,omitempty"`
	Project      string `json:"OpenAI-Project,omitempty"`
}
type Config struct {
	TextContext int        `json:"text_context,omitempty"`
	TextOutput  int        `json:"text_output,omitempty"`
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Kind        string     `json:"kind"`
	Protocol    string     `json:"protocol"`
	BaseURL     string     `json:"base_url"`
	ModelID     string     `json:"model_id"`
	Enabled     bool       `json:"enabled"`
	Headers     Headers    `json:"headers"`
	Capability  Capability `json:"capability"`
	Pricing     *Pricing   `json:"pricing"`
}
type View struct {
	Config     Config `json:"config"`
	HasKey     bool   `json:"has_key"`
	DefaultFor string `json:"default_for"`
}
type FrozenProvider struct {
	Config            Config `json:"config"`
	CredentialVersion string `json:"credential_version"`
}
type ProbeResult struct {
	LocalValid   bool   `json:"local_valid"`
	RemoteTested bool   `json:"remote_tested"`
	ModelsListed bool   `json:"models_listed"`
	ModelFound   bool   `json:"model_found"`
	HTTPStatus   int    `json:"http_status,omitempty"`
	Message      string `json:"message"`
}
type Receipt struct {
	Provider *View        `json:"provider,omitempty"`
	Probe    *ProbeResult `json:"probe,omitempty"`
	Deleted  bool         `json:"deleted,omitempty"`
}
type mutation struct {
	RequestID string  `json:"requestId"`
	Action    string  `json:"action"`
	ID        string  `json:"id,omitempty"`
	Config    *Config `json:"config,omitempty"`
	Key       *string `json:"key,omitempty"`
	RemoveKey bool    `json:"remove_key,omitempty"`
}
type Service struct {
	Files   *workspace.Service
	DB      *sql.DB
	Secrets SecretStore
	mu      sync.Mutex
}

func NewService(db *sql.DB, secrets SecretStore) *Service { return &Service{DB: db, Secrets: secrets} }

func ValidateConfig(c Config) error {
	if !identifier.MatchString(c.ID) || len(c.ID) > 128 || strings.TrimSpace(c.Name) == "" || len(c.Name) > 160 || strings.TrimSpace(c.ModelID) == "" || len(c.ModelID) > 256 {
		return invalid("Invalid identity, name or model")
	}
	if c.Kind != "image" && c.Kind != "video" && c.Kind != "llm" || c.Kind == "image" && c.Protocol != "openai-images" || c.Kind == "video" && c.Protocol != "ark-video" || c.Kind == "llm" && (c.Protocol != "openai-compatible" && c.Protocol != "openai" && c.Protocol != "anthropic" || c.TextContext < 1024 || c.TextContext > 2000000 || c.TextOutput < 1 || c.TextOutput >= c.TextContext) {
		return invalid("Unsupported kind/protocol; LLM is configured through opencode")
	}
	if err := ValidateBaseURL(c.BaseURL); err != nil {
		return invalid("Invalid Base URL")
	}
	for _, v := range []string{c.Headers.Organization, c.Headers.Project} {
		if len(v) > 200 || strings.ContainsAny(v, "\r\n") || strings.IndexFunc(v, func(r rune) bool { return r < 32 || r > 126 }) >= 0 {
			return invalid("Invalid additional header")
		}
	}
	if c.Protocol != "openai-images" && (c.Headers.Organization != "" || c.Headers.Project != "") {
		return invalid("Headers are unsupported by this protocol")
	}
	cap := c.Capability
	if cap.MaxPromptBytes < 1 || cap.MaxPromptBytes > 65536 || cap.MaxCount < 1 || cap.MaxCount > 10 || cap.MaxReferences < 0 || cap.MaxReferences > 16 {
		return invalid("Invalid capability limits")
	}
	if len(cap.Sizes) > 32 || len(cap.DurationSeconds) > 32 {
		return invalid("Too many declared sizes/durations")
	}
	for _, size := range cap.Sizes {
		if !regexp.MustCompile(`^[1-9][0-9]{0,4}x[1-9][0-9]{0,4}$`).MatchString(size) {
			return invalid("Invalid declared size")
		}
	}
	for _, duration := range cap.DurationSeconds {
		if duration < 1 || duration > 60 {
			return invalid("Invalid declared duration")
		}
	}
	if c.Kind == "image" && (len(cap.DurationSeconds) > 0 || cap.Audio) || c.Kind == "video" && (cap.Edit || cap.MaxCount != 1) {
		return invalid("Capability not supported by protocol template")
	}
	if err := validateDeclaration(c); c.Kind != "llm" && err != nil {
		return err
	}
	if p := c.Pricing; p != nil {
		if !regexp.MustCompile(`^[A-Z]{3}$`).MatchString(p.Currency) || p.MinorUnitScale < 0 || p.MinorUnitScale > 6 || p.UnitAmount < 0 || p.UnitAmount > 1e12 || p.CreditNumerator < 0 || p.CreditNumerator > 1e9 || p.CreditDenominator < 1 || p.CreditDenominator > 1e9 || p.EffectiveAt < 0 || strings.TrimSpace(p.Source) == "" || len(p.Source) > 200 || strings.TrimSpace(p.Version) == "" || len(p.Version) > 100 {
			return invalid("Invalid pricing declaration")
		}
		if c.Kind == "image" && p.Unit != "image" || c.Kind == "video" && p.Unit != "second" || c.Kind == "llm" && p.Unit != "token" {
			return invalid("Unsupported price unit")
		}
	}
	return nil
}
func readConfig(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string) (FrozenProvider, bool, error) {
	var f FrozenProvider
	var raw string
	var deleted bool
	err := q.QueryRowContext(ctx, `SELECT config_json,credential_version,deleted FROM provider_configs WHERE id=?`, id).Scan(&raw, &f.CredentialVersion, &deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return f, false, ErrNotFound
	}
	if err != nil {
		return f, false, ErrUnavailable
	}
	if json.Unmarshal([]byte(raw), &f.Config) != nil {
		return f, false, ErrUnavailable
	}
	return f, deleted, nil
}
func (s *Service) List(ctx context.Context) ([]View, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT p.config_json,p.credential_version,COALESCE(d.kind,'') FROM provider_configs p LEFT JOIN provider_defaults d ON d.provider_id=p.id WHERE p.deleted=0 ORDER BY p.id`)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer rows.Close()
	result := []View{}
	for rows.Next() {
		var raw, version string
		var v View
		if rows.Scan(&raw, &version, &v.DefaultFor) != nil || json.Unmarshal([]byte(raw), &v.Config) != nil {
			return nil, ErrUnavailable
		}
		v.HasKey = version != ""
		result = append(result, v)
	}
	return result, rows.Err()
}
func (s *Service) Resolve(id string) (FrozenProvider, error) {
	f, deleted, err := readConfig(context.Background(), s.DB, id)
	if err != nil {
		return f, err
	}
	if deleted || !f.Config.Enabled {
		return f, invalid("Provider is disabled")
	}
	return f, nil
}
func (s *Service) ResolveDefault(kind string) (FrozenProvider, error) {
	var id string
	err := s.DB.QueryRow(`SELECT provider_id FROM provider_defaults WHERE kind=?`, kind).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return FrozenProvider{}, ErrNotFound
	}
	if err != nil {
		return FrozenProvider{}, ErrUnavailable
	}
	return s.Resolve(id)
}
func (s *Service) Credential(f FrozenProvider) (string, error) {
	if f.CredentialVersion == "" {
		return "", nil
	}
	return s.Secrets.Get(f.CredentialVersion)
}

func (s *Service) Execute(ctx context.Context, raw []byte) (Receipt, error) {
	var empty Receipt
	m, err := store.DecodeValue[mutation](raw)
	if err != nil {
		return empty, invalid("Invalid provider request schema")
	}
	scope := store.Scope{ProjectID: "_app", Operation: "providers", RequestID: m.RequestID}
	if err = scope.Validate(); err != nil {
		return empty, invalid("Invalid requestId")
	}
	// Scope all actions together and hash the COMPLETE strict original input, including
	// omitted vs supplied fields and secret changes, without journaling any plaintext.
	canonical, err := store.CanonicalJSON(raw)
	if err != nil {
		return empty, invalid("Invalid JSON")
	}
	hash := sha256.Sum256(canonical)
	input, _ := json.Marshal(map[string]string{"sha256": hex.EncodeToString(hash[:])})
	if m.Action == "create" || m.Action == "update" {
		if m.Config == nil || m.ID != "" {
			return empty, invalid("Save requires config only")
		}
		if err = ValidateConfig(*m.Config); err != nil {
			return empty, err
		}
		if m.Config.Kind == "llm" || m.Config.TextContext != 0 || m.Config.TextOutput != 0 {
			return empty, invalid("LLM is configured only through agent settings")
		}
		if m.Key != nil && (m.RemoveKey || len(*m.Key) < 1 || len(*m.Key) > 8192 || strings.IndexFunc(*m.Key, func(r rune) bool { return r < 33 || r > 126 }) >= 0) {
			return empty, invalid("Invalid key or removal combination")
		}
	} else {
		if m.Action != "disable" && m.Action != "delete" && m.Action != "select" && m.Action != "probe" {
			return empty, invalid("Unknown action")
		}
		if !identifier.MatchString(m.ID) || len(m.ID) > 128 || m.Config != nil || m.Key != nil || m.RemoveKey {
			return empty, invalid("Action accepts id only")
		}
		// Reject explicitly supplied irrelevant zero/null values as well.
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		for k := range fields {
			if k != "requestId" && k != "action" && k != "id" {
				return empty, invalid("Unexpected action field")
			}
		}
	}
	// ponytail: serialize settings writes/free probes in this service; split per-provider
	// coordination if measured settings contention warrants it. Never hold a SQL tx over I/O.
	s.mu.Lock()
	defer s.mu.Unlock()
	var previous, response, state string
	err = s.DB.QueryRowContext(ctx, `SELECT input_json,response_json,state FROM write_requests WHERE project_id='_app' AND operation='providers' AND request_id=?`, m.RequestID).Scan(&previous, &response, &state)
	if err == nil {
		if previous != string(input) {
			return empty, store.ErrRequestConflict
		}
		if state != "completed" {
			return empty, store.ErrRequestPending
		}
		if json.Unmarshal([]byte(response), &empty) != nil {
			return empty, ErrUnavailable
		}
		return empty, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return empty, ErrUnavailable
	}
	id := m.ID
	if m.Config != nil {
		id = m.Config.ID
	}
	f, deleted, err := readConfig(ctx, s.DB, id)
	if m.Action == "create" {
		if !errors.Is(err, ErrNotFound) {
			if err != nil {
				return empty, err
			}
			return empty, invalid("Provider ID already exists")
		}
		f = FrozenProvider{Config: *m.Config}
	} else if err != nil {
		return empty, err
	} else if deleted {
		return empty, ErrNotFound
	}
	if m.Action == "update" {
		if f.Config.Kind != m.Config.Kind || f.Config.Protocol != m.Config.Protocol {
			return empty, invalid("Create a new configuration to change kind/protocol")
		}
		f.Config = *m.Config
	}
	if m.RemoveKey {
		f.CredentialVersion = ""
	}
	if m.Key != nil {
		version := uuid.NewString()
		if err = s.Secrets.Put(version, *m.Key); err != nil {
			return empty, ErrUnavailable
		}
		f.CredentialVersion = version
	}
	var probe *ProbeResult
	if m.Action == "probe" {
		if !f.Config.Enabled {
			return empty, invalid("Provider is disabled")
		}
		p := s.Probe(ctx, f)
		probe = &p
	}
	if m.Action == "select" && !f.Config.Enabled {
		return empty, invalid("Provider is disabled")
	}
	if m.Action == "disable" || m.Action == "delete" {
		f.Config.Enabled = false
	}
	receipt, err := store.WriteRequest(ctx, s.DB, scope, input, func(tx *sql.Tx) ([]byte, error) {
		switch m.Action {
		case "create", "update", "disable", "delete":
			config, _ := json.Marshal(f.Config)
			if m.Action == "create" {
				_, err = tx.ExecContext(ctx, `INSERT INTO provider_configs(id,config_json,credential_version) VALUES(?,?,?)`, id, string(config), f.CredentialVersion)
			} else {
				_, err = tx.ExecContext(ctx, `UPDATE provider_configs SET config_json=?,credential_version=?,deleted=? WHERE id=?`, string(config), f.CredentialVersion, m.Action == "delete", id)
			}
			if err != nil {
				return nil, ErrUnavailable
			}
			if !f.Config.Enabled {
				if _, err = tx.ExecContext(ctx, `DELETE FROM provider_defaults WHERE provider_id=?`, id); err != nil {
					return nil, ErrUnavailable
				}
			}
		case "select":
			_, err = tx.ExecContext(ctx, `INSERT INTO provider_defaults(kind,provider_id) VALUES(?,?) ON CONFLICT(kind) DO UPDATE SET provider_id=excluded.provider_id`, f.Config.Kind, id)
			if err != nil {
				return nil, ErrUnavailable
			}
		}
		v := View{Config: f.Config, HasKey: f.CredentialVersion != ""}
		err = tx.QueryRowContext(ctx, `SELECT kind FROM provider_defaults WHERE provider_id=?`, id).Scan(&v.DefaultFor)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUnavailable
		}
		return json.Marshal(Receipt{Provider: &v, Probe: probe, Deleted: m.Action == "delete"})
	})
	if err != nil {
		return empty, err
	}
	if json.Unmarshal(receipt, &empty) != nil {
		return empty, ErrUnavailable
	}
	return empty, nil
}

// Receipt reads the original completed journal result without requiring a key.
// Absence is not proof of non-execution; clients may only replay the original ID/input.
func (s *Service) Receipt(ctx context.Context, id string) (Receipt, error) {
	var result Receipt
	if err := (store.Scope{ProjectID: "_app", Operation: "providers", RequestID: id}).Validate(); err != nil {
		return result, invalid("Invalid requestId")
	}
	var raw string
	err := s.DB.QueryRowContext(ctx, `SELECT response_json FROM write_requests WHERE project_id='_app' AND operation='providers' AND request_id=? AND state='completed'`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return result, ErrNotFound
	}
	if err != nil || json.Unmarshal([]byte(raw), &result) != nil {
		return result, ErrUnavailable
	}
	return result, nil
}
