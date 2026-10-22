package updates_test

import (
	"testing"

	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/updates"
)

func TestValidSemverGrammar(t *testing.T) {
	valid := []string{
		"0.0.0", "1.2.3", " v1.2.3 \n", "1.2.3-0", "1.2.3-01a",
		"1.2.3-x.7.z.92", "1.2.3-x-y-z.--", "1.2.3+001",
		"1.2.3-alpha+001.sha-ABC", "1.2.3+21AF26D3----117B344092BD",
		"999999999999999999999999999999.2.3",
		"1.999999999999999999999999999999.3",
		"1.2.999999999999999999999999999999",
		"1.2.3-999999999999999999999999999999",
	}
	invalid := []string{
		"", "v", "1", "1.2", "1.2.3.4", "01.2.3", "1.02.3", "1.2.03",
		"+1.2.3", "-1.2.3", "1.2.-3", "1.2.3-", "1.2.3-01", "1.2.3-rc.01",
		"1.2.3-.rc", "1.2.3-rc.", "1.2.3-rc..1", "1.2.3-α", "1.2.3-rc_1",
		"1.2.3-rc 1", "1.2.3+", "1.2.3+.build", "1.2.3+build.", "1.2.3+build..1",
		"1.2.3+build+other", "1.2.3+构建", "1.2.3+build_1", "1.2.3+build 1",
	}
	for _, version := range valid {
		t.Run("valid/"+version, func(t *testing.T) {
			if !updates.ValidSemver(version) {
				t.Errorf("ValidSemver(%q) = false, want true", version)
			}
			if got, ok := updates.CompareSemver(version, version); !ok || got != 0 {
				t.Errorf("CompareSemver(%q, itself) = (%d, %t), want (0, true)", version, got, ok)
			}
		})
	}
	for _, version := range invalid {
		t.Run("invalid/"+version, func(t *testing.T) {
			if updates.ValidSemver(version) {
				t.Errorf("ValidSemver(%q) = true, want false", version)
			}
			for _, pair := range [][2]string{{version, "1.2.3"}, {"1.2.3", version}} {
				if got, ok := updates.CompareSemver(pair[0], pair[1]); ok || got != 0 {
					t.Errorf("CompareSemver(%q, %q) = (%d, %t), want (0, false)", pair[0], pair[1], got, ok)
				}
			}
		})
	}
}

func TestCompareSemverPrecedence(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.2.3-rc.2", "1.2.3-rc.10", -1},
		{"1.2.3-99999999999999999999", "1.2.3-100000000000000000000", -1},
		{"1.2.3-99999999999999999999", "1.2.3-99999999999999999998", 1},
		{"99999999999999999999.0.0", "100000000000000000000.0.0", -1},
		{"1.99999999999999999999.0", "1.100000000000000000000.0", -1},
		{"1.2.99999999999999999999", "1.2.100000000000000000000", -1},
		{"1.2.3-9", "1.2.3-1a", -1},
		{"1.2.3-1", "1.2.3--", -1},
		{"1.2.3-A", "1.2.3-a", -1},
		{"1.2.3-alpha", "1.2.3-alpha.0", -1},
		{"1.2.3+build.1", "1.2.3+build.2", 0},
		{"1.2.3-rc.2+build.1", "1.2.3-rc.2+build.2", 0},
		{" v1.2.3 \n", "1.2.3+build.1", 0},
		{"1.9.9", "1.10.0", -1},
		{"1.2.9", "1.2.10", -1},
		{"1.99.99", "2.0.0", -1},
	}
	standard := []string{"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0"}
	for i := 0; i < len(standard)-1; i++ {
		cases = append(cases, struct {
			a, b string
			want int
		}{standard[i], standard[i+1], -1})
	}
	for _, tc := range cases {
		t.Run(tc.a+"/"+tc.b, func(t *testing.T) {
			if got, ok := updates.CompareSemver(tc.a, tc.b); !ok || got != tc.want {
				t.Errorf("CompareSemver(%q, %q) = (%d, %t), want (%d, true)", tc.a, tc.b, got, ok, tc.want)
			}
			if got, ok := updates.CompareSemver(tc.b, tc.a); !ok || got != -tc.want {
				t.Errorf("CompareSemver(%q, %q) = (%d, %t), want (%d, true)", tc.b, tc.a, got, ok, -tc.want)
			}
		})
	}
}

func TestManualOnlyPrereleaseBounds(t *testing.T) {
	min, max := "1.2.3-rc.2", "1.2.3-rc.10"
	downloadURL := updates.DefaultReleasesURL
	policy := &updates.CDNPolicy{
		Enabled: true, MinSupportedVersion: "1.2.3", ManualDownloadURL: &downloadURL,
		ManualOnly: &updates.ManualOnlyRule{
			Platforms: []string{"win32"}, MinVersionInclusive: &min, MaxVersionExclusive: &max,
		},
	}
	for _, tc := range []struct {
		version string
		want    bool
	}{
		{"1.2.3-rc.1", false}, {"1.2.3-rc.2", true}, {"1.2.3-rc.2+001", true},
		{"1.2.3-rc.3", true}, {"1.2.3-rc.9", true}, {"1.2.3-rc.10", false},
		{"1.2.3-rc.10+001", false}, {"1.2.3-rc.11", false}, {"1.2.3", false},
		{"1.2.3-rc.02", false},
	} {
		t.Run(tc.version, func(t *testing.T) {
			if got := updates.ManualOnlyRuleMatches(tc.version, policy, "windows"); got != tc.want {
				t.Errorf("ManualOnlyRuleMatches(%q, [rc.2, rc.10)) = %t, want %t", tc.version, got, tc.want)
			}
			resolved := updates.ResolveUpdatePolicy(tc.version, policy, "en-US", "windows")
			if resolved.ManualOnly != tc.want {
				t.Errorf("ResolveUpdatePolicy(%q).ManualOnly = %t, want %t", tc.version, resolved.ManualOnly, tc.want)
			}
		})
	}
	policy.MinSupportedVersion = "1.2.3-rc.10"
	if !updates.ManualOnlyRuleMatches(min, policy, "win32") {
		t.Error("max equal to minimum supported must remain valid")
	}
	policy.MinSupportedVersion = "1.2.3-rc.9"
	if updates.ManualOnlyRuleMatches(min, policy, "win32") {
		t.Error("max above minimum supported must invalidate the rule")
	}
	policy.MinSupportedVersion = "1.2.3"
	min, max = max, min
	if updates.ManualOnlyRuleMatches("1.2.3-rc.2", policy, "win32") {
		t.Error("reversed prerelease bounds must invalidate the rule")
	}
}
