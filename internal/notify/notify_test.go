package notify

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"exa/internal/db"
	"exa/internal/model"
)

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return b
}

func startMockSMTP(t *testing.T) (host string, port int, received *[]string, mu *sync.Mutex, closeFn func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen smtp: %v", err)
	}
	var msgs []string
	var lock sync.Mutex

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.WriteString(c, "220 mock.smtp ESMTP\r\n")
				reader := bufio.NewReader(c)
				inData := false
				var dataBuf strings.Builder
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					if inData {
						if line == ".\r\n" || line == ".\n" {
							inData = false
							lock.Lock()
							msgs = append(msgs, dataBuf.String())
							lock.Unlock()
							dataBuf.Reset()
							_, _ = io.WriteString(c, "250 OK queued\r\n")
							continue
						}
						dataBuf.WriteString(line)
						continue
					}
					upper := strings.ToUpper(strings.TrimSpace(line))
					switch {
					case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
						_, _ = io.WriteString(c, "250-mock.smtp\r\n250 AUTH PLAIN\r\n")
					case strings.HasPrefix(upper, "AUTH"):
						_, _ = io.WriteString(c, "235 Authentication succeeded\r\n")
					case strings.HasPrefix(upper, "MAIL FROM"):
						_, _ = io.WriteString(c, "250 OK\r\n")
					case strings.HasPrefix(upper, "RCPT TO"):
						_, _ = io.WriteString(c, "250 OK\r\n")
					case upper == "DATA":
						inData = true
						_, _ = io.WriteString(c, "354 End data with <CR><LF>.<CR><LF>\r\n")
					case upper == "QUIT":
						_, _ = io.WriteString(c, "221 Bye\r\n")
						return
					default:
						_, _ = io.WriteString(c, "250 OK\r\n")
					}
				}
			}(conn)
		}
	}()

	h, pStr, _ := net.SplitHostPort(ln.Addr().String())
	p, _ := strconv.Atoi(pStr)
	return h, p, &msgs, &lock, func() { _ = ln.Close() }
}

