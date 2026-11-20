package probe

import (
	"context"
	"fmt"
	"time"

	"exa/internal/model"
)

func probePush(_ context.Context, m model.Monitor) model.ProbeResult {
	cfg, err := parseConfig[model.PushConfig](m.Config)
	if err != nil {
		return model.ProbeResult{
			Status:    model.HeartbeatStatusDown,
			Message:   fmt.Sprintf("invalid push config: %v", err),
			CheckedAt: time.Now().UTC(),
		}
	}
	cfg = cfg.WithDefaults()
	return model.ProbeResult{
		Status:    model.HeartbeatStatusUp,
		Message:   fmt.Sprintf("push monitor ready (grace period %ds)", cfg.GraceSec),
		CheckedAt: time.Now().UTC(),
	}
}
