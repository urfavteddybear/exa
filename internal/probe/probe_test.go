package probe

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/json"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"exa/internal/model"

	"golang.org/x/net/icmp"
)

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	return b
}

func makeSelfSignedCert(t *testing.T, notBefore, notAfter time.Time) tls.Certificate {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  priv,
	}
}

func TestProbeHTTP_MethodHeadersBodyAndStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("X-Custom") != "exa-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"ping":true}` {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"status":"ok","items":[{"name":"api","count":3}]}`))
	}))
	defer ts.Close()

	cfg := model.HTTPConfig{
		Method:         "POST",
		ExpectedStatus: "200,201",
		Headers:        map[string]string{"X-Custom": "exa-secret"},
		Body:           `{"ping":true}`,
		Keyword:        `"ok"`,
		JSONPath:       "$.items[0].count",
		JSONExpected:   "3",
	}

	res := Probe(context.Background(), model.Monitor{
		Type:       model.MonitorTypeHTTP,
		Target:     ts.URL,
		TimeoutSec: 5,
		Config:     mustJSON(t, cfg),
	})
	if res.Status != model.HeartbeatStatusUp {
		t.Fatalf("expected up, got %s (%s)", res.Status, res.Message)
	}
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("expected status 201, got %d", res.StatusCode)
	}
}

func TestProbeHTTP_RedirectsAndKeywordInvert(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("service healthy"))
	}))
	defer ts.Close()

	followFalse := false
	resNoFollow := Probe(context.Background(), model.Monitor{
		Type:   model.MonitorTypeHTTP,
		Target: ts.URL + "/redirect",
		Config: mustJSON(t, model.HTTPConfig{
			ExpectedStatus:  "302",
			FollowRedirects: &followFalse,
		}),
	})
	if resNoFollow.Status != model.HeartbeatStatusUp || resNoFollow.StatusCode != 302 {
		t.Fatalf("expected 302 up with FollowRedirects=false, got %s (%d: %s)", resNoFollow.Status, resNoFollow.StatusCode, resNoFollow.Message)
	}

	resFollow := Probe(context.Background(), model.Monitor{
		Type:   model.MonitorTypeHTTP,
		Target: ts.URL + "/redirect",
		Config: mustJSON(t, model.HTTPConfig{
			ExpectedStatus: "200-299",
			Keyword:        "error",
			KeywordInvert:  true,
		}),
	})
	if resFollow.Status != model.HeartbeatStatusUp || resFollow.StatusCode != 200 {
		t.Fatalf("expected 200 up with redirect follow, got %s (%s)", resFollow.Status, resFollow.Message)
	}

	resInvertFail := Probe(context.Background(), model.Monitor{
		Type:   model.MonitorTypeHTTP,
		Target: ts.URL + "/final",
		Config: mustJSON(t, model.HTTPConfig{
			Keyword:       "healthy",
			KeywordInvert: true,
		}),
	})
	if resInvertFail.Status != model.HeartbeatStatusDown {
		t.Fatalf("expected down when inverted keyword present, got %s", resInvertFail.Status)
	}
}

func TestProbeHTTP_JSONPathFailuresAndDegradedMs(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(40 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":{"nested":{"active":true}}}`))
	}))
	defer ts.Close()

	resDegraded := Probe(context.Background(), model.Monitor{
		Type:       model.MonitorTypeHTTP,
		Target:     ts.URL,
		DegradedMs: 10,
		Config: mustJSON(t, model.HTTPConfig{
			JSONPath:     "data.nested.active",
			JSONExpected: "true",
		}),
	})
	if resDegraded.Status != model.HeartbeatStatusDegraded {
		t.Fatalf("expected degraded on slow response, got %s (%s)", resDegraded.Status, resDegraded.Message)
	}

	resBadJSON := Probe(context.Background(), model.Monitor{
		Type:   model.MonitorTypeHTTP,
		Target: ts.URL,
		Config: mustJSON(t, model.HTTPConfig{
			JSONPath:     "data.nested.active",
			JSONExpected: "false",
		}),
	})
	if resBadJSON.Status != model.HeartbeatStatusDown {
		t.Fatalf("expected down on JSON mismatch, got %s", resBadJSON.Status)
	}
}

