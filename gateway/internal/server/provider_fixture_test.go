package server

import (
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"net/http"
	"net/http/httptest"
	"testing"
)

// This Windows host's ephemeral range includes reserved gateway/native ports.
// Retry fixture allocation only; never relax production URL validation.
func providerTestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	for n := 0; n < 16; n++ {
		s := httptest.NewServer(handler)
		if providers.ValidateBaseURL(s.URL) == nil {
			return s
		}
		t.Logf("reallocate reserved fixture endpoint %s", s.URL)
		s.Close()
	}
	t.Fatal("could not allocate an eligible loopback supplier")
	return nil
}
