package updates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultPolicyURL   = "https://raw.githubusercontent.com/newyngwieslash-ops/MovieDesign/main/updates/stable/policy.json"
	DefaultManifestURL = "https://github.com/newyngwieslash-ops/MovieDesign/releases/latest/download/latest.json"
	DefaultReleasesURL = "https://github.com/newyngwieslash-ops/MovieDesign/releases/latest"
	DefaultAppVersion  = "0.1.0"
	DefaultRegion      = "global"
	DefaultChannel     = "stable"
	DefaultPlatform    = "win32"
	DefaultLocale      = "zh-CN"

	CacheFileName    = "update-policy-cache.json"
	CacheTTL         = 24 * time.Hour
	FetchTimeout     = 3 * time.Second
	MaxFetchAttempts = 3
	RetryBaseDelay   = 250 * time.Millisecond
	RetryMaxDelay    = 1000 * time.Millisecond
	maxPolicyBytes   = 256 * 1024
)

var ErrInvalidPolicySchema = errors.New("invalid update policy schema")

func CacheFresh(now, fetched time.Time) bool {
	age := now.Sub(fetched)
	return age >= 0 && age <= 24*time.Hour
}

type LocalizedText struct {
	Zh string `json:"zh"`
	En string `json:"en"`
}

type ManualOnlyRule struct {
	Platforms           []string       `json:"platforms,omitempty"`
	MinVersionInclusive *string        `json:"min_version_inclusive,omitempty"`
	MaxVersionExclusive *string        `json:"max_version_exclusive,omitempty"`
	Reason              *LocalizedText `json:"reason,omitempty"`
}

type CDNPolicy struct {
	SchemaVersion       int             `json:"schema_version"`
	Enabled             bool            `json:"enabled"`
	MinSupportedVersion string          `json:"min_supported_version"`
	ForceReason         *LocalizedText  `json:"force_reason,omitempty"`
	ManualDownloadURL   *string         `json:"manual_download_url,omitempty"`
	UpdatedAt           *string         `json:"updated_at,omitempty"`
	ManualOnly          *ManualOnlyRule `json:"manual_only,omitempty"`
}

type CacheMetadata struct {
	Region   string `json:"region"`
	Channel  string `json:"channel"`
	Platform string `json:"platform"`
}

type CachedPolicyPayload struct {
	Policy   CDNPolicy     `json:"policy"`
	CachedAt string        `json:"cachedAt"`
	Metadata CacheMetadata `json:"metadata"`
}

type ResolvedUpdatePolicy struct {
	Forced               bool    `json:"forced"`
	TargetVersion        *string `json:"targetVersion"`
	MinSupportedVersion  string  `json:"minSupportedVersion"`
	RequiredReason       *string `json:"requiredReason"`
	ManualDownloadURL    *string `json:"manualDownloadUrl"`
	ManualOnly           bool    `json:"manualOnly"`
	ManualRecoveryReason *string `json:"manualRecoveryReason"`
}

type PolicySnapshot struct {
	Status               string               `json:"status"`
	Source               string               `json:"source"`
	Stale                bool                 `json:"stale"`
	ClockSkew            bool                 `json:"clockSkew,omitempty"`
	CachedAt             *string              `json:"cachedAt,omitempty"`
	PolicyCacheAgeMs     *int64               `json:"policyCacheAgeMs"`
	PolicyFallbackReason *string              `json:"policyFallbackReason"`
	Warnings             []string             `json:"warnings,omitempty"`
	CurrentVersion       string               `json:"currentVersion"`
	Platform             string               `json:"platform"`
	Region               string               `json:"region"`
	Channel              string               `json:"channel"`
	Locale               string               `json:"locale"`
	PolicyURL            string               `json:"policyUrl"`
	ManifestURL          string               `json:"manifestUrl"`
	ReleasesURL          string               `json:"releasesUrl"`
	Policy               *CDNPolicy           `json:"policy"`
	Resolved             ResolvedUpdatePolicy `json:"resolved"`
	Busy                 bool                 `json:"busy"`
	BusyReason           *string              `json:"busyReason,omitempty"`
	AutoUpdateAllowed    bool                 `json:"autoUpdateAllowed"`
}

func NormalizePlatform(platform string) string {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "", "windows", "win32":
		return "win32"
	case "macos", "darwin":
		return "darwin"
	case "linux":
		return "linux"
	default:
		return strings.ToLower(strings.TrimSpace(platform))
	}
}

