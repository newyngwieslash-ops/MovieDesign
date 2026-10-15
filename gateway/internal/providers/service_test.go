package providers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
)

func testService(t *testing.T) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "providers.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	secrets, err := OpenSecrets(filepath.Join(dir, "credentials"))
	if err != nil {
		t.Fatal(err)
	}
	return NewService(db, secrets), dir
}
func validConfig(id, base string) Config {
	return Config{ID: id, Name: "Test provider", Kind: "image", Protocol: "openai-images", BaseURL: base, ModelID: "custom-image", Enabled: true, Capability: Capability{MaxPromptBytes: 65536, MaxCount: 1, MaxReferences: 1, Sizes: []string{"1024x1024"}}, Pricing: &Pricing{Currency: "USD", Unit: "image", UnitAmount: 0, MinorUnitScale: 2, CreditDenominator: 1, Source: "declared", Version: "v1"}}
}
func execute(t *testing.T, s *Service, v any) Receipt {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestSecretsDPAPIRestartAndImmutability(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	key := "synthetic-secret-without-real-account"
	if err = s.Put("version-1", key); err != nil {
		t.Fatal(err)
	}
	encrypted, err := os.ReadFile(filepath.Join(dir, "version-1.dpapi"))
	if err != nil || strings.Contains(string(encrypted), key) {
		t.Fatal("not encrypted", err)
	}
	reopened, err := OpenSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := reopened.Get("version-1")
	if err != nil || actual != key {
		t.Fatal("restart failed", err)
	}
	if reopened.Put("version-1", "replace") == nil {
		t.Fatal("immutable secret replaced")
	}
	if _, err = reopened.Get("../outside"); err == nil {
		t.Fatal("path traversal")
	}
	if err = reopened.Delete("version-1"); err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.Get("version-1"); err == nil {
		t.Fatal("delete failed")
	}
}
func TestProviderPersistenceFrozenRotationReplayAndRedaction(t *testing.T) {
	s, dir := testService(t)
	cfg := validConfig("one", "http://127.0.0.1:11434/v1")
	key := "synthetic-distinct-key-A"
	body := map[string]any{"requestId": "create-1", "action": "create", "config": cfg, "key": key}
	r := execute(t, s, body)
	if !r.Provider.HasKey {
		t.Fatal("missing key marker")
	}
	frozen, err := s.Resolve("one")
	if err != nil {
		t.Fatal(err)
	}
	execute(t, s, map[string]any{"requestId": "select-1", "action": "select", "id": "one"})
	cfg.ModelID = "changed-model"
	body2 := map[string]any{"requestId": "update-1", "action": "update", "config": cfg, "key": "synthetic-key-B"}
	execute(t, s, body2)
	cfg.Name = "preserve key"
	delete(body2, "key")
	body2["requestId"] = "update-2"
	body2["config"] = cfg
	execute(t, s, body2)
	current, err := s.ResolveDefault("image")
	if err != nil {
		t.Fatal(err)
	}
	currentKey, err := s.Credential(current)
	if err != nil || currentKey != "synthetic-key-B" {
		t.Fatal("omitted key lost", err)
	}
	execute(t, s, map[string]any{"requestId": "disable-1", "action": "disable", "id": "one"})
	if _, err = s.Resolve("one"); err == nil {
		t.Fatal("disabled selectable")
	}
	oldKey, err := s.Credential(frozen)
	if err != nil || oldKey != key || frozen.Config.ModelID != "custom-image" {
		t.Fatal("frozen credential/config changed", err)
	}
	replay := execute(t, s, body)
	b1, _ := json.Marshal(r)
	b2, _ := json.Marshal(replay)
	if string(b1) != string(b2) {
		t.Fatal("receipt changed")
	}
	body["key"] = "different"
	raw, _ := json.Marshal(body)
	if _, err = s.Execute(context.Background(), raw); !errors.Is(err, store.ErrRequestConflict) {
		t.Fatal("secret mismatch replay accepted", err)
	}
	if _, err = s.Execute(context.Background(), []byte(`{"requestId":"create-1","action":"disable","id":"one","ignored":true}`)); !errors.Is(err, ErrInvalid) {
		t.Fatal("unknown input accepted")
	}
	execute(t, s, map[string]any{"requestId": "delete-1", "action": "delete", "id": "one"})
	list, err := s.List(context.Background())
	if err != nil || len(list) != 0 {
		t.Fatal("delete not hidden", err)
	}
	if v, err := s.Credential(frozen); err != nil || v != key {
		t.Fatal("delete destroyed frozen credential")
	}
	for _, file := range []string{"providers.db", "providers.db-wal", "providers.db-journal"} {
		data, e := os.ReadFile(filepath.Join(dir, file))
		if e == nil && (strings.Contains(string(data), key) || strings.Contains(string(data), "synthetic-key-B")) {
			t.Fatal("secret in SQL", file)
		}
	}
	var log string
	if err = s.DB.QueryRow(`SELECT input_json FROM write_requests WHERE request_id='create-1'`).Scan(&log); err != nil || strings.Contains(log, key) || !strings.Contains(log, "sha256") {
		t.Fatal("bad receipt journal", err)
	}
}
func TestProviderSchemaValidation(t *testing.T) {
	s, _ := testService(t)
	base := validConfig("one", "https://api.example.com/v1")
	for name, change := range map[string]func(*Config){"protocol": func(c *Config) { c.Protocol = "guess" }, "header": func(c *Config) { c.Headers.Project = "safe\r\nHost:evil" }, "price": func(c *Config) { c.Pricing.UnitAmount = -1 }, "limit": func(c *Config) { c.Capability.MaxPromptBytes = 65537 }} {
		t.Run(name, func(t *testing.T) {
			copy := base
			p := *base.Pricing
			copy.Pricing = &p
			change(&copy)
			if ValidateConfig(copy) == nil {
				t.Fatal("accepted")
			}
		})
	}
	raw, _ := json.Marshal(map[string]any{"requestId": "schema", "action": "create", "config": base})
	for _, replacement := range []string{`"headers":{"Host":"evil"}`, `"headers":{"Authorization":"secret"}`, `"headers":{"openai-project":"wrong case"}`} {
		bad := strings.Replace(string(raw), `"headers":{}`, replacement, 1)
		if _, err := s.Execute(context.Background(), []byte(bad)); !errors.Is(err, ErrInvalid) {
			t.Fatal("unsupported header", err)
		}
	}
	bad := strings.Replace(string(raw), `"capability":{`, `"capability":{"paramSchema":{},`, 1)
	if _, err := s.Execute(context.Background(), []byte(bad)); !errors.Is(err, ErrInvalid) {
		t.Fatal("unsupported schema")
	}
}
func TestProviderNetworkProbeAndRedirectBoundaries(t *testing.T) {
	var calls atomic.Int32
	var status atomic.Int32
	status.Store(200)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/prefix/v1/models" || r.Header.Get("Authorization") != "Bearer synthetic-probe" {
			t.Errorf("incorrect probe %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(int(status.Load()))
		_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"custom-image"}]}`)
	}))
	defer upstream.Close()
	s, _ := testService(t)
	cfg := validConfig("probe", upstream.URL+"/prefix/v1")
	execute(t, s, map[string]any{"requestId": "save", "action": "create", "config": cfg, "key": "synthetic-probe"})
	r := execute(t, s, map[string]any{"requestId": "probe-1", "action": "probe", "id": "probe"})
	if !r.Probe.RemoteTested || !r.Probe.ModelFound || !r.Probe.ModelsListed {
		t.Fatal(r.Probe)
	}
	execute(t, s, map[string]any{"requestId": "probe-1", "action": "probe", "id": "probe"})
	if calls.Load() != 1 {
		t.Fatal("probe replay repeated network")
	}
	status.Store(401)
	r = execute(t, s, map[string]any{"requestId": "probe-2", "action": "probe", "id": "probe"})
	if r.Probe.ModelsListed || r.Probe.HTTPStatus != 401 {
		t.Fatal("authentication failure misreported")
	}
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls.Add(1) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer redirect.Close()
	client, err := NewClient(redirect.URL, false)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", redirect.URL, nil)
	req.Header.Set("Authorization", "Bearer never-forward")
	if _, err = client.Do(req); err == nil || targetCalls.Load() != 0 {
		t.Fatal("cross-origin redirect followed")
	}
	if _, err = client.Get(target.URL); err == nil || targetCalls.Load() != 0 {
		t.Fatal("cross-origin direct request allowed")
	}
	download, err := NewClient(redirect.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	req, _ = http.NewRequest("GET", target.URL, nil)
	req.Header.Set("Authorization", "Bearer never-forward")
	if _, err = download.Do(req); err == nil || targetCalls.Load() != 0 {
		t.Fatal("authenticated download allowed")
	}
	for _, raw := range []string{"http://127.0.0.1:8001/a", "http://169.254.169.254/a", "https://metadata.google.internal/a"} {
		if _, err = download.Get(raw); err == nil {
			t.Fatal("forbidden download", raw)
		}
	}
	video := validConfig("video", "https://example.com/api/v3")
	video.Kind = "video"
	video.Protocol = "ark-video"
	video.Pricing = nil
	execute(t, s, map[string]any{"requestId": "video-save", "action": "create", "config": video})
	v := execute(t, s, map[string]any{"requestId": "video-probe", "action": "probe", "id": "video"})
	if v.Probe.RemoteTested || !v.Probe.LocalValid {
		t.Fatal("video probe invented")
	}
}

type failedSecrets struct{ SecretStore }

func (f failedSecrets) Put(id, value string) error { return errors.New("synthetic disk failure") }
func TestProviderSecretCommitOrderingAndRuntimeDNS(t *testing.T) {
	s, dir := testService(t)
	cfg := validConfig("safe", "http://127.0.0.1:11434/v1")
	execute(t, s, map[string]any{"requestId": "save", "action": "create", "config": cfg, "key": "old"})
	before, _ := s.Resolve("safe")
	original := s.Secrets
	s.Secrets = failedSecrets{original}
	raw, _ := json.Marshal(map[string]any{"requestId": "fail", "action": "update", "config": cfg, "key": "new"})
	if _, err := s.Execute(context.Background(), raw); err == nil {
		t.Fatal("disk failure ignored")
	}
	after, _ := s.Resolve("safe")
	if before.CredentialVersion != after.CredentialVersion {
		t.Fatal("published missing secret")
	}
	s.Secrets = original
	if _, err := s.DB.Exec(`CREATE TRIGGER reject_provider_update BEFORE UPDATE ON provider_configs BEGIN SELECT RAISE(ABORT,'controlled SQL failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Execute(context.Background(), raw); err == nil {
		t.Fatal("SQL failure ignored")
	}
	after, _ = s.Resolve("safe")
	if before.CredentialVersion != after.CredentialVersion {
		t.Fatal("failed SQL changed pointer")
	}
	files, err := os.ReadDir(filepath.Join(dir, "credentials"))
	if err != nil || len(files) != 2 {
		t.Fatal("expected safe encrypted orphan", len(files), err)
	}
	var n int
	if err = s.DB.QueryRow(`SELECT count(*) FROM write_requests WHERE request_id='fail'`).Scan(&n); err != nil || n != 0 {
		t.Fatal("failed write receipt committed")
	}
	client, err := NewClient("https://public.example.com/v1", false)
	if err != nil {
		t.Fatal(err)
	}
	dial := client.Transport.(guardTransport).next.(*http.Transport).DialContext
	// Real localhost DNS resolution at dial time cannot rebind a cloud endpoint locally.
	if conn, err := dial(context.Background(), "tcp", "localhost:11434"); err == nil {
		conn.Close()
		t.Fatal("DNS-to-loopback accepted")
	}
	if _, err = Endpoint("https://example.com/api/v1", "../admin"); err == nil {
		t.Fatal("path traversal operation")
	}
	if got, err := Endpoint("https://example.com/api/v1/", "images/generations"); err != nil || got != "https://example.com/api/v1/images/generations" {
		t.Fatal("base path lost", got, err)
	}
}