func TestProbeHTTP_TLSExpiry(t *testing.T) {
	now := time.Now().UTC()

	t.Run("valid cert", func(t *testing.T) {
		ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		ts.TLS = &tls.Config{
			Certificates: []tls.Certificate{makeSelfSignedCert(t, now.Add(-time.Hour), now.Add(60*24*time.Hour))},
		}
		ts.StartTLS()
		defer ts.Close()

		res := Probe(context.Background(), model.Monitor{
			Type:   model.MonitorTypeHTTP,
			Target: ts.URL,
			Config: mustJSON(t, model.HTTPConfig{IgnoreTLS: true, TLSExpiryWarnDays: 14}),
		})
		if res.Status != model.HeartbeatStatusUp {
			t.Fatalf("expected up, got %s (%s)", res.Status, res.Message)
		}
		if res.TLSExpiresAt == nil || res.TLSDaysLeft == nil || *res.TLSDaysLeft < 58 {
			t.Fatalf("unexpected TLS fields: exp=%v days=%v", res.TLSExpiresAt, res.TLSDaysLeft)
		}
	})

	t.Run("expiring soon degraded", func(t *testing.T) {
		ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		ts.TLS = &tls.Config{
			Certificates: []tls.Certificate{makeSelfSignedCert(t, now.Add(-time.Hour), now.Add(5*24*time.Hour))},
		}
		ts.StartTLS()
		defer ts.Close()

		res := Probe(context.Background(), model.Monitor{
			Type:   model.MonitorTypeHTTP,
			Target: ts.URL,
			Config: mustJSON(t, model.HTTPConfig{IgnoreTLS: true, TLSExpiryWarnDays: 14}),
		})
		if res.Status != model.HeartbeatStatusDegraded {
			t.Fatalf("expected degraded for soon-expiring cert, got %s (%s)", res.Status, res.Message)
		}
	})

	t.Run("expired down", func(t *testing.T) {
		ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		ts.TLS = &tls.Config{
			Certificates: []tls.Certificate{makeSelfSignedCert(t, now.Add(-48*time.Hour), now.Add(-2*time.Hour))},
		}
		ts.StartTLS()
		defer ts.Close()

		res := Probe(context.Background(), model.Monitor{
			Type:   model.MonitorTypeHTTP,
			Target: ts.URL,
			Config: mustJSON(t, model.HTTPConfig{IgnoreTLS: true, TLSExpiryWarnDays: 14}),
		})
		if res.Status != model.HeartbeatStatusDown {
			t.Fatalf("expected down for expired cert, got %s (%s)", res.Status, res.Message)
		}
	})
}

func TestProbeTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 64)
				n, _ := c.Read(buf)
				if strings.TrimSpace(string(buf[:n])) == "PING" {
					_, _ = c.Write([]byte("PONG\n"))
				} else {
					_, _ = c.Write([]byte("UNKNOWN\n"))
				}
			}(conn)
		}
	}()

	res := Probe(context.Background(), model.Monitor{
		Type:       model.MonitorTypeTCP,
		Target:     ln.Addr().String(),
		TimeoutSec: 3,
		Config:     mustJSON(t, model.TCPConfig{SendData: "PING\n", ExpectData: "PONG"}),
	})
	if res.Status != model.HeartbeatStatusUp {
		t.Fatalf("expected TCP up, got %s (%s)", res.Status, res.Message)
	}

	resFail := Probe(context.Background(), model.Monitor{
		Type:       model.MonitorTypeTCP,
		Target:     ln.Addr().String(),
		TimeoutSec: 3,
		Config:     mustJSON(t, model.TCPConfig{SendData: "HELLO\n", ExpectData: "PONG"}),
	})
	if resFail.Status != model.HeartbeatStatusDown {
		t.Fatalf("expected TCP down on mismatch, got %s", resFail.Status)
	}
}

