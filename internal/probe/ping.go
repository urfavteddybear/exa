package probe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/url"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"exa/internal/model"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

var (
	reWinAvg    = regexp.MustCompile(`(?i)Average\s*=\s*(\d+)\s*ms`)
	reUnixAvg   = regexp.MustCompile(`(?i)(?:rtt|round-trip)\s+min/avg/max(?:/[a-z]+)?\s*=\s*[\d.]+/([\d.]+)/`)
	rePacketRTT = regexp.MustCompile(`(?i)time\s*([<=])\s*([\d.]+)\s*ms`)
	// Localized Windows output ("Zeit=1ms", "temps=1 ms", "waktu<1ms") still ends each reply with "ms TTL=".
	reReplyTTL = regexp.MustCompile(`(?i)([<=])\s*([\d.]+)\s*ms\s+TTL=`)
)

func probePing(ctx context.Context, m model.Monitor, timeout time.Duration) model.ProbeResult {
	cfg, err := parseConfig[model.PingConfig](m.Config)
	if err != nil {
		return model.ProbeResult{
			Status:    model.HeartbeatStatusDown,
			Message:   fmt.Sprintf("invalid ping config: %v", err),
			CheckedAt: time.Now().UTC(),
		}
	}
	cfg = cfg.WithDefaults()

	host := extractPingHost(m.Target)
	if !isValidPingHost(host) {
		return model.ProbeResult{
			Status:    model.HeartbeatStatusDown,
			Message:   "invalid ping host",
			CheckedAt: time.Now().UTC(),
		}
	}

	latencyMs, fallback, err := icmpPing(ctx, host, cfg.PacketCount, timeout)
	if fallback {
		latencyMs, err = osPing(ctx, host, cfg.PacketCount, timeout)
	}

	now := time.Now().UTC()
	if err != nil {
		return model.ProbeResult{
			Status:    model.HeartbeatStatusDown,
			LatencyMs: latencyMs,
			Message:   err.Error(),
			CheckedAt: now,
		}
	}

	res := model.ProbeResult{
		Status:    model.HeartbeatStatusUp,
		LatencyMs: latencyMs,
		Message:   fmt.Sprintf("ping %s OK (%dms)", host, latencyMs),
		CheckedAt: now,
	}
	applyDegradedThreshold(&res, m.DegradedMs)
	return res
}

func extractPingHost(target string) string {
	t := strings.TrimSpace(target)
	if t == "" {
		return ""
	}
	if strings.Contains(t, "://") {
		if u, err := url.Parse(t); err == nil && u.Hostname() != "" {
			return u.Hostname()
		}
	}
	if h, _, err := net.SplitHostPort(t); err == nil && h != "" {
		return h
	}
	if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
		inner := t[1 : len(t)-1]
		if net.ParseIP(inner) != nil {
			return inner
		}
	}
	return t
}

