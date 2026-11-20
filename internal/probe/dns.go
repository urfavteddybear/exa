package probe

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"exa/internal/model"
)

func probeDNS(ctx context.Context, m model.Monitor, timeout time.Duration) model.ProbeResult {
	cfg, err := parseConfig[model.DNSConfig](m.Config)
	if err != nil {
		return model.ProbeResult{
			Status:    model.HeartbeatStatusDown,
			Message:   fmt.Sprintf("invalid DNS config: %v", err),
			CheckedAt: time.Now().UTC(),
		}
	}
	cfg = cfg.WithDefaults()

	host := strings.TrimSpace(m.Target)
	if host == "" {
		return model.ProbeResult{
			Status:    model.HeartbeatStatusDown,
			Message:   "DNS target host is empty",
			CheckedAt: time.Now().UTC(),
		}
	}

	resolver := net.DefaultResolver
	if resAddr := strings.TrimSpace(cfg.Resolver); resAddr != "" {
		addr := normalizeResolverAddr(resAddr)
		resolver = &net.Resolver{
			PreferGo: true,
			Dial: func(dialCtx context.Context, network, _ string) (net.Conn, error) {
				d := net.Dialer{Timeout: timeout}
				return d.DialContext(dialCtx, network, addr)
			},
		}
	}

	recordType := strings.ToUpper(strings.TrimSpace(cfg.RecordType))
	start := time.Now()
	records, err := lookupDNSRecords(ctx, resolver, recordType, host)
	latencyMs := int(time.Since(start).Milliseconds())
	now := time.Now().UTC()

	if err != nil {
		return model.ProbeResult{
			Status:    model.HeartbeatStatusDown,
			LatencyMs: latencyMs,
			Message:   fmt.Sprintf("DNS %s lookup failed: %v", recordType, err),
			CheckedAt: now,
		}
	}
	if len(records) == 0 {
		return model.ProbeResult{
			Status:    model.HeartbeatStatusDown,
			LatencyMs: latencyMs,
			Message:   fmt.Sprintf("DNS %s lookup returned no records", recordType),
			CheckedAt: now,
		}
	}

	if exp := strings.TrimSpace(cfg.ExpectedValue); exp != "" {
		if !matchDNSRecord(records, exp) {
			return model.ProbeResult{
				Status:    model.HeartbeatStatusDown,
				LatencyMs: latencyMs,
				Message:   fmt.Sprintf("DNS %s expected %q not found in [%s]", recordType, exp, strings.Join(records, ", ")),
				CheckedAt: now,
			}
		}
	}

	res := model.ProbeResult{
		Status:    model.HeartbeatStatusUp,
		LatencyMs: latencyMs,
		Message:   fmt.Sprintf("DNS %s resolved (%d records)", recordType, len(records)),
		CheckedAt: now,
	}
	applyDegradedThreshold(&res, m.DegradedMs)
	return res
}

func normalizeResolverAddr(addr string) string {
	if _, _, err := net.SplitHostPort(addr); err == nil {
		return addr
	}
	trimmed := strings.Trim(addr, "[]")
	return net.JoinHostPort(trimmed, "53")
}

func lookupDNSRecords(ctx context.Context, r *net.Resolver, recordType, host string) ([]string, error) {
	switch recordType {
	case "A":
		ips, err := r.LookupIP(ctx, "ip4", host)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(ips))
		for _, ip := range ips {
			out = append(out, ip.String())
		}
		return out, nil
	case "AAAA":
		ips, err := r.LookupIP(ctx, "ip6", host)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(ips))
		for _, ip := range ips {
			out = append(out, ip.String())
		}
		return out, nil
	case "CNAME":
		cname, err := r.LookupCNAME(ctx, host)
		if err != nil {
			return nil, err
		}
		if cname == "" {
			return nil, nil
		}
		return []string{strings.TrimSuffix(cname, ".")}, nil
	case "MX":
		mxs, err := r.LookupMX(ctx, host)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(mxs))
		for _, mx := range mxs {
			out = append(out, strings.TrimSuffix(mx.Host, "."))
		}
		return out, nil
	case "TXT":
		return r.LookupTXT(ctx, host)
	case "NS":
		nss, err := r.LookupNS(ctx, host)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(nss))
		for _, ns := range nss {
			out = append(out, strings.TrimSuffix(ns.Host, "."))
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported record type %q", recordType)
	}
}

func matchDNSRecord(records []string, expected string) bool {
	normExp := strings.TrimSuffix(strings.TrimSpace(expected), ".")
	for _, rec := range records {
		if strings.TrimSuffix(strings.TrimSpace(rec), ".") == normExp {
			return true
		}
	}
	return false
}
