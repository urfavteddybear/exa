package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"exa/internal/db"
	"exa/internal/model"
)

const (
	defaultNotifyTimeout = 10 * time.Second
	// Subscriber deliveries run off the probe goroutine; jobs are processed in order so DOWN never lands after UP.
	subscriberQueueSize   = 256
	subscriberConcurrency = 8
)

type subscriberJob struct {
	pageID   string
	subs     []model.Subscriber
	payload  notificationPayload
	emailCfg *model.EmailConfig
}

// Dispatcher routes monitor, incident, and maintenance notifications to channels and subscribers.
type Dispatcher struct {
	store            *db.Store
	client           *http.Client
	subscriberClient *http.Client
	telegramBaseURL  string
	subQueue         chan subscriberJob
	subPending       sync.WaitGroup
}

// NewDispatcher creates a Dispatcher backed by store.
func NewDispatcher(store *db.Store) *Dispatcher {
	d := &Dispatcher{
		store: store,
		client: &http.Client{
			Timeout: defaultNotifyTimeout,
		},
		subscriberClient: newSubscriberHTTPClient(),
		telegramBaseURL:  "https://api.telegram.org",
		subQueue:         make(chan subscriberJob, subscriberQueueSize),
	}
	go d.runSubscriberQueue()
	return d
}

func newSubscriberHTTPClient() *http.Client {
	return &http.Client{
		Timeout: defaultNotifyTimeout,
		Transport: &http.Transport{
			DialContext:       subscriberDialContext(nil),
			DisableKeepAlives: true,
		},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("redirects are not allowed for subscriber webhooks")
		},
	}
}

type notificationPayload struct {
	Event          string                `json:"event"`
	Title          string                `json:"title"`
	Message        string                `json:"message"`
	Severity       string                `json:"severity"`
	Timestamp      time.Time             `json:"timestamp"`
	Monitor        *monitorEventPayload  `json:"monitor,omitempty"`
	StatusPage     *pageEventPayload     `json:"status_page,omitempty"`
	Incident       *model.Incident       `json:"incident,omitempty"`
	IncidentUpdate *model.IncidentUpdate `json:"incident_update,omitempty"`
	Maintenance    *model.Maintenance    `json:"maintenance,omitempty"`
}

type monitorEventPayload struct {
	ID             string                `json:"id"`
	Name           string                `json:"name"`
	Slug           string                `json:"slug"`
	Type           model.MonitorType     `json:"type"`
	Target         string                `json:"target"`
	PreviousStatus model.ComponentStatus `json:"previous_status"`
	CurrentStatus  model.ComponentStatus `json:"current_status"`
	Error          string                `json:"error,omitempty"`
}

type pageEventPayload struct {
	ID    string `json:"id"`
	Slug  string `json:"slug"`
	Title string `json:"title"`
}

// NotifyMonitorStateChange notifies channels attached to m (or default channels) and public page subscribers.
func (d *Dispatcher) NotifyMonitorStateChange(ctx context.Context, m model.Monitor, prev, next model.ComponentStatus, errMsg string) {
	if d == nil || d.store == nil {
		return
	}

	channels, err := d.store.ListChannelsForMonitor(m.ID)
	if err != nil {
		slog.Error("list notification channels failed", "monitor_id", m.ID, "monitor", m.Name, "err", err)
	} else if len(channels) == 0 {
		slog.Warn("no enabled notification channels for monitor transition", "monitor_id", m.ID, "monitor", m.Name, "prev", prev, "next", next)
	} else {
		payload := notificationPayload{
			Event:     "monitor_state_change",
			Title:     adminMonitorTitle(m.Name, next),
			Message:   adminMonitorMessage(m, prev, next, errMsg),
			Severity:  severityForComponentStatus(next),
			Timestamp: time.Now().UTC(),
			Monitor: &monitorEventPayload{
				ID:             m.ID,
				Name:           m.Name,
				Slug:           m.Slug,
				Type:           m.Type,
				Target:         m.Target,
				PreviousStatus: prev,
				CurrentStatus:  model.ComponentStatus(adminStatusLabel(next)),
				Error:          errMsg,
			},
		}

		for _, ch := range channels {
			if !ch.Enabled {
				continue
			}
			if err := d.sendChannel(ctx, ch, payload); err != nil {
				slog.Error("monitor notification delivery failed",
					"monitor_id", m.ID,
					"monitor", m.Name,
					"channel_id", ch.ID,
					"channel", ch.Name,
					"type", ch.Type,
					"prev", prev,
					"next", next,
					"err", err,
				)
				continue
			}
			slog.Info("monitor notification delivered",
				"monitor_id", m.ID,
				"monitor", m.Name,
				"channel_id", ch.ID,
				"channel", ch.Name,
				"type", ch.Type,
				"prev", prev,
				"next", next,
			)
		}
	}

	d.notifyPublicMonitorSubscribers(ctx, m, prev, next)
}

