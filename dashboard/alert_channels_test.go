package dashboard

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSlackSender_PostsText(t *testing.T) {
	var gotBody string
	srv := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ch := AlertChannel{Kind: "slack", WebhookURL: srv.URL}
	require.NoError(t, sendAlert(ch, "PMG malware blocked: evil@1.0.0"))
	assert.Contains(t, gotBody, "evil@1.0.0")
}

func TestSlackSender_Non2xxIsError(t *testing.T) {
	srv := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	require.Error(t, sendAlert(AlertChannel{Kind: "slack", WebhookURL: srv.URL}, "x"))
}

func TestTelegramSender_BuildsAPIURL(t *testing.T) {
	var gotPath, gotQuery string
	srv := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ch := AlertChannel{Kind: "telegram", Token: "TKN", ChatID: "123"}
	require.NoError(t, sendTelegramTo(srv.URL, ch, "hello world"))
	assert.True(t, strings.Contains(gotPath, "/botTKN/sendMessage"))
	assert.Contains(t, gotQuery, "chat_id=123")
	assert.Contains(t, gotQuery, "hello")
}

func TestSendAlert_UnknownKind(t *testing.T) {
	require.Error(t, sendAlert(AlertChannel{Kind: "carrier-pigeon"}, "x"))
}