func TestProbeDNS_MockServer(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer pc.Close()

	// Minimal RFC 1035 responder returning 127.0.0.42 for A queries.
	go func() {
		buf := make([]byte, 512)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil || n < 12 {
				return
			}
			req := buf[:n]
			// Find end of QNAME.
			idx := 12
			for idx < n && req[idx] != 0 {
				idx += int(req[idx]) + 1
			}
			if idx+5 > n {
				continue
			}
			qEnd := idx + 5

			resp := make([]byte, 0, qEnd+16)
			resp = append(resp, req[0], req[1])     // Transaction ID
			resp = append(resp, 0x81, 0x80)         // Standard response, no error
			resp = append(resp, 0x00, 0x01)         // QDCOUNT = 1
			resp = append(resp, 0x00, 0x01)         // ANCOUNT = 1
			resp = append(resp, 0x00, 0x00, 0, 0)   // NSCOUNT, ARCOUNT
			resp = append(resp, req[12:qEnd]...)    // Question section
			resp = append(resp, 0xc0, 0x0c)         // Name pointer to offset 12
			resp = append(resp, 0x00, 0x01)         // TYPE A
			resp = append(resp, 0x00, 0x01)         // CLASS IN
			ttl := make([]byte, 4)
			binary.BigEndian.PutUint32(ttl, 60)
			resp = append(resp, ttl...)
			resp = append(resp, 0x00, 0x04)         // RDLENGTH = 4
			resp = append(resp, 127, 0, 0, 42)      // RDATA 127.0.0.42
			_, _ = pc.WriteTo(resp, addr)
		}
	}()

	res := Probe(context.Background(), model.Monitor{
		Type:       model.MonitorTypeDNS,
		Target:     "service.local",
		TimeoutSec: 3,
		Config: mustJSON(t, model.DNSConfig{
			RecordType:    "A",
			Resolver:      pc.LocalAddr().String(),
			ExpectedValue: "127.0.0.42",
		}),
	})
	if res.Status != model.HeartbeatStatusUp {
		t.Fatalf("expected DNS up, got %s (%s)", res.Status, res.Message)
	}

	resMismatch := Probe(context.Background(), model.Monitor{
		Type:       model.MonitorTypeDNS,
		Target:     "service.local",
		TimeoutSec: 3,
		Config: mustJSON(t, model.DNSConfig{
			RecordType:    "A",
			Resolver:      pc.LocalAddr().String(),
			ExpectedValue: "10.0.0.1",
		}),
	})
	if resMismatch.Status != model.HeartbeatStatusDown {
		t.Fatalf("expected DNS down on mismatch, got %s", resMismatch.Status)
	}
}

func TestParsePingOutputAndMatchHelpers(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   int
		ok     bool
	}{
		{
			name:   "windows average",
			output: "Minimum = 1ms, Maximum = 3ms, Average = 2ms",
			want:   2,
			ok:     true,
		},
		{
			name:   "windows sub-ms",
			output: "Reply from 127.0.0.1: bytes=32 time<1ms TTL=128\nApproximate round trip times in milli-seconds:\n    Minimum = 0ms, Maximum = 0ms, Average = 0ms",
			want:   0,
			ok:     true,
		},
		{
			name:   "linux rtt",
			output: "rtt min/avg/max/mdev = 10.100/14.600/19.200/2.100 ms",
			want:   15,
			ok:     true,
		},
		{
			name:   "macos round-trip",
			output: "round-trip min/avg/max/stddev = 3.1/4.2/5.3/0.4 ms",
			want:   4,
			ok:     true,
		},
		{
			name:   "alpine busybox round-trip",
			output: "round-trip min/avg/max = 0.420/0.510/0.600 ms",
			want:   1,
			ok:     true,
		},
		{
			name:   "unreachable",
			output: "Request timed out.\nPackets: Sent = 2, Received = 0, Lost = 2 (100% loss)",
			want:   0,
			ok:     false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parsePingOutput(tc.output)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("parsePingOutput() = (%d, %v), want (%d, %v)", got, ok, tc.want, tc.ok)
			}
		})
	}

	if ok, _ := matchStatusCode(204, "200, 204, 300-399"); !ok {
		t.Fatal("expected 204 to match status spec")
	}
	if ok, _ := matchStatusCode(404, "200-299"); ok {
		t.Fatal("expected 404 not to match 200-299")
	}

	res := Probe(context.Background(), model.Monitor{Type: model.MonitorTypePush})
	if res.Status != model.HeartbeatStatusUp {
		t.Fatalf("expected push probe up, got %s", res.Status)
	}
}

