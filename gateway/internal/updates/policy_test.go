package updates_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/server"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/updates"
)

func TestParseCDNPolicyAndManualOnlyMatching(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "policy_wire_fixture.json"))
	if err != nil {
		t.Fatalf("read wire fixture: %v", err)
	}
	policy, warnings, err := updates.ParseCDNPolicy(raw)
	if err != nil {
		t.Fatalf("ParseCDNPolicy failed: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if policy.SchemaVersion != 1 || !policy.Enabled || policy.MinSupportedVersion != "1.4.0" {
		t.Fatalf("unexpected parsed policy header: %+v", policy)
	}
	if policy.ManualOnly == nil || len(policy.ManualOnly.Platforms) != 2 {
		t.Fatalf("unexpected manual_only: %+v", policy.ManualOnly)
	}

	// 1. Hit platform ("win32" or Rust "windows" mapped to "win32") and half-open version interval [1.1.0, 1.3.0).
	if !updates.ManualOnlyRuleMatches("1.1.0", policy, "win32") {
		t.Fatalf("expected 1.1.0 on win32 to match inclusive lower bound")
	}
	if !updates.ManualOnlyRuleMatches("1.2.5", policy, "windows") {
		t.Fatalf("expected 1.2.5 on windows (mapped to win32) to match")
	}
	resHit := updates.ResolveUpdatePolicy("1.2.0", policy, "zh-CN", "windows")
	if !resHit.Forced || !resHit.ManualOnly || resHit.TargetVersion != nil {
		t.Fatalf("expected forced manual_only with nil targetVersion, got %+v", resHit)
	}
	if resHit.RequiredReason == nil || *resHit.RequiredReason != "1.1.0 至 1.3.0 之间的版本存在安装器变更，请前往 GitHub Releases 手动下载安装包。" {
		t.Fatalf("unexpected localized manual reason: %+v", resHit.RequiredReason)
	}

	// 2. Miss at max_version_exclusive (1.3.0 is outside [1.1.0, 1.3.0), but still < min_supported_version 1.4.0).
	if updates.ManualOnlyRuleMatches("1.3.0", policy, "win32") {
		t.Fatalf("expected 1.3.0 to miss half-open max_version_exclusive")
	}
	resForcedAuto := updates.ResolveUpdatePolicy("1.3.0", policy, "en-US", "win32")
	if !resForcedAuto.Forced || resForcedAuto.ManualOnly {
		t.Fatalf("expected 1.3.0 to be forced=true, manualOnly=false, got %+v", resForcedAuto)
	}
	if resForcedAuto.RequiredReason == nil || *resForcedAuto.RequiredReason != "Your version is outdated. Please update immediately." {
		t.Fatalf("unexpected English force_reason: %+v", resForcedAuto.RequiredReason)
	}

	// 3. Miss by platform ("linux" not in ["win32", "darwin"]).
	if updates.ManualOnlyRuleMatches("1.2.0", policy, "linux") {
		t.Fatalf("expected linux to miss manual_only platforms")
	}

	// 4. Current version >= min_supported_version: neither forced nor manual_only.
	resCurrent := updates.ResolveUpdatePolicy("1.4.0", policy, "zh-CN", "win32")
	if resCurrent.Forced || resCurrent.ManualOnly || resCurrent.RequiredReason != nil || resCurrent.ManualDownloadURL != nil {
		t.Fatalf("expected 1.4.0 to require no forced/manual update, got %+v", resCurrent)
	}

	// 5. enabled=false disables forced/manual_only rules even for older versions.
	disabled := *policy
	disabled.Enabled = false
	resDisabled := updates.ResolveUpdatePolicy("1.2.0", &disabled, "zh-CN", "win32")
	if resDisabled.Forced || resDisabled.ManualOnly {
		t.Fatalf("expected disabled policy to return forced=false, manualOnly=false, got %+v", resDisabled)
	}

	// 6. Empty manual_only rule does not match.
	emptyRuleRaw := []byte(`{
		"schema_version": 1,
		"enabled": true,
		"min_supported_version": "1.4.0",
		"manual_download_url": "https://github.com/newyngwieslash-ops/MovieDesign/releases/latest",
		"manual_only": {}
	}`)
	emptyRulePolicy, _, err := updates.ParseCDNPolicy(emptyRuleRaw)
	if err != nil {
		t.Fatalf("expected empty manual_only object to parse, got %v", err)
	}
	if emptyRulePolicy.ManualOnly != nil || updates.ManualOnlyRuleMatches("1.2.0", emptyRulePolicy, "win32") {
		t.Fatalf("expected empty manual_only rule not to match")
	}

	// 7. Invalid manual_download_url warns and is dropped without failing ParseCDNPolicy.
	badURLPolicyRaw := []byte(`{
		"schema_version": 1,
		"enabled": true,
		"min_supported_version": "1.4.0",
		"manual_download_url": "javascript:alert(1)",
		"manual_only": {
			"platforms": ["win32"],
			"max_version_exclusive": "1.3.0"
		}
	}`)
	badURLPolicy, warnList, err := updates.ParseCDNPolicy(badURLPolicyRaw)
	if err != nil {
		t.Fatalf("expected invalid manual_download_url not to fail ParseCDNPolicy, got %v", err)
	}
	if len(warnList) == 0 || badURLPolicy.ManualDownloadURL != nil {
		t.Fatalf("expected warning and nil ManualDownloadURL, got warnings=%v url=%v", warnList, badURLPolicy.ManualDownloadURL)
	}
	if updates.ManualOnlyRuleMatches("1.2.0", badURLPolicy, "win32") {
		t.Fatalf("expected missing manual_download_url to prevent manual_only match")
	}

	// 8. max_version_exclusive > min_supported_version is invalid and must not match.
	invalidRangeRaw := []byte(`{
		"schema_version": 1,
		"enabled": true,
		"min_supported_version": "1.4.0",
		"manual_download_url": "https://github.com/newyngwieslash-ops/MovieDesign/releases/latest",
		"manual_only": {
			"platforms": ["win32"],
			"min_version_inclusive": "1.2.0",
			"max_version_exclusive": "1.5.0"
		}
	}`)
	invalidRangePolicy, _, err := updates.ParseCDNPolicy(invalidRangeRaw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updates.ManualOnlyRuleMatches("1.3.0", invalidRangePolicy, "win32") {
		t.Fatalf("expected max_version_exclusive > min_supported_version not to match")
	}

	// 9. Unsupported platform entry in platforms array invalidates the manual_only rule.
	invalidPlatformRaw := []byte(`{
		"schema_version": 1,
		"enabled": true,
		"min_supported_version": "1.4.0",
		"manual_download_url": "https://github.com/newyngwieslash-ops/MovieDesign/releases/latest",
		"manual_only": {
			"platforms": ["win32", "android"],
			"max_version_exclusive": "1.3.0"
		}
	}`)
	invalidPlatformPolicy, _, err := updates.ParseCDNPolicy(invalidPlatformRaw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updates.ManualOnlyRuleMatches("1.2.0", invalidPlatformPolicy, "win32") {
		t.Fatalf("expected unknown platform in rule.platforms to invalidate rule")
	}

	// 10. Corrupted JSON or wrong schema_version fails ParseCDNPolicy.
	if _, _, err := updates.ParseCDNPolicy([]byte(`{"schema_version": 2, "enabled": true, "min_supported_version": "1.0.0"}`)); err == nil {
		t.Fatalf("expected schema_version 2 to fail")
	}
	if _, _, err := updates.ParseCDNPolicy([]byte(`{corrupted json`)); err == nil {
		t.Fatalf("expected corrupted JSON to fail")
	}
}

func TestCacheFresh24HourBoundaryAndClockRollback(t *testing.T) {
	fetched := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	if !updates.CacheFresh(fetched, fetched) {
		t.Fatalf("expected age=0 to be fresh")
	}
	if !updates.CacheFresh(fetched.Add(24*time.Hour), fetched) {
		t.Fatalf("expected exact 24h boundary to be fresh")
	}
	if updates.CacheFresh(fetched.Add(24*time.Hour+time.Millisecond), fetched) {
		t.Fatalf("expected 24h + 1ms to be stale")
	}
	if updates.CacheFresh(fetched.Add(-time.Second), fetched) {
		t.Fatalf("expected negative age (clock rollback / future timestamp) to be treated as stale")
	}
}

func TestServiceRetries5xxUses24hCacheAndFallsBackToLKG(t *testing.T) {
	fixtureBytes, err := os.ReadFile(filepath.Join("testdata", "policy_wire_fixture.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var attempts atomic.Int32
	var mode atomic.Int32 // 0: fail twice with 502 then 200; 1: always 503; 2: corrupt JSON
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		switch mode.Load() {
		case 0:
			if n < 3 {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(fixtureBytes)
		case 1:
			w.WriteHeader(http.StatusServiceUnavailable)
		case 2:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"schema_version": 999}`))
		}
	}))
	defer srv.Close()

	cacheDir := t.TempDir()
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	var delays []time.Duration
	svc := updates.NewService(cacheDir)
	svc.PolicyURL = srv.URL
	svc.Now = func() time.Time { return now }
	svc.Sleep = func(_ context.Context, d time.Duration) error {
		delays = append(delays, d)
		return nil
	}

	// 1. Cold start with no LKG cache and upstream 503 -> retries 3 times with 250ms, 500ms backoff, returns status=failed, source=none.
	mode.Store(1)
	snapFailed := svc.Evaluate(context.Background(), updates.EvalOptions{
		CurrentVersion: "1.2.0",
		Platform:       "windows",
		Locale:         "zh-CN",
		ForceRefresh:   true,
	})
	if snapFailed.Status != "failed" || snapFailed.Source != "none" || snapFailed.AutoUpdateAllowed {
		t.Fatalf("expected status=failed source=none autoUpdateAllowed=false without LKG, got %+v", snapFailed)
	}
	if attempts.Load() != 3 || len(delays) != 2 || delays[0] != 250*time.Millisecond || delays[1] != 500*time.Millisecond {
		t.Fatalf("expected 3 attempts and [250ms, 500ms] delays, got attempts=%d delays=%v", attempts.Load(), delays)
	}
	if snapFailed.Resolved.ManualOnly {
		t.Fatalf("must not fabricate server manual_only when policy fetch fails without LKG")
	}

	// 2. Upstream fails twice with 502 then succeeds on 3rd attempt -> atomic cache write, status=ready, source=cdn.
	attempts.Store(0)
	delays = nil
	mode.Store(0)
	snapCDN := svc.Evaluate(context.Background(), updates.EvalOptions{
		CurrentVersion: "1.2.0",
		Platform:       "windows",
		Locale:         "zh-CN",
		ForceRefresh:   true,
	})
	if snapCDN.Status != "ready" || snapCDN.Source != "cdn" || snapCDN.Stale {
		t.Fatalf("expected ready from cdn, got %+v", snapCDN)
	}
	if !snapCDN.Resolved.ManualOnly || snapCDN.AutoUpdateAllowed {
		t.Fatalf("expected manual_only=true and autoUpdateAllowed=false for 1.2.0 on win32, got %+v", snapCDN)
	}

	// 3. Within 24h (e.g. +12h), preferCache uses fresh cache without blocking on failed CDN.
	now = now.Add(12 * time.Hour)
	mode.Store(1)
	attempts.Store(0)
	svc.DisableBackgroundRefresh = true
	snapFreshCache := svc.Evaluate(context.Background(), updates.EvalOptions{
		CurrentVersion: "1.4.0",
		Platform:       "windows",
		Locale:         "zh-CN",
		PreferCache:    true,
		SafeToInstall:  true,
	})
	if snapFreshCache.Status != "ready" || snapFreshCache.Source != "cache" || snapFreshCache.Stale {
		t.Fatalf("expected fresh cache hit, got %+v", snapFreshCache)
	}
	if !snapFreshCache.AutoUpdateAllowed {
		t.Fatalf("expected 1.4.0 with ready policy and idle gateway to allow auto update, got %+v", snapFreshCache)
	}
	if attempts.Load() != 0 {
		t.Fatalf("expected zero synchronous CDN calls when cache is fresh, got %d", attempts.Load())
	}

	// 4. Busy gateway refuses auto-install even when policy is ready and not manual_only.
	snapBusy := svc.Evaluate(context.Background(), updates.EvalOptions{
		CurrentVersion: "1.4.0",
		Platform:       "windows",
		Locale:         "zh-CN",
		PreferCache:    true,
		SafeToInstall:  false,
		BusyReason:     "存在正在执行的生成任务，生成期间不自动安装更新或重启",
	})
	if snapBusy.AutoUpdateAllowed || !snapBusy.Busy {
		t.Fatalf("expected busy gateway to block auto-install, got %+v", snapBusy)
	}

	// 5. After 24h (+25h), cache is stale: attempts CDN refresh first; when CDN returns corrupt schema (non-retryable, 1 attempt), falls back to metadata-matching LKG cache with stale=true.
	now = now.Add(13 * time.Hour) // total 25h since fetch
	mode.Store(2)
	attempts.Store(0)
	snapStaleLKG := svc.Evaluate(context.Background(), updates.EvalOptions{
		CurrentVersion: "1.2.0",
		Platform:       "windows",
		Locale:         "zh-CN",
		PreferCache:    true,
		SafeToInstall:  true,
	})
	if attempts.Load() != 1 {
		t.Fatalf("expected invalid_schema not to retry (1 attempt), got %d", attempts.Load())
	}
	if snapStaleLKG.Status != "ready" || snapStaleLKG.Source != "cache" || !snapStaleLKG.Stale {
		t.Fatalf("expected stale LKG cache fallback, got %+v", snapStaleLKG)
	}
	if snapStaleLKG.PolicyFallbackReason == nil || *snapStaleLKG.PolicyFallbackReason != "invalid_schema" {
		t.Fatalf("expected fallback reason invalid_schema, got %+v", snapStaleLKG.PolicyFallbackReason)
	}
	if !snapStaleLKG.Resolved.ManualOnly {
		t.Fatalf("expected LKG manual_only rule to still apply, got %+v", snapStaleLKG.Resolved)
	}

	// 6. Clock rollback (now < cachedAt) marks cache as stale and clockSkew=true; if CDN fails, still applies LKG with stale=true and clockSkew=true.
	now = time.Date(2026, 10, 15, 11, 0, 0, 0, time.UTC) // 1 hour before cachedAt
	snapClockSkew := svc.Evaluate(context.Background(), updates.EvalOptions{
		CurrentVersion: "1.2.0",
		Platform:       "windows",
		Locale:         "zh-CN",
		PreferCache:    true,
	})
	if !snapClockSkew.Stale || !snapClockSkew.ClockSkew || snapClockSkew.Source != "cache" {
		t.Fatalf("expected clock rollback to mark cache stale + clockSkew, got %+v", snapClockSkew)
	}

	// 7. Metadata mismatch (e.g. different platform/channel in cache) discards cache and returns status=failed when CDN fails.
	snapMismatch := svc.Evaluate(context.Background(), updates.EvalOptions{
		CurrentVersion: "1.2.0",
		Platform:       "darwin",
		Locale:         "zh-CN",
		PreferCache:    true,
	})
	if snapMismatch.Status != "failed" || snapMismatch.Source != "none" {
		t.Fatalf("expected metadata-mismatched cache to be discarded, got %+v", snapMismatch)
	}
}

func TestHTTPUpdatesPolicyAndIdempotentCheckEvent(t *testing.T) {
	fixtureBytes, err := os.ReadFile(filepath.Join("testdata", "policy_wire_fixture.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var cdnHits atomic.Int32
	cdnSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cdnHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixtureBytes)
	}))
	defer cdnSrv.Close()

	root := t.TempDir()
	wsRoot := filepath.Join(root, "workspace")
	if err := os.MkdirAll(wsRoot, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	db, err := store.Open(filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer db.Close()

	origin := "http://tauri.localhost"
	srv, err := server.New(origin, db, wsRoot)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	defer srv.Close()
	srv.Updates().PolicyURL = cdnSrv.URL
	srv.Updates().DisableBackgroundRefresh = true

	// Create a project to verify project-scoped app_update_check event recording.
	projReq := httptest.NewRequest(http.MethodPost, "/api/projects", bytes.NewBufferString(`{"requestId":"proj-upd-1","action":"create","value":{"name":"更新测试项目"}}`))
	projReq.Header.Set("Origin", origin)
	projRec := httptest.NewRecorder()
	srv.ServeHTTP(projRec, projReq)
	if projRec.Code != http.StatusOK {
		t.Fatalf("create project failed: %d %s", projRec.Code, projRec.Body.String())
	}
	var proj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(projRec.Body.Bytes(), &proj); err != nil {
		t.Fatalf("decode project: %v", err)
	}

	// 1. GET /api/updates/policy without Origin is rejected (403).
	noOriginReq := httptest.NewRequest(http.MethodGet, "/api/updates/policy", nil)
	noOriginRec := httptest.NewRecorder()
	srv.ServeHTTP(noOriginRec, noOriginReq)
	if noOriginRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without Origin, got %d", noOriginRec.Code)
	}

	// 2. GET /api/updates/policy returns computed manual_only/reason/cache state.
	getReq := httptest.NewRequest(http.MethodGet, "/api/updates/policy?version=1.2.0&platform=windows&locale=zh-CN", nil)
	getReq.Header.Set("Origin", origin)
	getRec := httptest.NewRecorder()
	srv.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET /api/updates/policy failed: %d %s", getRec.Code, getRec.Body.String())
	}
	var getSnap updates.PolicySnapshot
	if err := json.Unmarshal(getRec.Body.Bytes(), &getSnap); err != nil {
		t.Fatalf("decode GET snapshot: %v", err)
	}
	if getSnap.Status != "ready" || getSnap.Source != "cdn" || !getSnap.Resolved.ManualOnly || getSnap.AutoUpdateAllowed {
		t.Fatalf("unexpected GET policy snapshot: %+v", getSnap)
	}

	// 3. POST /api/updates/check records requestId receipt and app_update_check event, and replays idempotently without re-fetching CDN.
	hitsBeforeCheck := cdnHits.Load()
	checkBody := `{"requestId":"upd-check-1","version":"1.4.0","platform":"windows","locale":"zh-CN","refresh":true}`
	postReq := httptest.NewRequest(http.MethodPost, "/api/updates/check?project_id="+proj.ID, bytes.NewBufferString(checkBody))
	postReq.Header.Set("Origin", origin)
	postRec := httptest.NewRecorder()
	srv.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST /api/updates/check failed: %d %s", postRec.Code, postRec.Body.String())
	}
	var checkSnap updates.PolicySnapshot
	if err := json.Unmarshal(postRec.Body.Bytes(), &checkSnap); err != nil {
		t.Fatalf("decode check snapshot: %v", err)
	}
	if checkSnap.Status != "ready" || checkSnap.Resolved.ManualOnly || !checkSnap.AutoUpdateAllowed || checkSnap.Busy {
		t.Fatalf("expected ready & autoUpdateAllowed for 1.4.0 on idle gateway, got %+v", checkSnap)
	}
	if cdnHits.Load() != hitsBeforeCheck+1 {
		t.Fatalf("expected 1 CDN fetch for fresh check, got delta %d", cdnHits.Load()-hitsBeforeCheck)
	}

	// Replay same requestId + same body -> 200 OK with zero additional CDN hits and still 1 app_update_check event.
	replayReq := httptest.NewRequest(http.MethodPost, "/api/updates/check?project_id="+proj.ID, bytes.NewBufferString(checkBody))
	replayReq.Header.Set("Origin", origin)
	replayRec := httptest.NewRecorder()
	srv.ServeHTTP(replayRec, replayReq)
	if replayRec.Code != http.StatusOK || replayRec.Body.String() != postRec.Body.String() {
		t.Fatalf("expected exact idempotent replay, got %d %s", replayRec.Code, replayRec.Body.String())
	}
	if cdnHits.Load() != hitsBeforeCheck+1 {
		t.Fatalf("expected zero additional CDN hits on idempotent replay")
	}

	// Same requestId with different input -> 409 Conflict.
	conflictReq := httptest.NewRequest(http.MethodPost, "/api/updates/check?project_id="+proj.ID, bytes.NewBufferString(`{"requestId":"upd-check-1","version":"1.2.0","platform":"windows","locale":"zh-CN","refresh":true}`))
	conflictReq.Header.Set("Origin", origin)
	conflictRec := httptest.NewRecorder()
	srv.ServeHTTP(conflictRec, conflictReq)
	if conflictRec.Code != http.StatusConflict {
		t.Fatalf("expected 409 on requestId conflict, got %d %s", conflictRec.Code, conflictRec.Body.String())
	}

	// Verify app_update_check event was persisted in SQLite events table.
	var eventCount int
	var payloadJSON string
	if err := db.QueryRow(`SELECT count(*), coalesce(max(payload_json),'') FROM events WHERE project_id=? AND type='app_update_check'`, proj.ID).Scan(&eventCount, &payloadJSON); err != nil {
		t.Fatalf("query app_update_check event: %v", err)
	}
	if eventCount != 1 || payloadJSON == "" {
		t.Fatalf("expected exactly 1 app_update_check event, got count=%d payload=%s", eventCount, payloadJSON)
	}

	// 4. Busy gateway (e.g. active task attempt or pending write_request) sets busy=true and autoUpdateAllowed=false.
	if _, err := db.Exec(`INSERT INTO write_requests(project_id,operation,request_id,input_json,state) VALUES(?,?,?,?,'pending')`, proj.ID, "busy.op", "busy-req-1", `{}`); err != nil {
		t.Fatalf("insert pending write_request: %v", err)
	}
	busyReq := httptest.NewRequest(http.MethodGet, "/api/updates/policy?version=1.4.0&platform=windows", nil)
	busyReq.Header.Set("Origin", origin)
	busyRec := httptest.NewRecorder()
	srv.ServeHTTP(busyRec, busyReq)
	if busyRec.Code != http.StatusOK {
		t.Fatalf("GET /api/updates/policy during busy failed: %d", busyRec.Code)
	}
	var busySnap updates.PolicySnapshot
	if err := json.Unmarshal(busyRec.Body.Bytes(), &busySnap); err != nil {
		t.Fatalf("decode busy snapshot: %v", err)
	}
	if !busySnap.Busy || busySnap.AutoUpdateAllowed {
		t.Fatalf("expected busy=true and autoUpdateAllowed=false when pending operation exists, got %+v", busySnap)
	}
}

func TestSimulatedVersionUpgradePreservesDataVersionsApprovalsAndIdempotentReceipts(t *testing.T) {
	policyBytes, err := os.ReadFile(filepath.Join("..", "..", "..", "updates", "stable", "policy.json"))
	if err != nil {
		t.Fatalf("read repo policy.json: %v", err)
	}
	cdnSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(policyBytes)
	}))
	defer cdnSrv.Close()

	dataDir := t.TempDir()
	wsRoot := filepath.Join(dataDir, "workspace")
	if err := os.MkdirAll(wsRoot, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	dbPath := filepath.Join(dataDir, "moviedesign.db")
	origin := "http://tauri.localhost"

	// Phase 1: Old version (0.0.9 -> hits manual_only in updates/stable/policy.json) creates project, creative artifact, human approval, and idempotent receipt.
	db1, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open phase 1: %v", err)
	}
	srv1, err := server.New(origin, db1, wsRoot)
	if err != nil {
		t.Fatalf("server.New phase 1: %v", err)
	}
	srv1.Updates().PolicyURL = cdnSrv.URL
	srv1.Updates().DisableBackgroundRefresh = true

	// Check policy on 0.0.9 -> manual_only=true, autoUpdateAllowed=false
	oldPolReq := httptest.NewRequest(http.MethodGet, "/api/updates/policy?version=0.0.9&platform=windows&refresh=1", nil)
	oldPolReq.Header.Set("Origin", origin)
	oldPolRec := httptest.NewRecorder()
	srv1.ServeHTTP(oldPolRec, oldPolReq)
	if oldPolRec.Code != http.StatusOK {
		t.Fatalf("phase 1 policy GET failed: %d", oldPolRec.Code)
	}
	var oldSnap updates.PolicySnapshot
	if err := json.Unmarshal(oldPolRec.Body.Bytes(), &oldSnap); err != nil {
		t.Fatalf("decode oldSnap: %v", err)
	}
	if !oldSnap.Resolved.Forced || !oldSnap.Resolved.ManualOnly || oldSnap.AutoUpdateAllowed {
		t.Fatalf("expected 0.0.9 to hit manual_only and block auto download/install, got %+v", oldSnap)
	}

	// Create project & intent document + human approval on old version
	createProjBody := `{"requestId":"upg-proj-1","action":"create","value":{"name":"升级保留测试"}}`
	projReq := httptest.NewRequest(http.MethodPost, "/api/projects", bytes.NewBufferString(createProjBody))
	projReq.Header.Set("Origin", origin)
	projRec := httptest.NewRecorder()
	srv1.ServeHTTP(projRec, projReq)
	if projRec.Code != http.StatusOK {
		t.Fatalf("create project failed: %d %s", projRec.Code, projRec.Body.String())
	}
	var createdProj struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(projRec.Body.Bytes(), &createdProj)

	intentBody := `{"requestId":"upg-intent-1","mode":"create","format":"md","content":"# intent\n\n保持15秒口播升级不丢数据","typedDocument":{"kind":"intent","pipelineId":"pipe-upg-1","expectedRevision":0,"content":"保持15秒口播升级不丢数据"},"output":{"resources":[{"key":"intent","mode":"create","name":"intent.md"}],"composition":{"name":"升级作品","type":{"label":"短视频"},"placements":[{"key":"intent","role":"意图"}]}}}`
	docReq := httptest.NewRequest(http.MethodPost, "/api/documents/write?project_id="+createdProj.ID, bytes.NewBufferString(intentBody))
	docReq.Header.Set("Origin", origin)
	docRec := httptest.NewRecorder()
	srv1.ServeHTTP(docRec, docReq)
	if docRec.Code != http.StatusOK {
		t.Fatalf("write intent failed: %d %s", docRec.Code, docRec.Body.String())
	}
	var docResp struct {
		Delivery struct {
			Versions []struct {
				VersionID string `json:"versionId"`
			} `json:"versions"`
		} `json:"delivery"`
	}
	_ = json.Unmarshal(docRec.Body.Bytes(), &docResp)
	intentVersionID := docResp.Delivery.Versions[0].VersionID

	// Propose and human-approve the intent card
	artReq := httptest.NewRequest(http.MethodGet, "/api/pipeline/artifact?project_id="+createdProj.ID+"&versionId="+intentVersionID, nil)
	artReq.Header.Set("Origin", origin)
	artRec := httptest.NewRecorder()
	srv1.ServeHTTP(artRec, artReq)
	var binding json.RawMessage = artRec.Body.Bytes()

	qBody, _ := json.Marshal(map[string]any{
		"requestId":  "upg-q-1",
		"session_id": "pipeline",
		"kind":       "creative",
		"creative":   binding,
	})
	qReq := httptest.NewRequest(http.MethodPost, "/api/question?project_id="+createdProj.ID, bytes.NewReader(qBody))
	qReq.Header.Set("Origin", origin)
	qRec := httptest.NewRecorder()
	srv1.ServeHTTP(qRec, qReq)
	if qRec.Code != http.StatusOK {
		t.Fatalf("create creative question failed: %d %s", qRec.Code, qRec.Body.String())
	}
	var qObj struct {
		ID       string `json:"id"`
		Revision int    `json:"revision"`
	}
	_ = json.Unmarshal(qRec.Body.Bytes(), &qObj)

	replyBody, _ := json.Marshal(map[string]any{
		"requestId":         "upg-reply-1",
		"expected_revision": qObj.Revision,
		"answers":           [][]string{{"批准意图"}},
	})
	replyReq := httptest.NewRequest(http.MethodPost, "/api/question/"+qObj.ID+"/reply?project_id="+createdProj.ID+"&session_id=pipeline", bytes.NewReader(replyBody))
	replyReq.Header.Set("Origin", origin)
	replyRec := httptest.NewRecorder()
	srv1.ServeHTTP(replyRec, replyReq)
	if replyRec.Code != http.StatusOK {
		t.Fatalf("approve intent failed: %d %s", replyRec.Code, replyRec.Body.String())
	}

	_ = srv1.Close()
	_ = db1.Close()

	// Phase 2: Reopen same dataDir/database after upgrading to version 0.1.0 -> verify data, version, approval, and idempotent receipts are preserved.
	db2, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open phase 2: %v", err)
	}
	defer db2.Close()
	srv2, err := server.New(origin, db2, wsRoot)
	if err != nil {
		t.Fatalf("server.New phase 2: %v", err)
	}
	defer srv2.Close()
	srv2.Updates().PolicyURL = cdnSrv.URL
	srv2.Updates().DisableBackgroundRefresh = true

	// Replaying the old project creation requestId returns the exact receipt without creating a duplicate project.
	projReplayReq := httptest.NewRequest(http.MethodPost, "/api/projects", bytes.NewBufferString(createProjBody))
	projReplayReq.Header.Set("Origin", origin)
	projReplayRec := httptest.NewRecorder()
	srv2.ServeHTTP(projReplayRec, projReplayReq)
	if projReplayRec.Code != http.StatusOK || projReplayRec.Body.String() != projRec.Body.String() {
		t.Fatalf("expected idempotent project receipt to survive upgrade, got %d %s", projReplayRec.Code, projReplayRec.Body.String())
	}

	// Verify human approval on intent is still valid=1 in SQLite after upgrade.
	var validApprovals int
	if err := db2.QueryRow(`SELECT count(*) FROM approvals WHERE pipeline_id='pipe-upg-1' AND gate='intent' AND document_version_id=? AND valid=1`, intentVersionID).Scan(&validApprovals); err != nil {
		t.Fatalf("query approvals after upgrade: %v", err)
	}
	if validApprovals != 1 {
		t.Fatalf("expected valid=1 intent approval to survive upgrade, got %d", validApprovals)
	}

	// Verify 0.1.0 policy is ready, uses the cached policy from phase 1, and allows signed updates when idle.
	newPolReq := httptest.NewRequest(http.MethodGet, "/api/updates/policy?version=0.1.0&platform=windows", nil)
	newPolReq.Header.Set("Origin", origin)
	newPolRec := httptest.NewRecorder()
	srv2.ServeHTTP(newPolRec, newPolReq)
	var newSnap updates.PolicySnapshot
	_ = json.Unmarshal(newPolRec.Body.Bytes(), &newSnap)
	if newSnap.Status != "ready" || newSnap.Source != "cache" || newSnap.Resolved.ManualOnly || !newSnap.AutoUpdateAllowed {
		t.Fatalf("expected upgraded 0.1.0 to read 24h cache and allow signed update, got %+v", newSnap)
	}
}