// NotifyIncidentUpdate notifies enabled default channels and active page subscribers.
func (d *Dispatcher) NotifyIncidentUpdate(ctx context.Context, page model.StatusPage, inc model.Incident, upd model.IncidentUpdate) {
	if d == nil || d.store == nil {
		return
	}

	msg := fmt.Sprintf("Incident %q (%s impact) status is %s: %s", inc.Title, inc.Impact, upd.Status, upd.Message)
	payload := notificationPayload{
		Event:     "incident_update",
		Title:     fmt.Sprintf("[%s] %s: %s", upd.Status, page.Title, inc.Title),
		Message:   msg,
		Severity:  severityForIncident(inc.Impact, upd.Status),
		Timestamp: time.Now().UTC(),
		StatusPage: &pageEventPayload{
			ID:    page.ID,
			Slug:  page.Slug,
			Title: page.Title,
		},
		Incident:       &inc,
		IncidentUpdate: &upd,
	}

	d.dispatchPageNotifications(ctx, page.ID, payload)
}

// NotifyMaintenanceUpdate notifies enabled default channels and active page subscribers.
func (d *Dispatcher) NotifyMaintenanceUpdate(ctx context.Context, page model.StatusPage, maint model.Maintenance) {
	if d == nil || d.store == nil {
		return
	}

	msg := fmt.Sprintf(
		"Maintenance %q (%s to %s) is %s.",
		maint.Title,
		maint.ScheduledStart.UTC().Format(time.RFC3339),
		maint.ScheduledEnd.UTC().Format(time.RFC3339),
		maint.Status,
	)
	if maint.Description != "" {
		msg += " " + maint.Description
	}

	sev := "info"
	if maint.Status == model.MaintenanceStatusCompleted {
		sev = "resolved"
	}

	payload := notificationPayload{
		Event:     "maintenance_update",
		Title:     fmt.Sprintf("[maintenance:%s] %s: %s", maint.Status, page.Title, maint.Title),
		Message:   msg,
		Severity:  sev,
		Timestamp: time.Now().UTC(),
		StatusPage: &pageEventPayload{
			ID:    page.ID,
			Slug:  page.Slug,
			Title: page.Title,
		},
		Maintenance: &maint,
	}

	d.dispatchPageNotifications(ctx, page.ID, payload)
}

// TestChannel sends a test notification through the provided channel configuration.
// SendSubscriptionConfirmation emails a double opt-in link using the first enabled email channel.
func (d *Dispatcher) SendSubscriptionConfirmation(ctx context.Context, page model.StatusPage, email, confirmURL, unsubscribeURL string) error {
	channels, err := d.store.ListNotificationChannels()
	if err != nil {
		return err
	}
	cfg := firstEnabledEmailConfig(channels)
	if cfg == nil {
		return errors.New("no enabled email notification channel")
	}
	sub := *cfg
	sub.To = []string{email}
	return d.sendEmail(ctx, sub, notificationPayload{
		Event: "subscription_confirmation",
		Title: fmt.Sprintf("Confirm your subscription to %s", page.Title),
		Message: fmt.Sprintf("Someone (hopefully you) asked to receive status updates for %s at this address.\n\nConfirm: %s\n\nIf this wasn't you, ignore this email or unsubscribe: %s",
			page.Title, confirmURL, unsubscribeURL),
		Severity:  "info",
		Timestamp: time.Now().UTC(),
	})
}

