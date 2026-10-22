package agent

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Host struct {
	textMu            sync.Mutex
	textSession       string
	Relay             *Relay
	password, base    string
	client            *http.Client
	active            View
	fingerprint       string
	credentialVersion string
	tools             []string
}
type Bootstrap struct {
	Password string `json:"password"`
	Config   string `json:"config"`
	Key      string `json:"key"`
}
type Status struct {
	HostReady      bool       `json:"host_ready"`
	Configured     bool       `json:"configured"`
	ToolsReady     bool       `json:"tools_ready"`
	Active         *LLMConfig `json:"active"`
	PendingChanges bool       `json:"pending_changes"`
	Message        string     `json:"message"`
}

func fingerprint(v View, version string) string {
	raw, _ := json.Marshal(struct {
		View    View
		Version string
	}{v, version})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// The native parent creates a private ACL directory before giving this path to the
// gateway. Only DPAPI ciphertext crosses disk; native consumes and removes it.
func NewHost(s *Settings, dir string, connections ...MCPConnection) (*Host, error) {
	v, version, err := s.Read(context.Background())
	if err != nil {
		return nil, err
	}
	c := LLMConfig{}
	if v.Config != nil {
		c = *v.Config
	}
	m := MCPConnection{}
	if len(connections) > 0 {
		m = connections[0]
	}
	config, err := BuildConfig(c, m)
	if err != nil {
		return nil, err
	}
	secret := ""
	if version != "" {
		secret, err = s.Secrets.Get(version)
		if err != nil {
			return nil, errors.New("LLM credential unavailable")
		}
	}
	var relay *Relay
	if c != (LLMConfig{}) {
		if m.Origin == "" {
			return nil, errors.New("private inference origin required")
		}
		relay, err = newRelay(s.DB, c, secret, m.Origin)
		if err != nil {
			return nil, err
		}
		var value map[string]any
		if err = json.Unmarshal(config, &value); err != nil {
			return nil, err
		}
		provider := value["provider"].(map[string]any)["moviedesign"].(map[string]any)
		provider["options"].(map[string]any)["baseURL"] = "http://127.0.0.1:8001" + inferencePath
		provider["models"].(map[string]any)[c.ModelID].(map[string]any)["headers"] = map[string]string{"Origin": m.Origin}
		config, err = json.Marshal(value)
		if err != nil {
			return nil, err
		}
		secret = relay.token
	}
	token := make([]byte, 32)
	if _, err = rand.Read(token); err != nil {
		return nil, err
	}
	password := hex.EncodeToString(token)
	raw, _ := json.Marshal(Bootstrap{Password: password, Config: string(config), Key: secret})
	encrypted, err := providers.OpenSecrets(dir)
	if err != nil {
		return nil, err
	}
	if err = encrypted.Put("launch", string(raw)); err != nil {
		return nil, err
	}
	return &Host{Relay: relay, credentialVersion: version, password: password, base: "http://127.0.0.1:4096", client: &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect denied") }}, tools: m.Tools, active: v, fingerprint: fingerprint(v, version)}, nil
}
func (h *Host) read(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", h.base+path, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth("opencode", h.password)
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, errors.New("agent unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, errors.New("agent authentication or response rejected")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil || len(raw) >= 2<<20 {
		return nil, errors.New("agent response invalid")
	}
	return raw, nil
}
func (h *Host) Ready(ctx context.Context) bool {
	return h.readyError(ctx) == nil
}
func (h *Host) readyError(ctx context.Context) error {
	if h == nil {
		return errors.New("运行时尚未启动")
	}
	raw, err := h.read(ctx, "/global/health")
	if err != nil {
		return errors.New("运行时健康检查未通过")
	}
	var health struct {
		Healthy bool   `json:"healthy"`
		Version string `json:"version"`
	}
	if json.Unmarshal(raw, &health) != nil || !health.Healthy || health.Version != "1.19.0" {
		return errors.New("运行时版本校验未通过")
	}
	raw, err = h.read(ctx, "/agent")
	if err != nil {
		return errors.New("运行时配置加载未完成")
	}
	if verifyPermissions(raw, h.tools...) != nil {
		return errors.New("运行时权限校验未通过")
	}
	return nil
}
func verifyPermissions(raw []byte, tools ...string) error {
	allowed := map[string]bool{}
	for _, tool := range tools {
		allowed[tool] = true
	}
	var agents []struct {
		Name       string                                         `json:"name"`
		Permission []struct{ Permission, Pattern, Action string } `json:"permission"`
	}
	if json.Unmarshal(raw, &agents) != nil || len(agents) == 0 {
		return errors.New("agent policy absent")
	}
	found := false
	for _, a := range agents {
		if a.Name != "build" && a.Name != "summary" && a.Name != "title" && a.Name != "compaction" && a.Name != "text" {
			return errors.New("unapproved agent")
		}
		last := -1
		for i, r := range a.Permission {
			if r.Permission == "*" && r.Pattern == "*" {
				last = i
			}
		}
		if last < 0 || a.Permission[last].Action != "deny" {
			return errors.New("default deny absent")
		}
		question := false
		granted := map[string]bool{}
		for _, r := range a.Permission[last+1:] {
			if r.Action == "deny" {
				continue
			}
			if a.Name == "build" && r.Permission == "question" && r.Pattern == "*" && r.Action == "allow" {
				question = true
				continue
			}
			if a.Name == "build" && allowed[r.Permission] && r.Pattern == "*" && r.Action == "allow" {
				granted[r.Permission] = true
				continue
			}
			return errors.New("unapproved effective permission")
		}
		if a.Name == "build" {
			found = question
			for _, tool := range tools {
				if !granted[tool] {
					return errors.New("MCP permission missing")
				}
			}
		}
	}
	if !found {
		return errors.New("native question missing")
	}
	return nil
}
func (h *Host) Status(ctx context.Context, s *Settings, bootstrapDir string) Status {
	v, version, err := s.Read(ctx)
	st := Status{Configured: v.Config != nil, Message: "agent 未就绪；媒体工具尚未注册。"}
	if h != nil {
		st.Active = h.active.Config
		st.PendingChanges = err == nil && h.fingerprint != fingerprint(v, version)
	}
	if data, e := os.ReadFile(filepath.Join(bootstrapDir, "error.txt")); e == nil {
		switch string(data) {
		case "port_conflict":
			st.Message = "agent 启动失败：127.0.0.1:4096 已占用；未连接或关闭现有服务。"
		case "port_permission_denied":
			st.Message = "agent 启动失败：127.0.0.1:4096 绑定被拒绝；可能受系统端口保留或权限限制，请检查后重新启动。"
		case "port_bind_failed":
			st.Message = "agent 启动失败：127.0.0.1:4096 绑定失败，请检查系统网络状态后重新启动。"
		case "start_failed":
			st.Message = "agent 启动失败，请检查应用专用 binary 后重新启动。"
		case "exited":
			st.Message = "受管 agent 已退出，请重新启动应用。"
		}
		return st // Never probe a listener after native reports a failed/terminated owned child.
	}
	readyError := h.readyError(ctx)
	st.HostReady = readyError == nil
	if readyError != nil {
		st.Message = readyError.Error() + "；媒体工具尚未注册。"
	} else {
		st.Message = "受管 LLM 运行时已就绪；媒体工具尚未注册。"
	}
	return st
}