func TestProviderSupportMigrationAppEvents(t *testing.T) {
	s, _ := testService(t)
	if _, err := s.DB.Exec(`INSERT INTO events(project_id,type,payload_json,created_at) VALUES(NULL,'app_update_check','{}',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO events(project_id,type,payload_json,created_at) VALUES('missing','task','{}',1)`); err == nil {
		t.Fatal("missing project accepted")
	}
	var n int
	if err := s.DB.QueryRow(`SELECT count(*) FROM events WHERE project_id='any-project'`).Scan(&n); err != nil || n != 0 {
		t.Fatal("global event leaked into project query")
	}
}

func TestProviderDefaultsRemovalAndServiceRestart(t *testing.T) {
	s, _ := testService(t)
	a := validConfig("a", "http://127.0.0.1:11434/v1")
	b := validConfig("b", "https://example.com/v1")
	execute(t, s, map[string]any{"requestId": "create-a", "action": "create", "config": a, "key": "synthetic-retained"})
	frozen, _ := s.Resolve("a")
	execute(t, s, map[string]any{"requestId": "create-b", "action": "create", "config": b})
	execute(t, s, map[string]any{"requestId": "select-a", "action": "select", "id": "a"})
	execute(t, s, map[string]any{"requestId": "select-b", "action": "select", "id": "b"})
	restarted := NewService(s.DB, s.Secrets)
	selected, err := restarted.ResolveDefault("image")
	if err != nil || selected.Config.ID != "b" {
		t.Fatal("default did not persist", err)
	}
	execute(t, restarted, map[string]any{"requestId": "remove-a-key", "action": "update", "config": a, "remove_key": true})
	current, _ := restarted.Resolve("a")
	if current.CredentialVersion != "" {
		t.Fatal("current key not removed")
	}
	if key, err := restarted.Credential(frozen); err != nil || key != "synthetic-retained" {
		t.Fatal("frozen key removed")
	}
}