func (d *Dispatcher) TestChannel(ctx context.Context, ch model.NotificationChannel) error {
	payload := notificationPayload{
		Event:     "test",
		Title:     "Exa Test Notification",
		Message:   fmt.Sprintf("Test notification for channel %q (%s).", ch.Name, ch.Type),
		Severity:  "info",
		Timestamp: time.Now().UTC(),
	}
	return d.sendChannel(ctx, ch, payload)
}

func (d *Dispatcher) dispatchPageNotifications(ctx context.Context, pageID string, payload notificationPayload) {
	channels, err := d.store.ListNotificationChannels()
	var emailCfg *model.EmailConfig
	if err == nil {
		emailCfg = firstEnabledEmailConfig(channels)
		for _, ch := range channels {
			if !ch.Enabled || !ch.IsDefault {
				continue
			}
			if err := d.sendChannel(ctx, ch, payload); err != nil {
				slog.Error("page notification delivery failed",
					"page_id", pageID,
					"event", payload.Event,
					"channel_id", ch.ID,
					"channel", ch.Name,
					"type", ch.Type,
					"err", err,
				)
			}
		}
	}

	d.deliverPageSubscribers(pageID, payload, emailCfg)
}

func (d *Dispatcher) notifyPublicMonitorSubscribers(ctx context.Context, m model.Monitor, prev, next model.ComponentStatus) {
	allowSubs, err := d.store.GetSetting("allow_subscriptions")
	if err != nil || !strings.EqualFold(strings.TrimSpace(allowSubs), "true") {
		return
	}

	isRecovery := next == model.ComponentStatusOperational &&
		(prev == model.ComponentStatusDegraded || prev == model.ComponentStatusMajorOutage)
	if next != model.ComponentStatusDegraded && next != model.ComponentStatusMajorOutage && !isRecovery {
		return
	}

	bindings, err := d.store.ListPublicPagesForMonitor(m.ID)
	if err != nil || len(bindings) == 0 {
		return
	}

	var emailCfg *model.EmailConfig
	emailLoaded := false

	for _, binding := range bindings {
		page := binding.Page
		subs, err := d.store.ListActiveSubscribers(page.ID)
		if err != nil {
			slog.Error("list active subscribers failed", "page_id", page.ID, "err", err)
			continue
		}
		if len(subs) == 0 {
			continue
		}

		if !emailLoaded {
			for _, sub := range subs {
				if sub.Type == model.SubscriberTypeEmail {
					if channels, chErr := d.store.ListNotificationChannels(); chErr == nil {
						emailCfg = firstEnabledEmailConfig(channels)
					}
					emailLoaded = true
					break
				}
			}
		}

		compName := strings.TrimSpace(binding.DisplayName)
		if compName == "" {
			compName = m.Name
		}

		payload := notificationPayload{
			Event:     "component_status_change",
			Title:     fmt.Sprintf("[%s] %s: %s", statusTag(next), page.Title, compName),
			Message:   fmt.Sprintf("Component %s status changed from %s to %s.", compName, prev, next),
			Severity:  severityForComponentStatus(next),
			Timestamp: time.Now().UTC(),
			StatusPage: &pageEventPayload{
				ID:    page.ID,
				Slug:  page.Slug,
				Title: page.Title,
			},
		}

		d.deliverSubscribersList(page.ID, subs, payload, emailCfg)
	}
}