func TestDispatcher_AllSixChannels(t *testing.T) {
	d := NewDispatcher(nil)
	ctx := context.Background()

	t.Run("webhook with HMAC and headers", func(t *testing.T) {
		var gotSig, gotHeader string
		var gotBody []byte
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotSig = r.Header.Get("X-Exa-Signature")
			gotHeader = r.Header.Get("X-Custom")
			gotBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
		}))
		defer ts.Close()

		secret := "top-secret-key"
		err := d.TestChannel(ctx, model.NotificationChannel{
			Name: "hook",
			Type: model.NotificationTypeWebhook,
			Config: mustJSON(t, model.WebhookConfig{
				URL:     ts.URL,
				Secret:  secret,
				Headers: map[string]string{"X-Custom": "val-1"},
			}),
		})
		if err != nil {
			t.Fatalf("webhook TestChannel failed: %v", err)
		}
		if gotHeader != "val-1" {
			t.Fatalf("expected custom header val-1, got %q", gotHeader)
		}
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(gotBody)
		wantSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		if gotSig != wantSig {
			t.Fatalf("signature mismatch: got %q, want %q", gotSig, wantSig)
		}
	})

	t.Run("discord embed", func(t *testing.T) {
		var payload map[string]any
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&payload)
			w.WriteHeader(http.StatusNoContent)
		}))
		defer ts.Close()

		err := d.TestChannel(ctx, model.NotificationChannel{
			Name: "disc",
			Type: model.NotificationTypeDiscord,
			Config: mustJSON(t, model.DiscordConfig{
				WebhookURL: ts.URL,
				Username:   "ExaBot",
			}),
		})
		if err != nil {
			t.Fatalf("discord TestChannel failed: %v", err)
		}
		if payload["username"] != "ExaBot" {
			t.Fatalf("unexpected discord username: %v", payload["username"])
		}
		embeds, ok := payload["embeds"].([]any)
		if !ok || len(embeds) != 1 {
			t.Fatalf("expected 1 discord embed, got %v", payload["embeds"])
		}
	})

	t.Run("slack attachment", func(t *testing.T) {
		var payload map[string]any
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&payload)
			w.WriteHeader(http.StatusOK)
		}))
		defer ts.Close()

		err := d.TestChannel(ctx, model.NotificationChannel{
			Name: "slack",
			Type: model.NotificationTypeSlack,
			Config: mustJSON(t, model.SlackConfig{
				WebhookURL: ts.URL,
				Channel:    "#ops",
			}),
		})
		if err != nil {
			t.Fatalf("slack TestChannel failed: %v", err)
		}
		if payload["channel"] != "#ops" {
			t.Fatalf("expected channel #ops, got %v", payload["channel"])
		}
		if _, ok := payload["attachments"]; !ok {
			t.Fatal("expected attachments in slack payload")
		}
	})

	t.Run("telegram sendMessage", func(t *testing.T) {
		var gotPath string
		var payload map[string]any
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			_ = json.NewDecoder(r.Body).Decode(&payload)
			w.WriteHeader(http.StatusOK)
		}))
		defer ts.Close()

		tgDisp := NewDispatcher(nil)
		tgDisp.telegramBaseURL = ts.URL

		err := tgDisp.TestChannel(ctx, model.NotificationChannel{
			Name: "tg",
			Type: model.NotificationTypeTelegram,
			Config: mustJSON(t, model.TelegramConfig{
				BotToken: "12345:ABC",
				ChatID:   "-100999",
			}),
		})
		if err != nil {
			t.Fatalf("telegram TestChannel failed: %v", err)
		}
		if gotPath != "/bot12345:ABC/sendMessage" {
			t.Fatalf("unexpected telegram path: %s", gotPath)
		}
		if payload["chat_id"] != "-100999" || payload["parse_mode"] != "HTML" {
			t.Fatalf("unexpected telegram payload: %v", payload)
		}
	})

	t.Run("email smtp", func(t *testing.T) {
		host, port, msgs, mu, closeSMTP := startMockSMTP(t)
		defer closeSMTP()

		err := d.TestChannel(ctx, model.NotificationChannel{
			Name: "mail",
			Type: model.NotificationTypeEmail,
			Config: mustJSON(t, model.EmailConfig{
				SMTPHost: host,
				SMTPPort: port,
				Username: "user",
				Password: "pw",
				From:     "alerts@example.com",
				To:       []string{"ops@example.com"},
			}),
		})
		if err != nil {
			t.Fatalf("email TestChannel failed: %v", err)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(*msgs) != 1 || !strings.Contains((*msgs)[0], "Subject: Exa Test Notification") {
			t.Fatalf("unexpected email messages: %v", *msgs)
		}
	})

	t.Run("ntfy", func(t *testing.T) {
		var gotPath, gotTitle, gotPriority, gotAuth, gotBody string
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			gotTitle = r.Header.Get("Title")
			gotPriority = r.Header.Get("Priority")
			gotAuth = r.Header.Get("Authorization")
			b, _ := io.ReadAll(r.Body)
			gotBody = string(b)
			w.WriteHeader(http.StatusOK)
		}))
		defer ts.Close()

		err := d.TestChannel(ctx, model.NotificationChannel{
			Name: "ntfy",
			Type: model.NotificationTypeNtfy,
			Config: mustJSON(t, model.NtfyConfig{
				ServerURL: ts.URL,
				Topic:     "exa-alerts",
				Token:     "tk_123",
				Priority:  "high",
			}),
		})
		if err != nil {
			t.Fatalf("ntfy TestChannel failed: %v", err)
		}
		if gotPath != "/exa-alerts" || gotTitle == "" || gotPriority != "high" || gotAuth != "Bearer tk_123" || gotBody == "" {
			t.Fatalf("unexpected ntfy request: path=%q title=%q pri=%q auth=%q body=%q", gotPath, gotTitle, gotPriority, gotAuth, gotBody)
		}
	})
}

