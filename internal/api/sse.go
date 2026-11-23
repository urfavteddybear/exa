package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"exa/internal/model"
)

const (
	maxSSEClientsTotal = 500
	maxSSEClientsPerIP = 10
)

// SSEHub fans out real-time monitoring events to connected browser clients.
type SSEHub struct {
	mu      sync.RWMutex
	clients map[chan model.SSEEvent]string
	ipCount map[string]int
}

func NewSSEHub() *SSEHub {
	return &SSEHub{
		clients: make(map[chan model.SSEEvent]string),
		ipCount: make(map[string]int),
	}
}

func (h *SSEHub) Broadcast(ev model.SSEEvent) {
	if h == nil {
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.clients {
		select {
		case ch <- ev:
		default:
			// Drop event for slow clients rather than stalling probe workers.
		}
	}
}

func (h *SSEHub) subscribe(ip string) (chan model.SSEEvent, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.clients) >= maxSSEClientsTotal {
		return nil, false
	}
	if h.ipCount[ip] >= maxSSEClientsPerIP {
		return nil, false
	}
	ch := make(chan model.SSEEvent, 32)
	h.clients[ch] = ip
	h.ipCount[ip]++
	return ch, true
}

func (h *SSEHub) unsubscribe(ch chan model.SSEEvent) {
	h.mu.Lock()
	if ip, ok := h.clients[ch]; ok {
		delete(h.clients, ch)
		if h.ipCount[ip] <= 1 {
			delete(h.ipCount, ip)
		} else {
			h.ipCount[ip]--
		}
		close(ch)
	}
	h.mu.Unlock()
}

func (h *SSEHub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

func (rt *router) handleSSE(w http.ResponseWriter, r *http.Request) {
	_, authed := rt.cfg.Auth.AuthenticateRequest(r)
	var scopedPage *model.StatusPage
	if !authed {
		slug := r.URL.Query().Get("slug")
		page, err := rt.resolvePublicPage(r, slug)
		if err != nil {
			writeError(w, http.StatusNotFound, "status page not found")
			return
		}
		scopedPage = page
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	ip := clientIP(r, rt.cfg.TrustProxy)
	ch, allowed := rt.cfg.SSE.subscribe(ip)
	if !allowed {
		writeError(w, http.StatusTooManyRequests, "too many active stream connections")
		return
	}
	defer rt.cfg.SSE.unsubscribe(ch)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// Cache the page's monitor set per connection so each event doesn't cost one DB query per viewer;
	// it is refreshed on every keepalive to pick up newly attached monitors.
	var pageMonitors map[string]bool
	loadPageMonitors := func() {
		if scopedPage == nil {
			return
		}
		items, err := rt.cfg.Store.ListStatusPageMonitors(scopedPage.ID)
		if err != nil {
			return
		}
		set := make(map[string]bool, len(items))
		for _, it := range items {
			set[it.MonitorID] = true
		}
		pageMonitors = set
	}
	loadPageMonitors()

	_, _ = fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepalive.C:
			loadPageMonitors()
			_, _ = fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		case ev, open := <-ch:
			if !open {
				return
			}
			payload, ok := filterSSEPayload(ev, authed, scopedPage, pageMonitors)
			if !ok {
				continue
			}
			evType := ev.Type
			if evType == "" {
				evType = "message"
			}
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", evType, payload)
			flusher.Flush()
		}
	}
}

func filterSSEPayload(ev model.SSEEvent, authed bool, page *model.StatusPage, pageMonitors map[string]bool) ([]byte, bool) {
	raw, err := json.Marshal(ev.Data)
	if err != nil {
		return nil, false
	}
	if authed || page == nil {
		return raw, true
	}

	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, false
	}

	switch ev.Type {
	case "incident_update", "maintenance_update":
		if _, isDel := m["deleted_id"]; isDel {
			return raw, true
		}
		pageID, _ := m["status_page_id"].(string)
		if pageID != page.ID {
			return nil, false
		}
		return raw, true

	case "heartbeat", "status_change":
		monID, _ := m["monitor_id"].(string)
		if monID == "" {
			monID, _ = m["id"].(string)
		}
		if monID == "" {
			return nil, false
		}
		if !pageMonitors[monID] {
			return nil, false
		}
		if _, hasMsg := m["message"]; hasMsg {
			m["message"] = ""
		}
		delete(m, "last_error")
		delete(m, "push_token")
		if !page.ShowLatency {
			delete(m, "latency_ms")
		}
		sanitized, err := json.Marshal(m)
		if err != nil {
			return nil, false
		}
		return sanitized, true

	default:
		return nil, false
	}
}
