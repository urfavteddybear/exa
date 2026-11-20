package probe

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"context"
	"exa/internal/model"
)

const maxHTTPBodyBytes = 4 << 20

func probeHTTP(ctx context.Context, m model.Monitor, timeout time.Duration) model.ProbeResult {
	cfg, err := parseConfig[model.HTTPConfig](m.Config)
	if err != nil {
		return model.ProbeResult{
			Status:    model.HeartbeatStatusDown,
			Message:   fmt.Sprintf("invalid HTTP config: %v", err),
			CheckedAt: time.Now().UTC(),
		}
	}
	cfg = cfg.WithDefaults()

	target := strings.TrimSpace(m.Target)
	if target == "" {
		return model.ProbeResult{
			Status:    model.HeartbeatStatusDown,
			Message:   "target URL is empty",
			CheckedAt: time.Now().UTC(),
		}
	}
	if !strings.Contains(target, "://") {
		target = "http://" + target
	}
	parsedURL, err := url.Parse(target)
	if err != nil || parsedURL.Host == "" {
		return model.ProbeResult{
			Status:    model.HeartbeatStatusDown,
			Message:   fmt.Sprintf("invalid target URL %q", m.Target),
			CheckedAt: time.Now().UTC(),
		}
	}

	method := strings.ToUpper(strings.TrimSpace(cfg.Method))
	if method == "" {
		method = http.MethodGet
	}

	var bodyReader io.Reader
	if cfg.Body != "" {
		bodyReader = strings.NewReader(cfg.Body)
	}

	req, err := http.NewRequestWithContext(ctx, method, target, bodyReader)
	if err != nil {
		return model.ProbeResult{
			Status:    model.HeartbeatStatusDown,
			Message:   fmt.Sprintf("create request: %v", err),
			CheckedAt: time.Now().UTC(),
		}
	}
	req.Header.Set("User-Agent", "Exa-Monitor/1.0")
	for k, v := range cfg.Headers {
		if strings.EqualFold(k, "Host") {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: cfg.IgnoreTLS,
		},
		DisableKeepAlives: true,
		DialContext: (&net.Dialer{
			Timeout: timeout,
		}).DialContext,
	}
	defer tr.CloseIdleConnections()

	client := &http.Client{
		Transport: tr,
		Timeout:   timeout,
	}
	if cfg.FollowRedirects != nil && !*cfg.FollowRedirects {
		client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}

	start := time.Now()
	resp, err := client.Do(req)
	latencyMs := int(time.Since(start).Milliseconds())
	now := time.Now().UTC()

	if err != nil {
		return model.ProbeResult{
			Status:    model.HeartbeatStatusDown,
			LatencyMs: latencyMs,
			Message:   err.Error(),
			CheckedAt: now,
		}
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxHTTPBodyBytes))
	if err != nil {
		return model.ProbeResult{
			Status:     model.HeartbeatStatusDown,
			LatencyMs:  latencyMs,
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("read response body: %v", err),
			CheckedAt:  now,
		}
	}

	res := model.ProbeResult{
		Status:     model.HeartbeatStatusUp,
		LatencyMs:  latencyMs,
		StatusCode: resp.StatusCode,
		CheckedAt:  now,
	}

	// Extract leaf certificate expiration before any early validation returns.
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		exp := resp.TLS.PeerCertificates[0].NotAfter.UTC()
		daysLeft := int(math.Floor(exp.Sub(now).Hours() / 24.0))
		res.TLSExpiresAt = &exp
		res.TLSDaysLeft = &daysLeft
	}

	okStatus, err := matchStatusCode(resp.StatusCode, cfg.ExpectedStatus)
	if err != nil {
		res.Status = model.HeartbeatStatusDown
		res.Message = err.Error()
		return res
	}
	if !okStatus {
		res.Status = model.HeartbeatStatusDown
		res.Message = fmt.Sprintf("unexpected status code %d (expected %s)", resp.StatusCode, cfg.ExpectedStatus)
		return res
	}

	if cfg.Keyword != "" {
		found := strings.Contains(string(bodyBytes), cfg.Keyword)
		if !cfg.KeywordInvert && !found {
			res.Status = model.HeartbeatStatusDown
			res.Message = fmt.Sprintf("keyword %q not found in response", cfg.Keyword)
			return res
		}
		if cfg.KeywordInvert && found {
			res.Status = model.HeartbeatStatusDown
			res.Message = fmt.Sprintf("inverted keyword %q found in response", cfg.Keyword)
			return res
		}
	}

	if cfg.JSONPath != "" {
		val, found, err := evalJSONPath(bodyBytes, cfg.JSONPath)
		if err != nil {
			res.Status = model.HeartbeatStatusDown
			res.Message = fmt.Sprintf("invalid JSON response: %v", err)
			return res
		}
		if !found {
			res.Status = model.HeartbeatStatusDown
			res.Message = fmt.Sprintf("JSON path %q not found", cfg.JSONPath)
			return res
		}
		if cfg.JSONExpected != "" && val != cfg.JSONExpected {
			res.Status = model.HeartbeatStatusDown
			res.Message = fmt.Sprintf("JSON path %q got %q, expected %q", cfg.JSONPath, val, cfg.JSONExpected)
			return res
		}
	}

	if res.TLSExpiresAt != nil {
		if !res.TLSExpiresAt.After(now) {
			res.Status = model.HeartbeatStatusDown
			res.Message = fmt.Sprintf("TLS certificate expired at %s", res.TLSExpiresAt.Format(time.RFC3339))
			return res
		}
		if res.TLSDaysLeft != nil && *res.TLSDaysLeft <= cfg.TLSExpiryWarnDays {
			res.Status = model.HeartbeatStatusDegraded
			res.Message = fmt.Sprintf("TLS certificate expires in %d days", *res.TLSDaysLeft)
		}
	}

	applyDegradedThreshold(&res, m.DegradedMs)
	if res.Status == model.HeartbeatStatusUp && res.Message == "" {
		res.Message = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	return res
}

