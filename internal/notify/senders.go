package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"time"

	"exa/internal/model"
)

var blockedSubscriberNets = mustParseCIDRs([]string{
	"0.0.0.0/8",
	"10.0.0.0/8",
	"100.64.0.0/10",
	"127.0.0.0/8",
	"169.254.0.0/16",
	"172.16.0.0/12",
	"192.0.0.0/24",
	"192.0.2.0/24",
	"192.88.99.0/24",
	"192.168.0.0/16",
	"198.18.0.0/15",
	"198.51.100.0/24",
	"203.0.113.0/24",
	"224.0.0.0/4",
	"240.0.0.0/4",
	"::/128",
	"::1/128",
	"64:ff9b::/96",
	"64:ff9b:1::/48",
	"100::/64",
	"2001::/32",
	"2001:2::/48",
	"2001:db8::/32",
	"fc00::/7",
	"fe80::/10",
	"fec0::/10",
	"ff00::/8",
})

func mustParseCIDRs(cidrs []string) []*net.IPNet {
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(fmt.Sprintf("invalid CIDR %q: %v", cidr, err))
		}
		nets = append(nets, ipNet)
	}
	return nets
}

func isPublicRoutableIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	// Normalize IPv4-mapped IPv6 addresses so ::ffff:127.0.0.1 is checked against IPv4 blocks.
	if ip4 := ip.To4(); ip4 != nil {
		ip = ip4
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || !ip.IsGlobalUnicast() {
		return false
	}
	for _, blocked := range blockedSubscriberNets {
		if blocked.Contains(ip) {
			return false
		}
	}
	return true
}

func subscriberDialContext(resolver *net.Resolver) func(ctx context.Context, network, addr string) (net.Conn, error) {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	dialer := &net.Dialer{Timeout: defaultNotifyTimeout}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("invalid subscriber address %q: %w", addr, err)
		}
		ips, err := resolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("resolve subscriber host %q: %w", host, err)
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("subscriber host %q resolved no IP addresses", host)
		}
		for _, ipAddr := range ips {
			if !isPublicRoutableIP(ipAddr.IP) {
				return nil, fmt.Errorf("subscriber target %q resolves to non-public IP %s", host, ipAddr.IP.String())
			}
		}
		target := net.JoinHostPort(ips[0].IP.String(), port)
		return dialer.DialContext(ctx, network, target)
	}
}