func isSupportedManualOnlyPlatform(p string) bool {
	return p == "win32" || p == "darwin" || p == "linux"
}

func normalizeHTTPURL(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}
	u, err := url.Parse(trimmed)
	if err != nil || u.Host == "" {
		return "", false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", false
	}
	return u.String(), true
}

func parseLocalizedReason(raw any) *LocalizedText {
	obj, ok := raw.(map[string]any)
	if !ok || obj == nil {
		return nil
	}
	zhRaw, okZh := obj["zh"].(string)
	enRaw, okEn := obj["en"].(string)
	if !okZh || !okEn {
		return nil
	}
	zh := strings.TrimSpace(zhRaw)
	en := strings.TrimSpace(enRaw)
	if zh == "" || en == "" {
		return nil
	}
	return &LocalizedText{Zh: zh, En: en}
}

func parseManualOnlyRule(raw any) *ManualOnlyRule {
	obj, ok := raw.(map[string]any)
	if !ok || obj == nil {
		return nil
	}
	rule := &ManualOnlyRule{}
	hasAny := false
	if arr, ok := obj["platforms"].([]any); ok {
		var platforms []string
		for _, item := range arr {
			if s, ok := item.(string); ok {
				if trimmed := strings.TrimSpace(s); trimmed != "" {
					platforms = append(platforms, trimmed)
				}
			}
		}
		if len(platforms) > 0 {
			rule.Platforms = platforms
			hasAny = true
		}
	}
	if s, ok := obj["min_version_inclusive"].(string); ok {
		if trimmed := strings.TrimSpace(s); trimmed != "" {
			rule.MinVersionInclusive = &trimmed
			hasAny = true
		}
	}
	if s, ok := obj["max_version_exclusive"].(string); ok {
		if trimmed := strings.TrimSpace(s); trimmed != "" {
			rule.MaxVersionExclusive = &trimmed
			hasAny = true
		}
	}
	if reason := parseLocalizedReason(obj["reason"]); reason != nil {
		rule.Reason = reason
		hasAny = true
	}
	if !hasAny {
		return nil
	}
	return rule
}

// ParseCDNPolicy implements the recovered parseCdnPolicy wire parser from
// extracted/out/main/chunks/update-policy-fetcher-CYQPhXS_.js:79 without renaming fields.
func ParseCDNPolicy(raw []byte) (*CDNPolicy, []string, error) {
	var obj map[string]any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&obj); err != nil || obj == nil {
		return nil, nil, ErrInvalidPolicySchema
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, nil, ErrInvalidPolicySchema
	}

	schemaNum, ok := obj["schema_version"].(json.Number)
	if !ok || schemaNum.String() != "1" {
		return nil, nil, ErrInvalidPolicySchema
	}
	enabled, ok := obj["enabled"].(bool)
	if !ok {
		return nil, nil, ErrInvalidPolicySchema
	}
	minSupportedRaw, ok := obj["min_supported_version"].(string)
	if !ok {
		return nil, nil, ErrInvalidPolicySchema
	}
	minSupported := strings.TrimSpace(minSupportedRaw)
	if minSupported == "" || !ValidSemver(minSupported) {
		return nil, nil, ErrInvalidPolicySchema
	}

	policy := &CDNPolicy{
		SchemaVersion:       1,
		Enabled:             enabled,
		MinSupportedVersion: minSupported,
		ForceReason:         parseLocalizedReason(obj["force_reason"]),
		ManualOnly:          parseManualOnlyRule(obj["manual_only"]),
	}
	var warnings []string
	if urlRaw, exists := obj["manual_download_url"]; exists && urlRaw != nil {
		if urlStr, ok := urlRaw.(string); ok && strings.TrimSpace(urlStr) != "" {
			if normalized, valid := normalizeHTTPURL(urlStr); valid {
				policy.ManualDownloadURL = &normalized
			} else {
				warnings = append(warnings, "Invalid manual_download_url in update policy, ignoring field")
			}
		}
	}
	if updatedRaw, ok := obj["updated_at"].(string); ok {
		if trimmed := strings.TrimSpace(updatedRaw); trimmed != "" {
			policy.UpdatedAt = &trimmed
		}
	}
	return policy, warnings, nil
}

