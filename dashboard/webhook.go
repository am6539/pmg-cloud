package dashboard

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// WebhookPayload is the JSON body sent to each webhook endpoint.
type WebhookPayload struct {
	Event      string    `json:"event"`
	Timestamp  time.Time `json:"timestamp"`
	GroupID    string    `json:"group_id,omitempty"`
	Package    string    `json:"package,omitempty"`
	Ecosystem  string    `json:"ecosystem,omitempty"`
	Action     string    `json:"action,omitempty"`
	EndpointID string    `json:"endpoint_id,omitempty"`
}

// WebhookDelivery dispatches webhook payloads asynchronously.
type WebhookDelivery struct {
	cfg    *ConfigStore
	client *http.Client
	queue  chan WebhookPayload
}

// NewWebhookDelivery creates a WebhookDelivery and starts the background worker.
func NewWebhookDelivery(cfg *ConfigStore) *WebhookDelivery {
	wd := &WebhookDelivery{
		cfg:    cfg,
		client: &http.Client{Timeout: 5 * time.Second},
		queue:  make(chan WebhookPayload, 200),
	}
	go wd.worker()
	return wd
}

// Send enqueues a payload for delivery. Non-blocking; drops if queue is full.
func (wd *WebhookDelivery) Send(p WebhookPayload) {
	select {
	case wd.queue <- p:
	default:
		slog.Warn("webhook queue full, dropping payload", "event", p.Event)
	}
}

func (wd *WebhookDelivery) worker() {
	for p := range wd.queue {
		wd.deliver(p)
	}
}

func (wd *WebhookDelivery) deliver(p WebhookPayload) {
	hooks := wd.cfg.Get().Webhooks
	for _, h := range hooks {
		if !h.Enabled {
			continue
		}
		if p.Event == "malware_detected" && !h.OnMalware {
			continue
		}
		if p.Event == "package_blocked" && !h.OnBlocked {
			continue
		}
		wd.post(h.URL, p)
	}
}

func (wd *WebhookDelivery) post(url string, p WebhookPayload) {
	body, err := json.Marshal(p)
	if err != nil {
		slog.Warn("webhook marshal error", "err", err)
		return
	}
	resp, err := wd.client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		slog.Warn("webhook delivery error", "url", url, "err", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		slog.Warn("webhook non-2xx response", "url", url, "status", resp.StatusCode)
	}
}