func (d *Dispatcher) deliverPageSubscribers(pageID string, payload notificationPayload, emailCfg *model.EmailConfig) {
	subs, err := d.store.ListActiveSubscribers(pageID)
	if err != nil {
		slog.Error("list active subscribers failed", "page_id", pageID, "err", err)
		return
	}
	d.deliverSubscribersList(pageID, subs, payload, emailCfg)
}

// deliverSubscribersList queues delivery so slow or hostile subscriber endpoints never block the caller.
func (d *Dispatcher) deliverSubscribersList(pageID string, subs []model.Subscriber, payload notificationPayload, emailCfg *model.EmailConfig) {
	if len(subs) == 0 {
		return
	}
	d.subPending.Add(1)
	select {
	case d.subQueue <- subscriberJob{pageID: pageID, subs: subs, payload: payload, emailCfg: emailCfg}:
	default:
		d.subPending.Done()
		slog.Error("subscriber notification queue full, dropping event", "page_id", pageID, "event", payload.Event, "subscribers", len(subs))
	}
}

func (d *Dispatcher) runSubscriberQueue() {
	for job := range d.subQueue {
		d.deliverSubscriberJob(job)
		d.subPending.Done()
	}
}

func (d *Dispatcher) deliverSubscriberJob(job subscriberJob) {
	sem := make(chan struct{}, subscriberConcurrency)
	var wg sync.WaitGroup
	for _, sub := range job.subs {
		wg.Add(1)
		sem <- struct{}{}
		go func(sub model.Subscriber) {
			defer wg.Done()
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(context.Background(), defaultNotifyTimeout)
			defer cancel()
			switch sub.Type {
			case model.SubscriberTypeWebhook:
				if err := d.sendSubscriberWebhook(ctx, sub.Endpoint, job.payload); err != nil {
					slog.Warn("subscriber webhook delivery failed", "page_id", job.pageID, "subscriber_id", sub.ID, "err", err)
				}
			case model.SubscriberTypeEmail:
				if job.emailCfg != nil {
					subEmail := *job.emailCfg
					subEmail.To = []string{sub.Endpoint}
					payload := job.payload
					if base, _ := d.store.GetSetting("base_url"); strings.TrimSpace(base) != "" && sub.Token != "" {
						payload.Message += "\n\nUnsubscribe: " + strings.TrimRight(strings.TrimSpace(base), "/") + "/api/v1/unsubscribe/" + sub.Token
					}
					if err := d.sendEmail(ctx, subEmail, payload); err != nil {
						slog.Warn("subscriber email delivery failed", "page_id", job.pageID, "subscriber_id", sub.ID, "err", err)
					}
				}
			}
		}(sub)
	}
	wg.Wait()
}

func firstEnabledEmailConfig(channels []model.NotificationChannel) *model.EmailConfig {
	for _, ch := range channels {
		if !ch.Enabled || ch.Type != model.NotificationTypeEmail {
			continue
		}
		var parsed model.EmailConfig
		if err := json.Unmarshal(ch.Config, &parsed); err == nil && strings.TrimSpace(parsed.SMTPHost) != "" {
			return &parsed
		}
	}
	return nil
}