type semverParts struct {
	major int
	minor int
	patch int
	pre   string
}

func parseSemver(raw string) (semverParts, bool) {
	s := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "v"))
	if s == "" {
		return semverParts{}, false
	}
	if idx := strings.IndexByte(s, '+'); idx >= 0 {
		s = s[:idx]
	}
	var pre string
	if idx := strings.IndexByte(s, '-'); idx >= 0 {
		pre = s[idx+1:]
		s = s[:idx]
		if pre == "" {
			return semverParts{}, false
		}
	}
	segs := strings.Split(s, ".")
	if len(segs) != 3 {
		return semverParts{}, false
	}
	maj, err1 := strconv.Atoi(segs[0])
	min, err2 := strconv.Atoi(segs[1])
	pat, err3 := strconv.Atoi(segs[2])
	if err1 != nil || err2 != nil || err3 != nil || maj < 0 || min < 0 || pat < 0 {
		return semverParts{}, false
	}
	return semverParts{major: maj, minor: min, patch: pat, pre: pre}, true
}

func ValidSemver(raw string) bool {
	_, ok := parseSemver(raw)
	return ok
}

func CompareSemver(a, b string) (int, bool) {
	va, okA := parseSemver(a)
	vb, okB := parseSemver(b)
	if !okA || !okB {
		return 0, false
	}
	if va.major != vb.major {
		if va.major < vb.major {
			return -1, true
		}
		return 1, true
	}
	if va.minor != vb.minor {
		if va.minor < vb.minor {
			return -1, true
		}
		return 1, true
	}
	if va.patch != vb.patch {
		if va.patch < vb.patch {
			return -1, true
		}
		return 1, true
	}
	if va.pre == vb.pre {
		return 0, true
	}
	if va.pre == "" {
		return 1, true
	}
	if vb.pre == "" {
		return -1, true
	}
	if va.pre < vb.pre {
		return -1, true
	}
	return 1, true
}

// ManualOnlyRuleMatches ports manualOnlyRuleMatches from extracted/out/main/chunks/index-WiZKxTAT.js:2158.
func ManualOnlyRuleMatches(currentVersion string, policy *CDNPolicy, platform string) bool {
	if policy == nil || policy.ManualOnly == nil || policy.ManualDownloadURL == nil {
		return false
	}
	if _, ok := normalizeHTTPURL(*policy.ManualDownloadURL); !ok {
		return false
	}
	normalizedPlatform := NormalizePlatform(platform)
	rule := policy.ManualOnly
	if len(rule.Platforms) == 0 {
		return false
	}
	platformMatched := false
	for _, item := range rule.Platforms {
		if !isSupportedManualOnlyPlatform(item) {
			return false
		}
		if item == normalizedPlatform {
			platformMatched = true
		}
	}
	if !platformMatched {
		return false
	}
	if rule.MinVersionInclusive != nil && *rule.MinVersionInclusive != "" {
		if !ValidSemver(*rule.MinVersionInclusive) {
			return false
		}
		cmp, ok := CompareSemver(currentVersion, *rule.MinVersionInclusive)
		if !ok || cmp < 0 {
			return false
		}
	}
	if rule.MaxVersionExclusive == nil || *rule.MaxVersionExclusive == "" || !ValidSemver(*rule.MaxVersionExclusive) {
		return false
	}
	maxVsSupported, ok := CompareSemver(*rule.MaxVersionExclusive, policy.MinSupportedVersion)
	if !ok || maxVsSupported > 0 {
		return false
	}
	if rule.MinVersionInclusive != nil && *rule.MinVersionInclusive != "" {
		minVsMax, ok1 := CompareSemver(*rule.MinVersionInclusive, *rule.MaxVersionExclusive)
		minVsSupported, ok2 := CompareSemver(*rule.MinVersionInclusive, policy.MinSupportedVersion)
		if !ok1 || !ok2 || minVsMax >= 0 || minVsSupported >= 0 {
			return false
		}
	}
	currVsMax, ok := CompareSemver(currentVersion, *rule.MaxVersionExclusive)
	return ok && currVsMax < 0
}

func pickLocalizedReason(reason *LocalizedText, locale string) *string {
	if reason == nil {
		return nil
	}
	primary := reason.En
	secondary := reason.Zh
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(locale)), "zh") {
		primary = reason.Zh
		secondary = reason.En
	}
	if strings.TrimSpace(primary) != "" {
		out := strings.TrimSpace(primary)
		return &out
	}
	if strings.TrimSpace(secondary) != "" {
		out := strings.TrimSpace(secondary)
		return &out
	}
	return nil
}

