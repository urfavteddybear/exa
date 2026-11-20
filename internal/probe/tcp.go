package probe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"exa/internal/model"
)

const maxTCPReadBytes = 64 << 10

func probeTCP(ctx context.Context, m model.Monitor, timeout time.Duration) model.ProbeResult {
	cfg, err := parseConfig[model.TCPConfig](m.Config)
	if err != nil {
		return model.ProbeResult{
			Status:    model.HeartbeatStatusDown,
			Message:   fmt.Sprintf("invalid TCP config: %v", err),
			CheckedAt: time.Now().UTC(),
		}
	}

	target := strings.TrimSpace(m.Target)
	if target == "" {
		return model.ProbeResult{
			Status:    model.HeartbeatStatusDown,
			Message:   "TCP target is empty",
			CheckedAt: time.Now().UTC(),
		}
	}

	dialer := &net.Dialer{Timeout: timeout}
	start := time.Now()
	conn, err := dialer.DialContext(ctx, "tcp", target)
	now := time.Now().UTC()
	if err != nil {
		return model.ProbeResult{
			Status:    model.HeartbeatStatusDown,
			LatencyMs: int(time.Since(start).Milliseconds()),
			Message:   err.Error(),
			CheckedAt: now,
		}
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(timeout))

	if cfg.SendData != "" {
		if _, err := io.WriteString(conn, cfg.SendData); err != nil {
			return model.ProbeResult{
				Status:    model.HeartbeatStatusDown,
				LatencyMs: int(time.Since(start).Milliseconds()),
				Message:   fmt.Sprintf("send data failed: %v", err),
				CheckedAt: time.Now().UTC(),
			}
		}
	}

	if cfg.ExpectData != "" {
		var buf bytes.Buffer
		tmp := make([]byte, 4096)
		matched := false
		for buf.Len() < maxTCPReadBytes {
			n, readErr := conn.Read(tmp)
			if n > 0 {
				buf.Write(tmp[:n])
				if strings.Contains(buf.String(), cfg.ExpectData) {
					matched = true
					break
				}
			}
			if readErr != nil {
				if errors.Is(readErr, io.EOF) {
					break
				}
				return model.ProbeResult{
					Status:    model.HeartbeatStatusDown,
					LatencyMs: int(time.Since(start).Milliseconds()),
					Message:   fmt.Sprintf("read expected data failed: %v", readErr),
					CheckedAt: time.Now().UTC(),
				}
			}
		}
		if !matched {
			return model.ProbeResult{
				Status:    model.HeartbeatStatusDown,
				LatencyMs: int(time.Since(start).Milliseconds()),
				Message:   fmt.Sprintf("expected data %q not found", cfg.ExpectData),
				CheckedAt: time.Now().UTC(),
			}
		}
	}

	res := model.ProbeResult{
		Status:    model.HeartbeatStatusUp,
		LatencyMs: int(time.Since(start).Milliseconds()),
		Message:   "TCP connection succeeded",
		CheckedAt: time.Now().UTC(),
	}
	applyDegradedThreshold(&res, m.DegradedMs)
	return res
}