func TestPingHostValidation(t *testing.T) {
	invalidTargets := []string{
		"",
		"   ",
		"\t\n",
		"-c",
		"--help",
		"-i 1",
		"-c 1 127.0.0.1",
		"host;id",
		"host&&whoami",
		"bad host",
		"sub..example.com",
		"-leading.example.com",
		"trailing-.example.com",
		strings.Repeat("a", 64) + ".example.com",
		strings.Repeat("a.", 127) + "com",
	}

	for _, target := range invalidTargets {
		res := Probe(context.Background(), model.Monitor{
			Type:       model.MonitorTypePing,
			Target:     target,
			TimeoutSec: 1,
		})
		if res.Status != model.HeartbeatStatusDown {
			t.Errorf("expected target %q to fail validation, got %s", target, res.Status)
		}
		if res.Message != "invalid ping host" {
			t.Errorf("expected target %q message 'invalid ping host', got %q", target, res.Message)
		}
	}

	validHosts := []string{
		"127.0.0.1",
		"::1",
		"2001:4860:4860::8888",
		"localhost",
		"api-1.internal.example.com",
		"a",
	}
	for _, host := range validHosts {
		if !isValidPingHost(host) {
			t.Errorf("expected isValidPingHost(%q) to be true", host)
		}
	}
}

func TestOrderPingTargets(t *testing.T) {
	t.Run("dual stack prioritizes first IPv4 then first IPv6", func(t *testing.T) {
		ips := []net.IPAddr{
			{IP: net.ParseIP("::1")},
			{IP: net.ParseIP("127.0.0.1")},
			{IP: net.ParseIP("10.0.0.1")},
			{IP: net.ParseIP("2001:db8::1")},
		}
		got := orderPingTargets(ips)
		if len(got) != 2 {
			t.Fatalf("expected 2 candidates, got %d", len(got))
		}
		if got[0].IP.String() != "127.0.0.1" {
			t.Errorf("expected first candidate 127.0.0.1, got %s", got[0].IP.String())
		}
		if got[1].IP.String() != "::1" {
			t.Errorf("expected second candidate ::1, got %s", got[1].IP.String())
		}
	})

	t.Run("ipv6 only", func(t *testing.T) {
		ips := []net.IPAddr{
			{IP: net.ParseIP("2001:db8::1")},
			{IP: net.ParseIP("2001:db8::2")},
		}
		got := orderPingTargets(ips)
		if len(got) != 1 || got[0].IP.String() != "2001:db8::1" {
			t.Fatalf("expected [2001:db8::1], got %+v", got)
		}
	})

	t.Run("ipv4 only", func(t *testing.T) {
		ips := []net.IPAddr{
			{IP: net.ParseIP("127.0.0.1")},
			{IP: net.ParseIP("10.0.0.1")},
		}
		got := orderPingTargets(ips)
		if len(got) != 1 || got[0].IP.String() != "127.0.0.1" {
			t.Fatalf("expected [127.0.0.1], got %+v", got)
		}
	})

	t.Run("empty", func(t *testing.T) {
		got := orderPingTargets(nil)
		if len(got) != 0 {
			t.Fatalf("expected empty slice, got %+v", got)
		}
	})
}