func TestDispatcher_MonitorIncidentAndMaintenanceRouting(t *testing.T) {
	store, err := db.OpenPath(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	var mu sync.Mutex
	var events []notificationPayload
	hookSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p notificationPayload
		_ = json.NewDecoder(r.Body).Decode(&p)
		mu.Lock()
		events = append(events, p)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer hookSrv.Close()

	var subEvents []notificationPayload
	subHookSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p notificationPayload
		_ = json.NewDecoder(r.Body).Decode(&p)
		mu.Lock()
		subEvents = append(subEvents, p)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer subHookSrv.Close()

	smtpHost, smtpPort, smtpMsgs, smtpMu, closeSMTP := startMockSMTP(t)
	defer closeSMTP()

	// Default webhook channel.
	_, err = store.CreateNotificationChannel(model.NotificationChannel{
		Name:      "default-hook",
		Type:      model.NotificationTypeWebhook,
		Config:    mustJSON(t, model.WebhookConfig{URL: hookSrv.URL}),
		IsDefault: true,
		Enabled:   true,
	})
	if err != nil {
		t.Fatalf("create default webhook: %v", err)
	}

	// Non-default enabled email channel (provides SMTP settings for email subscribers).
	_, err = store.CreateNotificationChannel(model.NotificationChannel{
		Name:      "smtp",
		Type:      model.NotificationTypeEmail,
		Config:    mustJSON(t, model.EmailConfig{SMTPHost: smtpHost, SMTPPort: smtpPort, From: "status@example.com", To: []string{"admin@example.com"}}),
		IsDefault: false,
		Enabled:   true,
	})
	if err != nil {
		t.Fatalf("create email channel: %v", err)
	}

	mon, err := store.CreateMonitor(model.Monitor{
		Name:   "API",
		Type:   model.MonitorTypeHTTP,
		Target: "https://example.com",
		Active: true,
	})
	if err != nil {
		t.Fatalf("create monitor: %v", err)
	}

	page, err := store.CreateStatusPage(model.StatusPage{
		Slug:     "main",
		Title:    "Main Status",
		IsPublic: true,
	})
	if err != nil {
		t.Fatalf("create status page: %v", err)
	}

	if _, err := store.CreateSubscriber(model.Subscriber{
		StatusPageID: page.ID,
		Type:         model.SubscriberTypeWebhook,
		Endpoint:     "https://example.com/hook",
		Verified:     true,
		Active:       true,
	}); err != nil {
		t.Fatalf("create webhook subscriber: %v", err)
	}
	if _, err := store.CreateSubscriber(model.Subscriber{
		StatusPageID: page.ID,
		Type:         model.SubscriberTypeEmail,
		Endpoint:     "user@example.com",
		Verified:     true,
		Active:       true,
	}); err != nil {
		t.Fatalf("create email subscriber: %v", err)
	}

	d := NewDispatcher(store)
	tr := subHookSrv.Client().Transport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, subHookSrv.Listener.Addr().String())
	}
	d.subscriberClient.Transport = tr
	ctx := context.Background()

	d.NotifyMonitorStateChange(ctx, *mon, model.ComponentStatusOperational, model.ComponentStatusMajorOutage, "connection refused")

	inc := model.Incident{
		ID:           "inc-1",
		StatusPageID: page.ID,
		Title:        "Database Latency",
		Impact:       model.IncidentImpactMajor,
		Status:       model.IncidentStatusInvestigating,
	}
	upd := model.IncidentUpdate{
		ID:         "upd-1",
		IncidentID: inc.ID,
		Status:     model.IncidentStatusInvestigating,
		Message:    "Investigating elevated query latency.",
	}
	d.NotifyIncidentUpdate(ctx, *page, inc, upd)

	maint := model.Maintenance{
		ID:             "maint-1",
		StatusPageID:   page.ID,
		Title:          "DB Upgrade",
		Description:    "Upgrading primary cluster.",
		Status:         model.MaintenanceStatusScheduled,
		ScheduledStart: time.Now().UTC().Add(time.Hour),
		ScheduledEnd:   time.Now().UTC().Add(2 * time.Hour),
	}
	d.NotifyMaintenanceUpdate(ctx, *page, maint)

	mu.Lock()
	gotDefaultCount := len(events)
	gotSubCount := len(subEvents)
	mu.Unlock()

	if gotDefaultCount != 3 {
		t.Fatalf("expected 3 events on default webhook, got %d", gotDefaultCount)
	}
	if gotSubCount != 2 {
		t.Fatalf("expected 2 events on webhook subscriber, got %d", gotSubCount)
	}

	smtpMu.Lock()
	gotEmails := len(*smtpMsgs)
	smtpMu.Unlock()
	if gotEmails != 2 {
		t.Fatalf("expected 2 emails sent to email subscriber, got %d", gotEmails)
	}
}

func TestIsPublicRoutableIP(t *testing.T) {
	blocked := []string{
		"0.0.0.0",
		"127.0.0.1",
		"127.255.255.254",
		"169.254.169.254",
		"10.0.0.1",
		"172.16.0.1",
		"172.31.255.254",
		"192.168.1.1",
		"100.64.0.1",
		"100.127.255.254",
		"224.0.0.1",
		"255.255.255.255",
		"::",
		"::1",
		"fc00::1",
		"fd12:3456:789a::1",
		"fe80::1",
		"ff02::1",
		"::ffff:127.0.0.1",
		"::ffff:169.254.169.254",
		"::ffff:10.0.0.1",
		"::ffff:192.168.1.1",
		"::ffff:100.64.0.1",
	}
	for _, raw := range blocked {
		ip := net.ParseIP(raw)
		if ip == nil {
			t.Fatalf("failed to parse IP %q", raw)
		}
		if isPublicRoutableIP(ip) {
			t.Errorf("expected %q to be rejected as non-public IP", raw)
		}
	}
	if isPublicRoutableIP(nil) {
		t.Error("expected nil IP to be rejected")
	}

	allowed := []string{
		"1.1.1.1",
		"8.8.8.8",
		"93.184.216.34",
		"2606:4700:4700::1111",
		"2001:4860:4860::8888",
	}
	for _, raw := range allowed {
		ip := net.ParseIP(raw)
		if ip == nil {
			t.Fatalf("failed to parse IP %q", raw)
		}
		if !isPublicRoutableIP(ip) {
			t.Errorf("expected %q to be allowed as public routable IP", raw)
		}
	}
}

