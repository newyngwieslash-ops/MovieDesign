package updates_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/updates"
)

type policyRoundTripper func(*http.Request) (*http.Response, error)

func (f policyRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPolicyRetryOnly5xxWithLKGFallback(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "policy_wire_fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name         string
		status       int
		body         string
		transportErr error
		readErr      error
		deadline     bool
		cancelled    bool
		succeedAfter int
		wantCalls    int
		wantReason   string
	}{
		{name: "transport_error", transportErr: errors.New("offline"), wantCalls: 1, wantReason: "network_error"},
		{name: "request_deadline", deadline: true, wantCalls: 1, wantReason: "timeout"},
		{name: "body_read_error", status: 200, readErr: errors.New("body read failed"), wantCalls: 1, wantReason: "network_error"},
		{name: "truncated_body", status: 200, readErr: io.ErrUnexpectedEOF, wantCalls: 1, wantReason: "network_error"},
		{name: "404", status: 404, wantCalls: 1, wantReason: "http_404"},
		{name: "invalid_schema", status: 200, body: `{"schema_version":999}`, wantCalls: 1, wantReason: "invalid_schema"},
		{name: "oversized_policy", status: 200, body: strings.Repeat(" ", 256*1024+1), wantCalls: 1, wantReason: "invalid_schema"},
		{name: "cancelled_parent", cancelled: true, wantCalls: 1, wantReason: "network_error"},
		{name: "503_then_success", status: 503, succeedAfter: 2, wantCalls: 3},
		{name: "503_exhausted", status: 503, wantCalls: 3, wantReason: "http_503"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
			svc := updates.NewService(t.TempDir())
			svc.PolicyURL = "https://policy.invalid/policy.json"
			svc.Now = func() time.Time { return now }
			svc.DisableBackgroundRefresh = true
			svc.Client = &http.Client{Transport: policyRoundTripper(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(fixture)))}, nil
			})}
			opts := updates.EvalOptions{CurrentVersion: "1.2.0", Platform: "windows", PreferCache: true}
			if snap := svc.Evaluate(context.Background(), opts); snap.Source != "cdn" || snap.Status != "ready" {
				t.Fatalf("seed LKG: %+v", snap)
			}
			now = now.Add(25 * time.Hour)
			calls := 0
			var delays []time.Duration
			svc.Sleep = func(ctx context.Context, delay time.Duration) error {
				delays = append(delays, delay)
				return ctx.Err()
			}
			svc.Client.Transport = policyRoundTripper(func(req *http.Request) (*http.Response, error) {
				calls++
				deadline, ok := req.Context().Deadline()
				if !ok || time.Until(deadline) > 3*time.Second {
					t.Fatalf("request must have a deadline within 3 seconds, got %v, %v", deadline, ok)
				}
				if tt.deadline {
					<-req.Context().Done()
					return nil, req.Context().Err()
				}
				if tt.cancelled {
					return nil, req.Context().Err()
				}
				if tt.transportErr != nil {
					return nil, tt.transportErr
				}
				if tt.succeedAfter > 0 && calls > tt.succeedAfter {
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(fixture)))}, nil
				}
				var body io.Reader = strings.NewReader(tt.body)
				if tt.readErr != nil {
					body = iotest.ErrReader(tt.readErr)
				}
				return &http.Response{StatusCode: tt.status, Body: io.NopCloser(body)}, nil
			})
			ctx := context.Background()
			if tt.deadline {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, time.Millisecond)
				defer cancel()
			}
			if tt.cancelled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			snap := svc.Evaluate(ctx, opts)
			if calls != tt.wantCalls {
				t.Errorf("requests = %d, want %d", calls, tt.wantCalls)
			}
			var wantDelays []time.Duration
			if tt.wantCalls == 3 {
				wantDelays = []time.Duration{250 * time.Millisecond, 500 * time.Millisecond}
			}
			if !reflect.DeepEqual(delays, wantDelays) {
				t.Errorf("delays = %v, want %v", delays, wantDelays)
			}
			if tt.wantReason == "" {
				if snap.Status != "ready" || snap.Source != "cdn" || snap.Stale || snap.PolicyFallbackReason != nil {
					t.Fatalf("expected successful refresh: %+v", snap)
				}
			} else if snap.Status != "ready" || snap.Source != "cache" || !snap.Stale || snap.Policy == nil || snap.PolicyFallbackReason == nil || *snap.PolicyFallbackReason != tt.wantReason || snap.PolicyCacheAgeMs == nil || *snap.PolicyCacheAgeMs != (25*time.Hour).Milliseconds() {
				t.Fatalf("expected stale LKG with reason %q: %+v", tt.wantReason, snap)
			}
		})
	}
}
