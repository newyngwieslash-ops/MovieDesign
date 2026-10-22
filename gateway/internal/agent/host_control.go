package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"
)

// control does not retry a possibly accepted native mutation. Only gateway-owned
// IDs reach these private methods; no browser can access the management password.
func (h *Host) control(ctx context.Context, path string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, e := http.NewRequestWithContext(ctx, "POST", h.base+path, nil)
	if e != nil {
		return e
	}
	request.SetBasicAuth("opencode", h.password)
	client := *h.client
	client.Timeout = 0
	response, e := client.Do(request)
	if e != nil {
		return errors.New("native control acceptance unknown")
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return errors.New("native control rejected")
	}
	return nil
}
func (h *Host) Abort(ctx context.Context, session string) error {
	return h.control(ctx, "/session/"+url.PathEscape(session)+"/abort")
}
func (h *Host) RejectQuestion(ctx context.Context, id string) error {
	return h.control(ctx, "/question/"+url.PathEscape(id)+"/reject")
}
func (h *Host) DisconnectTools(ctx context.Context) error {
	return h.control(ctx, "/mcp/design/disconnect")
}

// OpenCode 1.19.0 disposes instance/MCP resources, but keeps its HTTP server alive.
func (h *Host) Dispose(ctx context.Context) error      { return h.control(ctx, "/global/dispose") }
func (h *Host) ConnectTools(ctx context.Context) error { return h.control(ctx, "/mcp/design/connect") }
func (h *Host) WaitIdle(ctx context.Context, session string) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		raw, e := h.read(ctx, "/session/status")
		if e != nil {
			return e
		}
		var status map[string]struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &status) != nil {
			return errors.New("invalid native status")
		}
		if v, ok := status[session]; !ok || v.Type == "idle" {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (h *Host) ToolsConnected(ctx context.Context) bool {
	if h == nil || len(h.tools) == 0 {
		return false
	}
	raw, e := h.read(ctx, "/mcp")
	if e != nil {
		return false
	}
	var statuses map[string]struct {
		Status string `json:"status"`
	}
	return json.Unmarshal(raw, &statuses) == nil && statuses["design"].Status == "connected"
}