// ResolveUpdatePolicy ports resolveUpdatePolicy from extracted/out/main/chunks/index-WiZKxTAT.js:2218.
func ResolveUpdatePolicy(currentVersion string, policy *CDNPolicy, locale string, platform string) ResolvedUpdatePolicy {
	if policy == nil {
		return ResolvedUpdatePolicy{}
	}
	if !policy.Enabled {
		return ResolvedUpdatePolicy{
			Forced:              false,
			TargetVersion:       nil,
			MinSupportedVersion: policy.MinSupportedVersion,
		}
	}
	cmp, ok := CompareSemver(currentVersion, policy.MinSupportedVersion)
	if !ok || cmp >= 0 {
		return ResolvedUpdatePolicy{
			Forced:              false,
			TargetVersion:       nil,
			MinSupportedVersion: policy.MinSupportedVersion,
		}
	}
	manualOnly := ManualOnlyRuleMatches(currentVersion, policy, platform)
	var manualRecoveryReason *string
	if manualOnly && policy.ManualOnly != nil {
		manualRecoveryReason = pickLocalizedReason(policy.ManualOnly.Reason, locale)
		if manualRecoveryReason == nil {
			manualRecoveryReason = pickLocalizedReason(policy.ForceReason, locale)
		}
	}
	requiredReason := manualRecoveryReason
	if requiredReason == nil {
		requiredReason = pickLocalizedReason(policy.ForceReason, locale)
	}
	return ResolvedUpdatePolicy{
		Forced:               true,
		TargetVersion:        nil,
		MinSupportedVersion:  policy.MinSupportedVersion,
		RequiredReason:       requiredReason,
		ManualDownloadURL:    policy.ManualDownloadURL,
		ManualOnly:           manualOnly,
		ManualRecoveryReason: manualRecoveryReason,
	}
}

type EvalOptions struct {
	CurrentVersion string
	Platform       string
	Locale         string
	PreferCache    bool
	ForceRefresh   bool
	SafeToInstall  bool
	BusyReason     string
}

type Service struct {
	CacheDir                 string
	PolicyURL                string
	ManifestURL              string
	ReleasesURL              string
	Region                   string
	Channel                  string
	Client                   *http.Client
	Now                      func() time.Time
	Sleep                    func(context.Context, time.Duration) error
	DisableBackgroundRefresh bool

	mu         sync.Mutex
	refreshing bool
}

func NewService(cacheDir string) *Service {
	policyURL := strings.TrimSpace(os.Getenv("MOVIEDESIGN_UPDATE_POLICY_URL"))
	if policyURL == "" {
		policyURL = DefaultPolicyURL
	}
	manifestURL := strings.TrimSpace(os.Getenv("MOVIEDESIGN_UPDATE_MANIFEST_URL"))
	if manifestURL == "" {
		manifestURL = DefaultManifestURL
	}
	releasesURL := strings.TrimSpace(os.Getenv("MOVIEDESIGN_RELEASES_URL"))
	if releasesURL == "" {
		releasesURL = DefaultReleasesURL
	}
	return &Service{
		CacheDir:    cacheDir,
		PolicyURL:   policyURL,
		ManifestURL: manifestURL,
		ReleasesURL: releasesURL,
		Region:      DefaultRegion,
		Channel:     DefaultChannel,
		Client:      &http.Client{},
		Now:         time.Now,
		Sleep: func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		},
	}
}

type cachedReadResult struct {
	policy    *CDNPolicy
	cachedAt  string
	ageMs     int64
	stale     bool
	clockSkew bool
	warnings  []string
}

func (s *Service) cacheFilePath() string {
	return filepath.Join(s.CacheDir, CacheFileName)
}

