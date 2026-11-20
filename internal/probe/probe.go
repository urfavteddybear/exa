package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"exa/internal/model"
)

const defaultTimeout = 10 * time.Second

// Probe executes a single health check for the given monitor and returns the result.
func Probe(ctx context.Context, m model.Monitor) model.ProbeResult {
	timeout := time.Duration(m.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	switch m.Type {
	case model.MonitorTypeHTTP:
		return probeHTTP(probeCtx, m, timeout)
	case model.MonitorTypeTCP:
		return probeTCP(probeCtx, m, timeout)
	case model.MonitorTypePing:
		return probePing(probeCtx, m, timeout)
	case model.MonitorTypeDNS:
		return probeDNS(probeCtx, m, timeout)
	case model.MonitorTypePush:
		return probePush(probeCtx, m)
	default:
		return model.ProbeResult{
			Status:    model.HeartbeatStatusDown,
			Message:   fmt.Sprintf("unsupported monitor type: %s", m.Type),
			CheckedAt: time.Now().UTC(),
		}
	}
}

func parseConfig[T any](raw json.RawMessage) (T, error) {
	var cfg T
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return cfg, nil
	}
	if err := json.Unmarshal([]byte(trimmed), &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func applyDegradedThreshold(res *model.ProbeResult, degradedMs int) {
	if res.Status == model.HeartbeatStatusUp && degradedMs > 0 && res.LatencyMs > degradedMs {
		res.Status = model.HeartbeatStatusDegraded
		res.Message = fmt.Sprintf("latency %dms exceeded degraded threshold %dms", res.LatencyMs, degradedMs)
	}
}