func isValidPingHost(host string) bool {
	if host == "" {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	if len(host) > 253 || host[0] == '-' {
		return false
	}
	for i := 0; i < len(host); i++ {
		c := host[i]
		isAlpha := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		isDigit := c >= '0' && c <= '9'
		if !isAlpha && !isDigit && c != '.' && c != '-' {
			return false
		}
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if len(label) < 1 || len(label) > 63 {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
	}
	return true
}

func orderPingTargets(ips []net.IPAddr) []net.IPAddr {
	var firstV4, firstV6 *net.IPAddr
	for i := range ips {
		ip := ips[i]
		if ip.IP.To4() != nil {
			if firstV4 == nil {
				v4Copy := ip
				firstV4 = &v4Copy
			}
		} else if firstV6 == nil {
			v6Copy := ip
			firstV6 = &v6Copy
		}
		if firstV4 != nil && firstV6 != nil {
			break
		}
	}

	out := make([]net.IPAddr, 0, 2)
	if firstV4 != nil {
		out = append(out, *firstV4)
	}
	if firstV6 != nil {
		out = append(out, *firstV6)
	}
	return out
}

func openICMPConn(isV6 bool) (*icmp.PacketConn, bool, error) {
	listenAddr := "0.0.0.0"
	dgramNet := "udp4"
	rawNet := "ip4:icmp"
	if isV6 {
		listenAddr = "::"
		dgramNet = "udp6"
		rawNet = "ip6:ipv6-icmp"
	}

	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		conn, err := icmp.ListenPacket(dgramNet, listenAddr)
		if err == nil {
			return conn, true, nil
		}
	}

	conn, err := icmp.ListenPacket(rawNet, listenAddr)
	if err != nil {
		return nil, false, err
	}
	return conn, false, nil
}

func icmpDestAddr(target net.IPAddr, isDatagram bool) net.Addr {
	ip := target.IP
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if isDatagram {
		return &net.UDPAddr{IP: ip, Zone: target.Zone}
	}
	return &net.IPAddr{IP: ip, Zone: target.Zone}
}

func matchEchoReply(echo *icmp.Echo, id, seq int, payload []byte, isDatagram bool) bool {
	if echo == nil || echo.Seq != seq {
		return false
	}
	// Linux datagram ICMP sockets overwrite echo.ID with the bound local port and demux replies by socket.
	if !isDatagram && echo.ID != id {
		return false
	}
	return bytes.Equal(echo.Data, payload)
}

func icmpPing(ctx context.Context, host string, count int, timeout time.Duration) (int, bool, error) {
	if count <= 0 {
		count = 2
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(ips) == 0 {
		if err == nil {
			err = fmt.Errorf("no IP addresses resolved for %s", host)
		}
		return 0, false, err
	}

	candidates := orderPingTargets(ips)
	openedAny := false
	var lastOpenErr error
	var lastWriteErr error

	for _, targetIP := range candidates {
		isV6 := targetIP.IP.To4() == nil
		proto := 1 // ICMPv4
		var reqType icmp.Type = ipv4.ICMPTypeEcho
		var replyType icmp.Type = ipv4.ICMPTypeEchoReply
		if isV6 {
			proto = 58 // ICMPv6
			reqType = ipv6.ICMPTypeEchoRequest
			replyType = ipv6.ICMPTypeEchoReply
		}

		conn, isDatagram, openErr := openICMPConn(isV6)
		if openErr != nil {
			// Track open error and let caller fall back to OS ping if all candidates lack socket access.
			lastOpenErr = openErr
			continue
		}
		openedAny = true

		avgMs, sent, pingErr := icmpPingTarget(ctx, conn, targetIP, isDatagram, proto, reqType, replyType, count, timeout)
		if pingErr == nil {
			return avgMs, false, nil
		}
		if sent > 0 || ctx.Err() != nil {
			return 0, false, pingErr
		}
		lastWriteErr = pingErr
	}

	if !openedAny {
		return 0, true, lastOpenErr
	}
	if lastWriteErr != nil {
		return 0, false, lastWriteErr
	}
	return 0, false, errors.New("ping timed out: 0 replies received")
}

func icmpPingTarget(
	ctx context.Context,
	conn *icmp.PacketConn,
	targetIP net.IPAddr,
	isDatagram bool,
	proto int,
	reqType icmp.Type,
	replyType icmp.Type,
	count int,
	timeout time.Duration,
) (int, int, error) {
	defer conn.Close()

	id := rand.IntN(0xffff)
	payload := []byte(fmt.Sprintf("exa-%04x-%08x", id, rand.Uint32()))
	dst := icmpDestAddr(targetIP, isDatagram)

	perPacketTimeout := timeout / time.Duration(count)
	if perPacketTimeout < 500*time.Millisecond {
		perPacketTimeout = timeout
	}

	var totalRTT time.Duration
	received := 0
	sent := 0
	var lastWriteErr error
	buf := make([]byte, 1500)

	for seq := 1; seq <= count; seq++ {
		if ctx.Err() != nil {
			return 0, sent, ctx.Err()
		}
		msg := icmp.Message{
			Type: reqType,
			Code: 0,
			Body: &icmp.Echo{
				ID:   id,
				Seq:  seq,
				Data: payload,
			},
		}
		wire, err := msg.Marshal(nil)
		if err != nil {
			return 0, sent, err
		}

		sentAt := time.Now()
		deadline := sentAt.Add(perPacketTimeout)
		if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
			deadline = ctxDeadline
		}
		_ = conn.SetDeadline(deadline)

		if _, err := conn.WriteTo(wire, dst); err != nil {
			lastWriteErr = err
			if sent == 0 {
				return 0, 0, err
			}
			continue
		}
		sent++

		for {
			n, _, err := conn.ReadFrom(buf)
			if err != nil {
				break
			}
			rm, err := icmp.ParseMessage(proto, buf[:n])
			if err != nil || rm.Type != replyType {
				continue
			}
			echo, ok := rm.Body.(*icmp.Echo)
			if !ok || !matchEchoReply(echo, id, seq, payload, isDatagram) {
				continue
			}
			totalRTT += time.Since(sentAt)
			received++
			break
		}
	}

	if received == 0 {
		if lastWriteErr != nil {
			return 0, sent, lastWriteErr
		}
		return 0, sent, errors.New("ping timed out: 0 replies received")
	}
	avgMs := int((totalRTT / time.Duration(received)).Milliseconds())
	return avgMs, sent, nil
}

func osPing(ctx context.Context, host string, count int, timeout time.Duration) (int, error) {
	if count <= 0 {
		count = 2
	}
	timeoutMs := int(timeout.Milliseconds())
	if timeoutMs <= 0 {
		timeoutMs = 1000
	}
	timeoutSec := int(timeout.Seconds())
	if timeoutSec <= 0 {
		timeoutSec = 1
	}

	var args []string
	switch runtime.GOOS {
	case "windows":
		args = []string{"-n", strconv.Itoa(count), "-w", strconv.Itoa(timeoutMs), host}
	case "linux":
		args = []string{"-c", strconv.Itoa(count), "-W", strconv.Itoa(timeoutSec), "--", host}
	default:
		args = []string{"-c", strconv.Itoa(count), "-W", strconv.Itoa(timeoutMs), "--", host}
	}

	cmd := exec.CommandContext(ctx, "ping", args...)
	out, err := cmd.CombinedOutput()
	outStr := string(out)

	rtt, ok := parsePingOutput(outStr)
	if err != nil {
		if !ok {
			msg := strings.TrimSpace(outStr)
			if msg == "" {
				msg = err.Error()
			}
			return 0, fmt.Errorf("ping failed: %s", msg)
		}
	}
	if !ok {
		return 0, errors.New("ping failed: host unreachable or no RTT reported")
	}
	return rtt, nil
}

func parsePingOutput(output string) (int, bool) {
	if m := reWinAvg.FindStringSubmatch(output); len(m) == 2 {
		if v, err := strconv.Atoi(m[1]); err == nil {
			return v, true
		}
	}
	if m := reUnixAvg.FindStringSubmatch(output); len(m) == 2 {
		if f, err := strconv.ParseFloat(m[1], 64); err == nil {
			return int(f + 0.5), true
		}
	}

	matches := rePacketRTT.FindAllStringSubmatch(output, -1)
	if len(matches) == 0 {
		matches = reReplyTTL.FindAllStringSubmatch(output, -1)
	}
	if len(matches) == 0 {
		return 0, false
	}
	var sum float64
	for _, m := range matches {
		if m[1] == "<" {
			sum += 0
			continue
		}
		if f, err := strconv.ParseFloat(m[2], 64); err == nil {
			sum += f
		}
	}
	avg := sum / float64(len(matches))
	return int(avg + 0.5), true
}