func TestSendSubscriberWebhook_SSRFAndRedirectsRejected(t *testing.T) {
	d := NewDispatcher(nil)
	ctx := context.Background()
	payload := notificationPayload{
		Event:     "incident_update",
		Title:     "Test",
		Message:   "Testing SSRF protections",
		Severity:  "info",
		Timestamp: time.Now().UTC(),
	}

	rejectedURLs := []string{
		"",
		"http://example.com/webhook",
		"ftp://example.com/webhook",
		"https://127.0.0.1/webhook",
		"https://127.0.0.1:8080/webhook",
		"https://169.254.169.254/latest/meta-data/",
		"https://10.0.0.1/webhook",
		"https://172.16.0.1/webhook",
		"https://192.168.1.1/webhook",
		"https://100.64.0.1/webhook",
		"https://[::1]/webhook",
		"https://[fc00::1]/webhook",
		"https://[fe80::1]/webhook",
		"https://[::ffff:127.0.0.1]/webhook",
		"https://localhost/webhook",
	}

	for _, endpoint := range rejectedURLs {
		if err := d.sendSubscriberWebhook(ctx, endpoint, payload); err == nil {
			t.Errorf("expected sendSubscriberWebhook(%q) to fail, but it succeeded", endpoint)
		}
	}

	// Verify 3xx redirects are strictly refused even over HTTPS.
	redirectHits := 0
	targetHits := 0
	redirSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bounce" {
			redirectHits++
			http.Redirect(w, r, "/internal", http.StatusFound)
			return
		}
		targetHits++
		w.WriteHeader(http.StatusOK)
	}))
	defer redirSrv.Close()

	redirDisp := NewDispatcher(nil)
	tr := redirSrv.Client().Transport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, redirSrv.Listener.Addr().String())
	}
	redirDisp.subscriberClient.Transport = tr

	if err := redirDisp.sendSubscriberWebhook(ctx, "https://example.com/bounce", payload); err == nil {
		t.Fatal("expected sendSubscriberWebhook to reject 302 redirect")
	}
	if redirectHits != 1 {
		t.Fatalf("expected 1 hit to /bounce, got %d", redirectHits)
	}
	if targetHits != 0 {
		t.Fatalf("expected 0 hits to redirected target, got %d", targetHits)
	}
}

func TestSendDiscord_TruncatesOversizedFieldsAndIncludesErrorBody(t *testing.T) {
	var captured struct {
		Username string `json:"username"`
		Embeds   []struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			Fields      []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"fields"`
		} `json:"embeds"`
	}

	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decode discord payload: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer okSrv.Close()

	d := NewDispatcher(nil)
	err := d.sendDiscord(context.Background(), model.DiscordConfig{
		WebhookURL: okSrv.URL,
		Username:   strings.Repeat("x", 120),
	}, notificationPayload{
		Event:     "monitor_state_change",
		Title:     strings.Repeat("T", 320),
		Message:   strings.Repeat("M", 5000),
		Severity:  "critical",
		Timestamp: time.Now().UTC(),
		Monitor: &monitorEventPayload{
			Name:          strings.Repeat("N", 1200),
			CurrentStatus: "",
		},
	})
	if err != nil {
		t.Fatalf("expected truncated discord payload to succeed, got: %v", err)
	}
	if len([]rune(captured.Username)) > 80 {
		t.Errorf("expected username <= 80 runes, got %d", len([]rune(captured.Username)))
	}
	if len(captured.Embeds) != 1 {
		t.Fatalf("expected 1 embed, got %d", len(captured.Embeds))
	}
	if len([]rune(captured.Embeds[0].Title)) > 256 {
		t.Errorf("expected title <= 256 runes, got %d", len([]rune(captured.Embeds[0].Title)))
	}
	if len([]rune(captured.Embeds[0].Description)) > 4000 {
		t.Errorf("expected description <= 4000 runes, got %d", len([]rune(captured.Embeds[0].Description)))
	}
	for _, f := range captured.Embeds[0].Fields {
		if f.Value == "" {
			t.Errorf("expected non-empty field value for %q", f.Name)
		}
		if len([]rune(f.Value)) > 1024 {
			t.Errorf("expected field %q value <= 1024 runes, got %d", f.Name, len([]rune(f.Value)))
		}
	}

	errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"Invalid Form Body"}`))
	}))
	defer errSrv.Close()

	err = d.sendDiscord(context.Background(), model.DiscordConfig{WebhookURL: errSrv.URL}, notificationPayload{
		Event:     "test",
		Title:     "Test",
		Message:   "Test",
		Severity:  "info",
		Timestamp: time.Now().UTC(),
	})
	if err == nil || !strings.Contains(err.Error(), "Invalid Form Body") {
		t.Fatalf("expected HTTP 400 error body to surface, got: %v", err)
	}
}