func matchStatusCode(code int, spec string) (bool, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		spec = "200-299"
	}
	parts := strings.Split(spec, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if loStr, hiStr, hasRange := strings.Cut(part, "-"); hasRange {
			lo, err1 := strconv.Atoi(strings.TrimSpace(loStr))
			hi, err2 := strconv.Atoi(strings.TrimSpace(hiStr))
			if err1 != nil || err2 != nil {
				return false, fmt.Errorf("invalid status range %q", part)
			}
			if code >= lo && code <= hi {
				return true, nil
			}
			continue
		}
		exact, err := strconv.Atoi(part)
		if err != nil {
			return false, fmt.Errorf("invalid status code %q", part)
		}
		if code == exact {
			return true, nil
		}
	}
	return false, nil
}

func evalJSONPath(raw []byte, path string) (string, bool, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var root any
	if err := dec.Decode(&root); err != nil {
		return "", false, err
	}

	tokens := parseJSONPathTokens(path)
	if len(tokens) == 0 {
		return formatJSONValue(root), true, nil
	}

	curr := root
	for _, tok := range tokens {
		switch node := curr.(type) {
		case map[string]any:
			val, ok := node[tok]
			if !ok {
				return "", false, nil
			}
			curr = val
		case []any:
			idx, err := strconv.Atoi(tok)
			if err != nil || idx < 0 || idx >= len(node) {
				return "", false, nil
			}
			curr = node[idx]
		default:
			return "", false, nil
		}
	}
	return formatJSONValue(curr), true, nil
}

func parseJSONPathTokens(path string) []string {
	p := strings.TrimSpace(path)
	p = strings.TrimPrefix(p, "$.")
	p = strings.TrimPrefix(p, "$")
	p = strings.ReplaceAll(p, "[", ".")
	p = strings.ReplaceAll(p, "]", "")
	rawTokens := strings.Split(p, ".")
	tokens := make([]string, 0, len(rawTokens))
	for _, tok := range rawTokens {
		if tok != "" {
			tokens = append(tokens, tok)
		}
	}
	return tokens
}

func formatJSONValue(v any) string {
	switch val := v.(type) {
	case nil:
		return "null"
	case string:
		return val
	case json.Number:
		return val.String()
	case bool:
		return strconv.FormatBool(val)
	default:
		b, err := json.Marshal(val)
		if err != nil {
			return fmt.Sprintf("%v", val)
		}
		return string(b)
	}
}
