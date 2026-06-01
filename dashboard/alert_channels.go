package dashboard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

const telegramAPIBase = "https://api.telegram.org"

var alertHTTPClient = &http.Client{Timeout: 10 * time.Second}

// sendAlert dispatches text to a single channel based on its Kind.
func sendAlert(ch AlertChannel, text string) error {
	switch ch.Kind {
	case "slack":
		return sendSlack(ch.WebhookURL, text)
	case "telegram":
		return sendTelegramTo(telegramAPIBase, ch, text)
	default:
		return fmt.Errorf("unknown alert channel kind: %q", ch.Kind)
	}
}

func sendSlack(webhookURL, text string) error {
	body, _ := json.Marshal(map[string]string{"text": text})
	resp, err := alertHTTPClient.Post(webhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("slack post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("slack returned %d", resp.StatusCode)
	}
	return nil
}

// sendTelegramTo posts via the Telegram Bot API; apiBase is injectable for tests.
func sendTelegramTo(apiBase string, ch AlertChannel, text string) error {
	endpoint := fmt.Sprintf("%s/bot%s/sendMessage", apiBase, ch.Token)
	q := url.Values{}
	q.Set("chat_id", ch.ChatID)
	q.Set("text", text)
	resp, err := alertHTTPClient.Get(endpoint + "?" + q.Encode())
	if err != nil {
		return fmt.Errorf("telegram get: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("telegram returned %d", resp.StatusCode)
	}
	return nil
}
