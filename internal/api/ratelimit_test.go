package api

import (
	"net/http/httptest"
	"testing"
)

func TestClientIPUsesRightmostForwardedFor(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.2:5555"
	// Client spoofs 1.2.3.4; the proxy appends the real peer 203.0.113.9.
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 203.0.113.9")
	if got := clientIP(r, true); got != "203.0.113.9" {
		t.Fatalf("expected rightmost XFF entry, got %q", got)
	}
	if got := clientIP(r, false); got != "10.0.0.2" {
		t.Fatalf("expected RemoteAddr when proxy untrusted, got %q", got)
	}
}
