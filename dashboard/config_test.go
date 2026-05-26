package dashboard

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateWebhookURL_ValidURLs(t *testing.T) {
	valid := []string{
		"https://hooks.example.com/webhook",
		"http://webhook.example.com/path?token=abc",
		"https://1.2.3.4/hook",       // public IP literal
		"https://example.com:8443/x", // non-standard port
	}
	for _, u := range valid {
		t.Run(u, func(t *testing.T) {
			require.NoError(t, validateWebhookURL(u))
		})
	}
}

func TestValidateWebhookURL_RejectsNonHTTP(t *testing.T) {
	cases := []string{
		"ftp://example.com/hook",
		"file:///etc/passwd",
		"not-a-url",
	}
	for _, u := range cases {
		t.Run(u, func(t *testing.T) {
			assert.Error(t, validateWebhookURL(u))
		})
	}
}

func TestValidateWebhookURL_RejectsPrivateIPs(t *testing.T) {
	cases := []string{
		"http://127.0.0.1/hook",
		"http://127.1.2.3/hook",
		"http://10.0.0.1/hook",
		"http://10.255.255.255/hook",
		"http://192.168.1.1/hook",
		"http://172.16.0.1/hook",
		"http://169.254.169.254/hook", // AWS metadata endpoint
		"http://[::1]/hook",           // IPv6 loopback
	}
	for _, u := range cases {
		t.Run(u, func(t *testing.T) {
			assert.Error(t, validateWebhookURL(u))
		})
	}
}

func TestValidateWebhookURL_RejectsLocalhost(t *testing.T) {
	cases := []string{
		"http://localhost/hook",
		"https://localhost:9000/hook",
		"http://LOCALHOST/hook",
	}
	for _, u := range cases {
		t.Run(u, func(t *testing.T) {
			assert.Error(t, validateWebhookURL(u))
		})
	}
}

func TestConfigStore_AddWebhook_RejectsSSRF(t *testing.T) {
	cs, err := NewConfigStore(t.TempDir())
	require.NoError(t, err)

	_, err = cs.AddWebhook(WebhookEntry{
		Name:    "bad",
		URL:     "http://169.254.169.254/latest/meta-data",
		Enabled: true,
	})
	assert.Error(t, err)
}

func TestConfigStore_UpdateWebhook_RejectsSSRF(t *testing.T) {
	cs, err := NewConfigStore(t.TempDir())
	require.NoError(t, err)

	// Add a valid webhook first
	created, err := cs.AddWebhook(WebhookEntry{
		Name:    "valid",
		URL:     "https://hooks.example.com/wh",
		Enabled: true,
	})
	require.NoError(t, err)

	// Try to update it with a private address
	created.URL = "http://10.0.0.1/hook"
	err = cs.UpdateWebhook(created)
	assert.Error(t, err)
}
