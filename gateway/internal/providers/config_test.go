package providers

import "testing"

func TestProviderURLs(t *testing.T) {
	for _, tc := range []struct {
		raw     string
		allowed bool
	}{
		{"http://127.0.0.1:11434/v1", true}, {"https://api.example.com/v1", true},
		{"http://127.0.0.1:8001", false}, {"http://127.0.0.1:4096", false}, {"http://169.254.169.254", false},
		{"file:///C:/secrets", false}, {"https://example.com@127.0.0.1:8001", false}, {"https://example.com/v1?key=hidden", false},
		{"http://example.com", false}, {"http://[::ffff:169.254.169.254]", false}, {"http://127.0.0.1:11434/v1/../admin", false},
	} {
		if (ValidateBaseURL(tc.raw) == nil) != tc.allowed {
			t.Fatal(tc.raw)
		}
	}
}
