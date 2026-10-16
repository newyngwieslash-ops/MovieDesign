package providers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func blockedIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	return !ip.IsValid() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip == netip.MustParseAddr("100.100.100.200") || ip == netip.MustParseAddr("168.63.129.16") || ip == netip.MustParseAddr("192.0.0.192") || ip == netip.MustParseAddr("fd00:ec2::254")
}
func localHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && (ip.Unmap().IsLoopback() || ip.Unmap().IsPrivate())
}
func ValidateBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" && u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || u.RawPath != "" || strings.ContainsAny(raw, "\\\r\n\t") {
		return errors.New("invalid endpoint")
	}
	host := strings.ToLower(u.Hostname())
	if strings.Contains(host, "%") || strings.HasSuffix(host, ".") || host == "metadata.google.internal" || host == "metadata" {
		return errors.New("forbidden host")
	}
	if ip, err := netip.ParseAddr(host); err == nil && blockedIP(ip) {
		return errors.New("forbidden address")
	}
	if u.Scheme == "http" && !localHost(host) {
		return errors.New("HTTP requires an explicit local endpoint")
	}
	if port := u.Port(); port != "" {
		p, err := strconv.Atoi(port)
		if err != nil || p < 1 || p > 65535 || p == 8001 || p == 4096 {
			return errors.New("forbidden port")
		}
	}
	for _, part := range strings.Split(u.Path, "/") {
		if part == "." || part == ".." {
			return errors.New("ambiguous endpoint path")
		}
	}
	return nil
}

// Endpoint appends a known protocol path to the configured API prefix. Callers cannot
// supply absolute URLs, interpolation, query strings or dot segments as an operation.
func Endpoint(base, operation string) (string, error) {
	if ValidateBaseURL(base) != nil || operation == "" || strings.HasPrefix(operation, "/") || strings.ContainsAny(operation, "%?#\\:") {
		return "", invalid("Invalid endpoint operation")
	}
	for _, p := range strings.Split(operation, "/") {
		if p == "." || p == ".." || p == "" {
			return "", invalid("Invalid endpoint operation")
		}
	}
	u, _ := url.Parse(base)
	u.Path = strings.TrimRight(u.Path, "/") + "/" + operation
	return u.String(), nil
}

// NewClient is the shared generation/probe/download network boundary. No proxies,
// redirects, or automatic request retries. DNS answers are checked and then dialed
// by numeric IP (no second resolver lookup/rebinding window). Downloads must use
// unauthenticated requests; unrelated private/local targets remain forbidden.
func NewClient(base string, download bool) (*http.Client, error) {
	return newClient(base, download, 20*time.Second)
}

// Image generation gets a bounded ten-minute budget; probes/downloads retain 20s.
// Both use this identical DNS/address/redirect boundary.
func newClient(base string, download bool, budget time.Duration) (*http.Client, error) {
	if err := ValidateBaseURL(base); err != nil {
		return nil, err
	}
	configured, _ := url.Parse(base)
	headerBudget := budget
	if budget == 20*time.Second {
		headerBudget = 15 * time.Second
	}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, ResponseHeaderTimeout: headerBudget, TLSHandshakeTimeout: 10 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil || port == "8001" || port == "4096" {
			return nil, errors.New("forbidden endpoint")
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(ips) == 0 {
			return nil, errors.New("endpoint DNS failed")
		}
		for _, ip := range ips {
			ip = ip.Unmap()
			if blockedIP(ip) || (ip.IsPrivate() || ip.IsLoopback()) && !(localHost(configured.Hostname()) && strings.EqualFold(host, configured.Hostname()) && port == effectivePort(configured)) {
				return nil, errors.New("forbidden resolved address")
			}
		}
		// All answers were checked. Dial one answer once; a user can explicitly retry a failed probe.
		return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
	}
	return &http.Client{Transport: guardTransport{base: configured, download: download, next: transport}, Timeout: budget, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("provider redirects are not allowed") }}, nil
}
func effectivePort(u *url.URL) string {
	if u.Port() != "" {
		return u.Port()
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}

type guardTransport struct {
	base     *url.URL
	download bool
	next     http.RoundTripper
}

func (g guardTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	// Signed download query strings are allowed, but never userinfo/fragments/odd schemes.
	u := *r.URL
	u.RawQuery = ""
	u.ForceQuery = false
	if ValidateBaseURL(u.String()) != nil {
		return nil, errors.New("forbidden request URL")
	}
	same := r.URL.Scheme == g.base.Scheme && strings.EqualFold(r.URL.Hostname(), g.base.Hostname()) && effectivePort(r.URL) == effectivePort(g.base)
	if !g.download && !same || g.download && !same && r.URL.Scheme != "https" {
		return nil, errors.New("request destination differs from provider")
	}
	if g.download && (r.Header.Get("Authorization") != "" || r.Header.Get("OpenAI-Organization") != "" || r.Header.Get("OpenAI-Project") != "" || r.Header.Get("Cookie") != "") {
		return nil, errors.New("download cannot carry provider credentials")
	}
	return g.next.RoundTrip(r)
}
func (s *Service) Probe(ctx context.Context, f FrozenProvider) ProbeResult {
	result := ProbeResult{LocalValid: true, Message: "配置校验通过，远端未测试；此协议没有已支持的免费探测接口。"}
	if f.Config.Protocol != "openai-images" {
		return result
	}
	key, err := s.Credential(f)
	if err != nil {
		result.Message = "凭据版本不可用，远端未测试。"
		return result
	}
	endpoint, err := Endpoint(f.Config.BaseURL, "models")
	if err != nil {
		result.Message = "端点无效，远端未测试。"
		return result
	}
	client, err := NewClient(f.Config.BaseURL, false)
	if err != nil {
		result.Message = "端点无效，远端未测试。"
		return result
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		result.Message = "无法创建免费测试请求。"
		return result
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	if f.Config.Headers.Organization != "" {
		req.Header.Set("OpenAI-Organization", f.Config.Headers.Organization)
	}
	if f.Config.Headers.Project != "" {
		req.Header.Set("OpenAI-Project", f.Config.Headers.Project)
	}
	response, err := client.Do(req)
	if err != nil {
		result.Message = "免费连接测试未取得响应；请检查地址、证书或网络。不会自动重试。"
		return result
	}
	defer response.Body.Close()
	result.RemoteTested = true
	result.HTTPStatus = response.StatusCode
	if response.StatusCode == 401 || response.StatusCode == 403 {
		result.Message = "远端拒绝凭据或访问权限。"
		return result
	}
	if response.StatusCode != 200 {
		result.Message = "远端模型列表不可用；未验证生成能力。"
		return result
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		result.Message = "模型列表超出限制或读取失败。"
		return result
	}
	var data struct {
		Object string `json:"object"`
		Data   []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &data) != nil || data.Object != "list" || data.Data == nil {
		result.Message = "远端响应不是支持的模型列表格式。"
		return result
	}
	result.ModelsListed = true
	for _, model := range data.Data {
		if model.ID == f.Config.ModelID {
			result.ModelFound = true
		}
	}
	result.Message = "免费模型列表读取成功；所选模型未列出，生成能力和报价仍为用户声明。"
	if result.ModelFound {
		result.Message = "免费模型列表读取成功且列出所选模型；未执行生成，能力和报价仍为用户声明。"
	}
	return result
}
