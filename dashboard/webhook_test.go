package dashboard

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestAlertMessage_MalwareWithVersionAndGroup(t *testing.T) {
	p := WebhookPayload{
		Event:          "malware_detected",
		Timestamp:      time.Date(2026, 9, 10, 14, 32, 7, 0, time.UTC),
		GroupName:      "vega",
		Package:        "evil-pkg",
		PackageVersion: "6.6.6",
		Ecosystem:      "npm",
		Action:         "BLOCKED",
		Hostname:       "HT-PC",
		IsMalware:      true,
	}
	msg := alertMessage(p)

	assert.Contains(t, msg, "🛡️ PMG Alert: Malware Detected")
	assert.Contains(t, msg, "Package: evil-pkg@6.6.6 (npm)")
	assert.Contains(t, msg, "Host: HT-PC")
	assert.Contains(t, msg, "Group: vega")
	assert.Contains(t, msg, "Action: BLOCKED")
	assert.Contains(t, msg, "Time: 2026-09-10 14:32:07 UTC")
}

func TestAlertMessage_BlockedWithoutGroup_OmitsGroupLine(t *testing.T) {
	p := WebhookPayload{
		Event:     "package_blocked",
		Package:   "handlebars",
		Ecosystem: "npm",
		Action:    "COOLDOWN_BLOCKED",
		Hostname:  "HT-PC",
		IsMalware: false,
	}
	msg := alertMessage(p)

	assert.Contains(t, msg, "🚫 PMG Alert: Package Blocked")
	assert.Contains(t, msg, "Package: handlebars (npm)")
	assert.NotContains(t, msg, "Group:")
}

func TestAlertMessage_MissingHostname_FallsBackToEndpointID(t *testing.T) {
	p := WebhookPayload{
		Package:    "some-pkg",
		Ecosystem:  "pypi",
		Action:     "BLOCKED",
		EndpointID: "endpoint-123",
	}
	msg := alertMessage(p)

	assert.Contains(t, msg, "Host: endpoint-123")
}

func TestAlertMessage_NoVersion_PackageNameOnly(t *testing.T) {
	p := WebhookPayload{
		Package:   "leftpad",
		Ecosystem: "npm",
		Action:    "BLOCKED",
		Hostname:  "HT-PC",
	}
	msg := alertMessage(p)

	assert.Contains(t, msg, "Package: leftpad (npm)")
}