func (d *Dispatcher) sendSubscriberWebhook(ctx context.Context, endpoint string, payload notificationPayload) error {
	targetURL := strings.TrimSpace(endpoint)
	if targetURL == "" {
		return errors.New("subscriber webhook url is required")
	}
	parsed, err := url.Parse(targetURL)
	if err != nil || parsed.Hostname() == "" {
		return errors.New("invalid subscriber webhook url")
	}
	if !strings.EqualFold(parsed.Scheme, "https") {
		return errors.New("subscriber webhook url must use https")
	}
	if ip := net.ParseIP(parsed.Hostname()); ip != nil && !isPublicRoutableIP(ip) {
		return fmt.Errorf("subscriber webhook target IP %s is not publicly routable", ip.String())
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Exa-Notifier/1.0")

	client := d.subscriberClient
	if client == nil {
		client = newSubscriberHTTPClient()
	}
	return doRequestWithClient(client, req)
}

func (d *Dispatcher) sendWebhook(ctx context.Context, cfg model.WebhookConfig, payload notificationPayload) error {
	targetURL := strings.TrimSpace(cfg.URL)
	if targetURL == "" {
		return errors.New("webhook url is required")
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Exa-Notifier/1.0")
	for k, v := range cfg.Headers {
		req.Header.Set(k, v)
	}
	if cfg.Secret != "" {
		mac := hmac.New(sha256.New, []byte(cfg.Secret))
		mac.Write(body)
		req.Header.Set("X-Exa-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}

	return d.doJSONRequest(req)
}

func (d *Dispatcher) sendDiscord(ctx context.Context, cfg model.DiscordConfig, payload notificationPayload) error {
	targetURL := strings.TrimSpace(cfg.WebhookURL)
	if targetURL == "" {
		return errors.New("discord webhook_url is required")
	}

	username := strings.TrimSpace(cfg.Username)
	if username == "" {
		username = "Exa"
	}

	fields := []map[string]any{
		{"name": "Event", "value": payload.Event, "inline": true},
		{"name": "Severity", "value": payload.Severity, "inline": true},
	}
	if payload.Monitor != nil {
		fields = append(fields,
			map[string]any{"name": "Monitor", "value": payload.Monitor.Name, "inline": true},
			map[string]any{"name": "Status", "value": string(payload.Monitor.CurrentStatus), "inline": true},
		)
	}

	bodyStruct := map[string]any{
		"username": username,
		"embeds": []map[string]any{
			{
				"title":       payload.Title,
				"description": payload.Message,
				"color":       discordColor(payload.Severity),
				"timestamp":   payload.Timestamp.UTC().Format(time.RFC3339),
				"fields":      fields,
			},
		},
	}

	return d.postJSON(ctx, targetURL, bodyStruct, nil)
}

func (d *Dispatcher) sendSlack(ctx context.Context, cfg model.SlackConfig, payload notificationPayload) error {
	targetURL := strings.TrimSpace(cfg.WebhookURL)
	if targetURL == "" {
		return errors.New("slack webhook_url is required")
	}

	attachment := map[string]any{
		"color": slackColor(payload.Severity),
		"title": payload.Title,
		"text":  payload.Message,
		"ts":    payload.Timestamp.UTC().Unix(),
		"blocks": []map[string]any{
			{
				"type": "section",
				"text": map[string]any{
					"type": "mrkdwn",
					"text": fmt.Sprintf("*%s*\n%s", payload.Title, payload.Message),
				},
			},
		},
	}

	bodyStruct := map[string]any{
		"text":        payload.Title,
		"attachments": []map[string]any{attachment},
	}
	if strings.TrimSpace(cfg.Channel) != "" {
		bodyStruct["channel"] = strings.TrimSpace(cfg.Channel)
	}

	return d.postJSON(ctx, targetURL, bodyStruct, nil)
}

func (d *Dispatcher) sendTelegram(ctx context.Context, cfg model.TelegramConfig, payload notificationPayload) error {
	token := strings.TrimSpace(cfg.BotToken)
	chatID := strings.TrimSpace(cfg.ChatID)
	if token == "" || chatID == "" {
		return errors.New("telegram bot_token and chat_id are required")
	}

	base := strings.TrimRight(d.telegramBaseURL, "/")
	endpoint := fmt.Sprintf("%s/bot%s/sendMessage", base, url.PathEscape(token))
	text := fmt.Sprintf("<b>%s</b>\n%s", html.EscapeString(payload.Title), html.EscapeString(payload.Message))

	bodyStruct := map[string]any{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "HTML",
	}

	return d.postJSON(ctx, endpoint, bodyStruct, nil)
}

func (d *Dispatcher) sendEmail(ctx context.Context, cfg model.EmailConfig, payload notificationPayload) error {
	host := strings.TrimSpace(cfg.SMTPHost)
	if host == "" || cfg.SMTPPort <= 0 || strings.TrimSpace(cfg.From) == "" || len(cfg.To) == 0 {
		return errors.New("email smtp_host, smtp_port, from, and to are required")
	}

	addr := net.JoinHostPort(host, strconv.Itoa(cfg.SMTPPort))
	dialer := &net.Dialer{Timeout: defaultNotifyTimeout}
	tlsCfg := &tls.Config{ServerName: host}

	var conn net.Conn
	var err error
	if cfg.UseTLS && cfg.SMTPPort == 465 {
		tlsDialer := &tls.Dialer{NetDialer: dialer, Config: tlsCfg}
		conn, err = tlsDialer.DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp client: %w", err)
	}
	defer client.Close()

	if cfg.UseTLS && cfg.SMTPPort != 465 {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(tlsCfg); err != nil {
				return fmt.Errorf("smtp starttls: %w", err)
			}
		}
	}

	if cfg.Username != "" {
		auth := smtp.PlainAuth("", cfg.Username, cfg.Password, host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}

	from := sanitizeHeader(cfg.From)
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	cleanTo := make([]string, 0, len(cfg.To))
	for _, rcpt := range cfg.To {
		r := sanitizeHeader(strings.TrimSpace(rcpt))
		if r == "" {
			continue
		}
		if err := client.Rcpt(r); err != nil {
			return fmt.Errorf("smtp rcpt %q: %w", r, err)
		}
		cleanTo = append(cleanTo, r)
	}
	if len(cleanTo) == 0 {
		return errors.New("no valid email recipients")
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}

	msg := strings.Join([]string{
		"From: " + from,
		"To: " + strings.Join(cleanTo, ", "),
		"Subject: " + sanitizeHeader(payload.Title),
		"Date: " + payload.Timestamp.UTC().Format(time.RFC1123Z),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"",
		payload.Message,
		"",
	}, "\r\n")

	if _, err := io.WriteString(w, msg); err != nil {
		_ = w.Close()
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp close data: %w", err)
	}
	return client.Quit()
}

func (d *Dispatcher) sendNtfy(ctx context.Context, cfg model.NtfyConfig, payload notificationPayload) error {
	cfg = cfg.WithDefaults()
	topic := strings.TrimLeft(strings.TrimSpace(cfg.Topic), "/")
	if topic == "" {
		return errors.New("ntfy topic is required")
	}
	endpoint := strings.TrimRight(strings.TrimSpace(cfg.ServerURL), "/") + "/" + topic

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(payload.Message))
	if err != nil {
		return err
	}
	req.Header.Set("Title", payload.Title)
	if strings.TrimSpace(cfg.Priority) != "" {
		req.Header.Set("Priority", strings.TrimSpace(cfg.Priority))
	}
	if strings.TrimSpace(cfg.Token) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(cfg.Token))
	}

	return d.doJSONRequest(req)
}

func (d *Dispatcher) postJSON(ctx context.Context, targetURL string, body any, headers map[string]string) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Exa-Notifier/1.0")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return d.doJSONRequest(req)
}

func (d *Dispatcher) doJSONRequest(req *http.Request) error {
	return doRequestWithClient(d.client, req)
}

func doRequestWithClient(client *http.Client, req *http.Request) error {
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 16<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("notification endpoint returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func sanitizeHeader(v string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(v)
}

func discordColor(severity string) int {
	switch severity {
	case "resolved":
		return 0x15803D
	case "warning":
		return 0xB45309
	case "critical":
		return 0xB91C1C
	default:
		return 0x1D4ED8
	}
}

func slackColor(severity string) string {
	switch severity {
	case "resolved":
		return "#15803D"
	case "warning":
		return "#B45309"
	case "critical":
		return "#B91C1C"
	default:
		return "#1D4ED8"
	}
}