func TestProviderEmptyArraysAndReceiptLookup(t *testing.T) {
	s, _ := testService(t)
	cfg := validConfig("empty", "https://example.com/v1")
	cfg.Capability.Sizes = nil
	cfg.Capability.DurationSeconds = nil
	r := execute(t, s, map[string]any{"requestId": "array", "action": "create", "config": cfg, "key": "synthetic-rotation"})
	raw, _ := json.Marshal(r)
	if strings.Contains(string(raw), `"sizes":null`) || strings.Contains(string(raw), `"duration_seconds":null`) {
		t.Fatal("accepted empty arrays must render as []", string(raw))
	}
}

func TestSecretsRealCorruptionAndFilesystemFailures(t *testing.T) {
	s, dir := testService(t)
	cfg := validConfig("fault", "http://127.0.0.1:11434/v1")
	execute(t, s, map[string]any{"requestId": "initial", "action": "create", "config": cfg, "key": "synthetic-old-fault-key"})
	old, _ := s.Resolve("fault")
	native := s.Secrets.(*DPAPIStore)
	originalDir := native.dir
	originalFile := filepath.Join(originalDir, old.CredentialVersion+".dpapi")
	encrypted, err := os.ReadFile(originalFile)
	if err != nil {
		t.Fatal(err)
	}
	corrupt := append([]byte(nil), encrypted...)
	corrupt[len(corrupt)/2] ^= 0xff
	if err = os.WriteFile(originalFile, corrupt, 0600); err != nil {
		t.Fatal(err)
	}
	if value, err := s.Credential(old); err == nil || value != "" {
		t.Fatal("corrupt ciphertext decrypted")
	}
	if err = os.WriteFile(originalFile, encrypted, 0600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"temp-create", "publication"} {
		t.Run(mode, func(t *testing.T) {
			var obstruction string
			if mode == "temp-create" {
				native.dir = filepath.Join(originalDir, "missing-child")
			} else {
				native.beforePublish = func(path string) {
					pending, err := filepath.Glob(filepath.Join(originalDir, ".pending-*"))
					if err != nil || len(pending) != 1 {
						t.Fatal("expected actual encrypted temporary", err)
					}
					data, err := os.ReadFile(pending[0])
					if err != nil || strings.Contains(string(data), "synthetic-new-fault-key") {
						t.Fatal("plaintext temporary", err)
					}
					decrypted, err := crypt(data, false)
					if err != nil || string(decrypted) != "synthetic-new-fault-key" {
						t.Fatal("temporary was not real DPAPI encrypted data", err)
					}
					clear(decrypted)
					obstruction = path
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
				}
			}
			raw, _ := json.Marshal(map[string]any{"requestId": "fault-" + mode, "action": "update", "config": cfg, "key": "synthetic-new-fault-key"})
			_, writeErr := s.Execute(context.Background(), raw)
			native.dir = originalDir
			native.beforePublish = nil
			if writeErr == nil {
				t.Fatal("real filesystem failure ignored")
			}
			if obstruction != "" {
				if err := os.Remove(obstruction); err != nil {
					t.Fatal(err)
				}
			}
			current, err := s.Resolve("fault")
			if err != nil || current.CredentialVersion != old.CredentialVersion {
				t.Fatal("pointer changed", err)
			}
			if key, err := s.Credential(old); err != nil || key != "synthetic-old-fault-key" {
				t.Fatal("old credential damaged", err)
			}
			var count int
			if err = s.DB.QueryRow(`SELECT count(*) FROM write_requests WHERE request_id=?`, "fault-"+mode).Scan(&count); err != nil || count != 0 {
				t.Fatal("success receipt after failure", err)
			}
			files, err := os.ReadDir(originalDir)
			if err != nil {
				t.Fatal(err)
			}
			if len(files) != 1 {
				t.Fatal("temporary file leaked", len(files))
			}
			for _, file := range files {
				data, err := os.ReadFile(filepath.Join(originalDir, file.Name()))
				if err != nil || strings.Contains(string(data), "synthetic-new-fault-key") || strings.Contains(string(data), "synthetic-old-fault-key") {
					t.Fatal("plaintext artifact", err)
				}
			}
		})
	}
	data, err := os.ReadFile(filepath.Join(dir, "providers.db"))
	if err != nil || strings.Contains(string(data), "synthetic-new-fault-key") {
		t.Fatal("SQL leaked secret", err)
	}
}
func TestProviderReceiptRecoveryAfterKeyRotation(t *testing.T) {
	s, _ := testService(t)
	cfg := validConfig("rotate", "https://example.com/v1")
	execute(t, s, map[string]any{"requestId": "first", "action": "create", "config": cfg, "key": "old"})
	r := execute(t, s, map[string]any{"requestId": "rotation-original", "action": "update", "config": cfg, "key": "new"})
	reopened := NewService(s.DB, s.Secrets)
	got, err := reopened.Receipt(context.Background(), "rotation-original")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(r)
	b, _ := json.Marshal(got)
	if string(a) != string(b) {
		t.Fatal("different original receipt")
	}
	if _, err = reopened.Receipt(context.Background(), "never-sent"); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing receipt misreported")
	}
}