func TestICMPDestAddr(t *testing.T) {
	v4As16 := net.ParseIP("127.0.0.1") // 16-byte representation
	if len(v4As16) != net.IPv6len {
		t.Fatalf("unexpected parsed IP length: %d", len(v4As16))
	}

	dgramV4 := icmpDestAddr(net.IPAddr{IP: v4As16}, true)
	udpV4, ok := dgramV4.(*net.UDPAddr)
	if !ok {
		t.Fatalf("expected *net.UDPAddr for datagram IPv4, got %T", dgramV4)
	}
	if len(udpV4.IP) != net.IPv4len || udpV4.IP.String() != "127.0.0.1" {
		t.Errorf("expected 4-byte 127.0.0.1, got %v (len=%d)", udpV4.IP, len(udpV4.IP))
	}

	dgramV6 := icmpDestAddr(net.IPAddr{IP: net.ParseIP("fe80::1"), Zone: "eth0"}, true)
	udpV6, ok := dgramV6.(*net.UDPAddr)
	if !ok {
		t.Fatalf("expected *net.UDPAddr for datagram IPv6, got %T", dgramV6)
	}
	if udpV6.IP.String() != "fe80::1" || udpV6.Zone != "eth0" {
		t.Errorf("expected fe80::1%%eth0, got %v%%%s", udpV6.IP, udpV6.Zone)
	}

	rawV4 := icmpDestAddr(net.IPAddr{IP: v4As16}, false)
	ipV4, ok := rawV4.(*net.IPAddr)
	if !ok || ipV4.IP.String() != "127.0.0.1" {
		t.Errorf("expected *net.IPAddr 127.0.0.1, got %T (%v)", rawV4, rawV4)
	}

	rawV6 := icmpDestAddr(net.IPAddr{IP: net.ParseIP("fe80::1"), Zone: "eth0"}, false)
	ipV6, ok := rawV6.(*net.IPAddr)
	if !ok || ipV6.IP.String() != "fe80::1" || ipV6.Zone != "eth0" {
		t.Errorf("expected *net.IPAddr fe80::1%%eth0, got %T (%v)", rawV6, rawV6)
	}
}

func TestMatchEchoReply(t *testing.T) {
	payload := []byte("exa-1234-deadbeef")

	t.Run("raw socket requires matching id, seq, and payload", func(t *testing.T) {
		if !matchEchoReply(&icmp.Echo{ID: 42, Seq: 1, Data: payload}, 42, 1, payload, false) {
			t.Error("expected exact raw echo reply to match")
		}
		if matchEchoReply(&icmp.Echo{ID: 99, Seq: 1, Data: payload}, 42, 1, payload, false) {
			t.Error("expected raw echo reply with mismatched ID not to match")
		}
		if matchEchoReply(&icmp.Echo{ID: 42, Seq: 2, Data: payload}, 42, 1, payload, false) {
			t.Error("expected raw echo reply with mismatched Seq not to match")
		}
		if matchEchoReply(&icmp.Echo{ID: 42, Seq: 1, Data: []byte("other")}, 42, 1, payload, false) {
			t.Error("expected raw echo reply with mismatched payload not to match")
		}
	})

	t.Run("datagram socket allows kernel-rewritten id when seq and payload match", func(t *testing.T) {
		if !matchEchoReply(&icmp.Echo{ID: 55123, Seq: 1, Data: payload}, 42, 1, payload, true) {
			t.Error("expected datagram echo reply with rewritten ID to match")
		}
		if matchEchoReply(&icmp.Echo{ID: 55123, Seq: 2, Data: payload}, 42, 1, payload, true) {
			t.Error("expected datagram echo reply with mismatched Seq not to match")
		}
		if matchEchoReply(&icmp.Echo{ID: 55123, Seq: 1, Data: []byte("other")}, 42, 1, payload, true) {
			t.Error("expected datagram echo reply with mismatched payload not to match")
		}
		if matchEchoReply(nil, 42, 1, payload, true) {
			t.Error("expected nil echo not to match")
		}
	})
}
