package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var errNativeRejected = errors.New("native request rejected")
var errNativeUnknown = errors.New("native acceptance unknown")

func (h *Host) request(ctx context.Context, method, path string, body any) ([]byte, error) {
	budget := 10 * time.Second
	if strings.HasSuffix(path, "/prompt_async") {
		budget = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	var reader io.Reader
	if body != nil {
		raw, e := json.Marshal(body)
		if e != nil {
			return nil, e
		}
		reader = bytes.NewReader(raw)
	}
	req, e := http.NewRequestWithContext(ctx, method, h.base+path, reader)
	if e != nil {
		return nil, e
	}
	req.GetBody = nil
	req.SetBasicAuth("opencode", h.password)
	req.Header.Set("Content-Type", "application/json")
	client := *h.client
	client.Timeout = 0
	response, e := client.Do(req)
	if e != nil {
		return nil, errNativeUnknown
	}
	defer response.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(response.Body, inferenceLimit+1))
	if e != nil || len(raw) > inferenceLimit {
		return nil, errNativeUnknown
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if response.StatusCode >= 400 && response.StatusCode < 500 {
			return nil, errNativeRejected
		}
		return nil, errNativeUnknown
	}
	return raw, nil
}

func (h *Host) snapshot(ctx context.Context, session string) ([]nativeMessage, error) {
	raw, e := h.request(ctx, "GET", "/session/"+url.PathEscape(session)+"/message", nil)
	if e != nil {
		return nil, e
	}
	var messages []nativeMessage
	e = json.Unmarshal(raw, &messages)
	return messages, e
}
func (h *Host) pendingQuestions(ctx context.Context) ([]NativeQuestion, error) {
	raw, e := h.request(ctx, "GET", "/question", nil)
	if e != nil {
		return nil, e
	}
	var questions []NativeQuestion
	e = json.Unmarshal(raw, &questions)
	return questions, e
}