func (d *Dispatcher) sendChannel(ctx context.Context, ch model.NotificationChannel, payload notificationPayload) error {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultNotifyTimeout)
		defer cancel()
	}

	switch ch.Type {
	case model.NotificationTypeWebhook:
		var cfg model.WebhookConfig
		if err := json.Unmarshal(ch.Config, &cfg); err != nil {
			return fmt.Errorf("invalid webhook config: %w", err)
		}
		return d.sendWebhook(ctx, cfg, payload)
	case model.NotificationTypeDiscord:
		var cfg model.DiscordConfig
		if err := json.Unmarshal(ch.Config, &cfg); err != nil {
			return fmt.Errorf("invalid discord config: %w", err)
		}
		return d.sendDiscord(ctx, cfg, payload)
	case model.NotificationTypeSlack:
		var cfg model.SlackConfig
		if err := json.Unmarshal(ch.Config, &cfg); err != nil {
			return fmt.Errorf("invalid slack config: %w", err)
		}
		return d.sendSlack(ctx, cfg, payload)
	case model.NotificationTypeTelegram:
		var cfg model.TelegramConfig
		if err := json.Unmarshal(ch.Config, &cfg); err != nil {
			return fmt.Errorf("invalid telegram config: %w", err)
		}
		return d.sendTelegram(ctx, cfg, payload)
	case model.NotificationTypeEmail:
		var cfg model.EmailConfig
		if err := json.Unmarshal(ch.Config, &cfg); err != nil {
			return fmt.Errorf("invalid email config: %w", err)
		}
		return d.sendEmail(ctx, cfg, payload)
	case model.NotificationTypeNtfy:
		var cfg model.NtfyConfig
		if err := json.Unmarshal(ch.Config, &cfg); err != nil {
			return fmt.Errorf("invalid ntfy config: %w", err)
		}
		return d.sendNtfy(ctx, cfg, payload)
	default:
		return fmt.Errorf("unsupported notification channel type: %s", ch.Type)
	}
}

func adminMonitorTitle(name string, next model.ComponentStatus) string {
	switch next {
	case model.ComponentStatusMajorOutage:
		return fmt.Sprintf("[DOWN] %s is DOWN", name)
	case model.ComponentStatusDegraded:
		return fmt.Sprintf("[DEGRADED] %s is DEGRADED", name)
	case model.ComponentStatusOperational:
		return fmt.Sprintf("[UP] %s is back UP", name)
	default:
		return fmt.Sprintf("[%s] %s is %s", statusTag(next), name, adminStatusLabel(next))
	}
}

func adminMonitorMessage(m model.Monitor, prev, next model.ComponentStatus, errMsg string) string {
	var msg string
	switch next {
	case model.ComponentStatusMajorOutage:
		msg = fmt.Sprintf("Monitor %s (%s) is DOWN (previous: %s).", m.Name, m.Target, prev)
	case model.ComponentStatusDegraded:
		msg = fmt.Sprintf("Monitor %s (%s) is DEGRADED (previous: %s).", m.Name, m.Target, prev)
	case model.ComponentStatusOperational:
		msg = fmt.Sprintf("Monitor %s (%s) is back UP (previous: %s).", m.Name, m.Target, prev)
	default:
		msg = fmt.Sprintf("Monitor %s (%s) changed from %s to %s.", m.Name, m.Target, prev, next)
	}
	if errMsg != "" {
		msg += " Details: " + errMsg
	}
	return msg
}

func adminStatusLabel(st model.ComponentStatus) string {
	switch st {
	case model.ComponentStatusMajorOutage:
		return "down"
	case model.ComponentStatusDegraded:
		return "degraded"
	case model.ComponentStatusOperational:
		return "up"
	case model.ComponentStatusMaintenance:
		return "maintenance"
	default:
		return string(st)
	}
}

func statusTag(st model.ComponentStatus) string {
	switch st {
	case model.ComponentStatusOperational:
		return "UP"
	case model.ComponentStatusDegraded:
		return "DEGRADED"
	case model.ComponentStatusMajorOutage:
		return "DOWN"
	case model.ComponentStatusMaintenance:
		return "MAINT"
	default:
		return "INFO"
	}
}

func severityForComponentStatus(st model.ComponentStatus) string {
	switch st {
	case model.ComponentStatusOperational:
		return "resolved"
	case model.ComponentStatusDegraded:
		return "warning"
	case model.ComponentStatusMajorOutage:
		return "critical"
	default:
		return "info"
	}
}

func severityForIncident(impact model.IncidentImpact, status model.IncidentStatus) string {
	if status == model.IncidentStatusResolved {
		return "resolved"
	}
	switch impact {
	case model.IncidentImpactMajor, model.IncidentImpactCritical:
		return "critical"
	case model.IncidentImpactMinor:
		return "warning"
	default:
		return "info"
	}
}