func (s *Service) readCache(now time.Time, expected CacheMetadata) *cachedReadResult {
	if s.CacheDir == "" {
		return nil
	}
	raw, err := os.ReadFile(s.cacheFilePath())
	if err != nil {
		return nil
	}
	var wrapper struct {
		Policy   json.RawMessage `json:"policy"`
		CachedAt string          `json:"cachedAt"`
		Metadata CacheMetadata   `json:"metadata"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return nil
	}
	if wrapper.Metadata.Region != expected.Region ||
		wrapper.Metadata.Channel != expected.Channel ||
		wrapper.Metadata.Platform != expected.Platform {
		return nil
	}
	policy, warnings, err := ParseCDNPolicy(wrapper.Policy)
	if err != nil {
		return nil
	}
	fetched, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(wrapper.CachedAt))
	if err != nil {
		fetched, err = time.Parse(time.RFC3339, strings.TrimSpace(wrapper.CachedAt))
		if err != nil {
			return nil
		}
	}
	age := now.Sub(fetched)
	ageMs := age.Milliseconds()
	clockSkew := age < 0
	if clockSkew {
		ageMs = 0
	}
	fresh := CacheFresh(now, fetched)
	return &cachedReadResult{
		policy:    policy,
		cachedAt:  wrapper.CachedAt,
		ageMs:     ageMs,
		stale:     !fresh,
		clockSkew: clockSkew,
		warnings:  warnings,
	}
}

func (s *Service) writeCacheAtomically(now time.Time, policy *CDNPolicy, meta CacheMetadata) (string, error) {
	if s.CacheDir == "" || policy == nil {
		return "", nil
	}
	if err := os.MkdirAll(s.CacheDir, 0o755); err != nil {
		return "", err
	}
	cachedAt := now.UTC().Format(time.RFC3339Nano)
	payload := CachedPolicyPayload{
		Policy:   *policy,
		CachedAt: cachedAt,
		Metadata: meta,
	}
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	encoded = append(encoded, '\n')
	tmpPath := fmt.Sprintf("%s.tmp.%d.%d", s.cacheFilePath(), os.Getpid(), now.UnixNano())
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(encoded); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := os.Rename(tmpPath, s.cacheFilePath()); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	return cachedAt, nil
}

type cdnFetchError struct {
	reason    string
	retryable bool
	err       error
}

func (e *cdnFetchError) Error() string {
	if e.err != nil {
		return fmt.Sprintf("%s: %v", e.reason, e.err)
	}
	return e.reason
}

func retryDelay(attempt int) time.Duration {
	shift := attempt - 1
	if shift < 0 {
		shift = 0
	}
	d := RetryBaseDelay * time.Duration(1<<shift)
	if d > RetryMaxDelay {
		return RetryMaxDelay
	}
	return d
}

func (s *Service) fetchFromCDNOnce(ctx context.Context) (*CDNPolicy, []string, *cdnFetchError) {
	reqCtx, cancel := context.WithTimeout(ctx, FetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, s.PolicyURL, nil)
	if err != nil {
		return nil, nil, &cdnFetchError{reason: "invalid_url", retryable: false, err: err}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cache-Control", "no-cache")

	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(reqCtx.Err(), context.DeadlineExceeded) {
			return nil, nil, &cdnFetchError{reason: "timeout", retryable: false, err: err}
		}
		return nil, nil, &cdnFetchError{reason: "network_error", retryable: false, err: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		reason := fmt.Sprintf("http_%d", resp.StatusCode)
		retryable := resp.StatusCode >= 500 && resp.StatusCode <= 599
		return nil, nil, &cdnFetchError{reason: reason, retryable: retryable}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPolicyBytes+1))
	if err != nil {
		return nil, nil, &cdnFetchError{reason: "network_error", retryable: false, err: err}
	}
	if len(body) > maxPolicyBytes {
		return nil, nil, &cdnFetchError{reason: "invalid_schema", retryable: false}
	}
	policy, warnings, err := ParseCDNPolicy(body)
	if err != nil {
		return nil, nil, &cdnFetchError{reason: "invalid_schema", retryable: false, err: err}
	}
	return policy, warnings, nil
}

func (s *Service) fetchFromCDNWithRetry(ctx context.Context) (*CDNPolicy, []string, string) {
	var lastReason string
	for attempt := 1; attempt <= MaxFetchAttempts; attempt++ {
		policy, warnings, fetchErr := s.fetchFromCDNOnce(ctx)
		if fetchErr == nil {
			return policy, warnings, ""
		}
		lastReason = fetchErr.reason
		if !fetchErr.retryable || attempt >= MaxFetchAttempts {
			break
		}
		if err := s.Sleep(ctx, retryDelay(attempt)); err != nil {
			break
		}
	}
	if lastReason == "" {
		lastReason = "network_error"
	}
	return nil, nil, lastReason
}

func (s *Service) triggerBackgroundRefresh(meta CacheMetadata) {
	if s.DisableBackgroundRefresh {
		return
	}
	s.mu.Lock()
	if s.refreshing {
		s.mu.Unlock()
		return
	}
	s.refreshing = true
	s.mu.Unlock()

	go func() {
		defer func() {
			s.mu.Lock()
			s.refreshing = false
			s.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		policy, _, reason := s.fetchFromCDNWithRetry(ctx)
		if policy != nil && reason == "" {
			_, _ = s.writeCacheAtomically(s.Now(), policy, meta)
		}
	}()
}

func (s *Service) Evaluate(ctx context.Context, opts EvalOptions) PolicySnapshot {
	version := strings.TrimSpace(opts.CurrentVersion)
	if version == "" {
		version = DefaultAppVersion
	}
	platform := NormalizePlatform(opts.Platform)
	locale := strings.TrimSpace(opts.Locale)
	if locale == "" {
		locale = DefaultLocale
	}
	meta := CacheMetadata{
		Region:   s.Region,
		Channel:  s.Channel,
		Platform: platform,
	}
	now := s.Now()

	cached := s.readCache(now, meta)
	if opts.PreferCache && !opts.ForceRefresh && cached != nil && !cached.stale {
		s.triggerBackgroundRefresh(meta)
		return s.buildSnapshot(version, platform, locale, "ready", "cache", false, cached.clockSkew, &cached.cachedAt, &cached.ageMs, nil, cached.warnings, cached.policy, opts.SafeToInstall, opts.BusyReason)
	}

	policy, warnings, failReason := s.fetchFromCDNWithRetry(ctx)
	if policy != nil && failReason == "" {
		cachedAt, _ := s.writeCacheAtomically(s.Now(), policy, meta)
		var cachedAtPtr *string
		if cachedAt != "" {
			cachedAtPtr = &cachedAt
		}
		zero := int64(0)
		return s.buildSnapshot(version, platform, locale, "ready", "cdn", false, false, cachedAtPtr, &zero, nil, warnings, policy, opts.SafeToInstall, opts.BusyReason)
	}

	// Refresh failed: apply metadata-matching LKG cache if available.
	if cached == nil {
		cached = s.readCache(s.Now(), meta)
	}
	if cached != nil {
		reason := failReason
		return s.buildSnapshot(version, platform, locale, "ready", "cache", true, cached.clockSkew, &cached.cachedAt, &cached.ageMs, &reason, cached.warnings, cached.policy, opts.SafeToInstall, opts.BusyReason)
	}

	reason := failReason
	return s.buildSnapshot(version, platform, locale, "failed", "none", false, false, nil, nil, &reason, nil, nil, opts.SafeToInstall, opts.BusyReason)
}

func (s *Service) buildSnapshot(
	version, platform, locale, status, source string,
	stale, clockSkew bool,
	cachedAt *string,
	cacheAgeMs *int64,
	fallbackReason *string,
	warnings []string,
	policy *CDNPolicy,
	safeToInstall bool,
	busyReason string,
) PolicySnapshot {
	resolved := ResolveUpdatePolicy(version, policy, locale, platform)
	busy := !safeToInstall
	var busyReasonPtr *string
	if busy && strings.TrimSpace(busyReason) != "" {
		trimmed := strings.TrimSpace(busyReason)
		busyReasonPtr = &trimmed
	}
	autoAllowed := status == "ready" && policy != nil && !resolved.ManualOnly && safeToInstall
	return PolicySnapshot{
		Status:               status,
		Source:               source,
		Stale:                stale,
		ClockSkew:            clockSkew,
		CachedAt:             cachedAt,
		PolicyCacheAgeMs:     cacheAgeMs,
		PolicyFallbackReason: fallbackReason,
		Warnings:             warnings,
		CurrentVersion:       version,
		Platform:             platform,
		Region:               s.Region,
		Channel:              s.Channel,
		Locale:               locale,
		PolicyURL:            s.PolicyURL,
		ManifestURL:          s.ManifestURL,
		ReleasesURL:          s.ReleasesURL,
		Policy:               policy,
		Resolved:             resolved,
		Busy:                 busy,
		BusyReason:           busyReasonPtr,
		AutoUpdateAllowed:    autoAllowed,
	}
}
