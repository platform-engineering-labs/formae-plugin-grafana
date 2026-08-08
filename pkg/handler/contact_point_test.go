// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	goapi "github.com/grafana/grafana-openapi-client-go/client"
	"github.com/platform-engineering-labs/formae-plugin-grafana/pkg/config"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// stubGrafana answers the contact-point provisioning endpoints with canned
// bodies, so a test can pin what the plugin reports for a given server view.
type stubGrafana struct {
	postBody   string
	getBody    string
	getStatus  int
	postStatus int
}

func newStubGrafana(t *testing.T, stub stubGrafana) *goapi.GrafanaHTTPAPI {
	t.Helper()
	if stub.postStatus == 0 {
		stub.postStatus = http.StatusAccepted
	}
	if stub.getStatus == 0 {
		stub.getStatus = http.StatusOK
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The notifier-metadata lookup that guards a write reads the target's
		// version and its notifier vocabulary. Reporting a version but no
		// vocabulary sends settings validation to the baked one, which is what
		// these tests validate against.
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/health") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"database":"ok","version":"12.0.0"}`))
			return
		}
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/alert-notifiers") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPost:
			w.WriteHeader(stub.postStatus)
			_, _ = w.Write([]byte(stub.postBody))
		case http.MethodGet:
			w.WriteHeader(stub.getStatus)
			_, _ = w.Write([]byte(stub.getBody))
		default:
			w.WriteHeader(http.StatusNotImplemented)
		}
	}))
	t.Cleanup(server.Close)

	return clientForStub(t, server.URL)
}

// clientForStub builds a Grafana client pointed at a stub server, with the
// credential the stub expects to see.
func clientForStub(t *testing.T, url string) *goapi.GrafanaHTTPAPI {
	t.Helper()
	client, err := config.NewClient(&config.TargetConfig{
		Type: "Grafana",
		URL:  url,
		Auth: json.RawMessage(`{"Type":"Token","Token":"stub-token"}`),
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client
}

// reportedSettings decodes the settings object the handler reported.
func reportedSettings(t *testing.T, props json.RawMessage) map[string]any {
	t.Helper()
	var out struct {
		Settings map[string]any `json:"settings"`
	}
	if err := json.Unmarshal(props, &out); err != nil {
		t.Fatalf("reported properties not valid JSON: %v", err)
	}
	return out.Settings
}

// Grafana returns every option it classifies as secret as the literal
// "[REDACTED]" on its read paths. Those keys must not be reported: a stripped
// key keeps the author's declared value through the property merge, while every
// key the plugin can actually observe stays reported and drift-checked.
func TestStripRedacted_RemovesSentinelValuesRecursively(t *testing.T) {
	in := map[string]any{
		"url":   "https://hooks.example.com/services/T000",
		"token": "[REDACTED]",
		"httpConfig": map[string]any{
			"oauth2": map[string]any{
				"clientId":     "formae",
				"clientSecret": "[REDACTED]",
			},
		},
		"hmacConfig": map[string]any{
			"secret": "[REDACTED]",
		},
		"responders": []any{
			map[string]any{
				"type":     "team",
				"apiKey":   "[REDACTED]",
				"username": "oncall",
			},
		},
	}

	got, ok := stripRedacted(in).(map[string]any)
	if !ok {
		t.Fatalf("stripRedacted returned %T, want map[string]any", stripRedacted(in))
	}

	if _, present := got["token"]; present {
		t.Errorf("top-level redacted key reported: %v", got)
	}
	if got["url"] != "https://hooks.example.com/services/T000" {
		t.Errorf("non-secret sibling not preserved: %v", got["url"])
	}

	httpConfig, ok := got["httpConfig"].(map[string]any)
	if !ok {
		t.Fatalf("httpConfig = %#v, want map[string]any", got["httpConfig"])
	}
	oauth2, ok := httpConfig["oauth2"].(map[string]any)
	if !ok {
		t.Fatalf("httpConfig.oauth2 = %#v, want map[string]any", httpConfig["oauth2"])
	}
	if _, present := oauth2["clientSecret"]; present {
		t.Errorf("redacted key two levels deep reported: %v", oauth2)
	}
	if oauth2["clientId"] != "formae" {
		t.Errorf("nested non-secret sibling not preserved: %v", oauth2)
	}

	// hmacConfig holds nothing but a secret, so stripping empties it and the
	// object itself is dropped rather than reported as an empty object no
	// declaration can match.
	if _, present := got["hmacConfig"]; present {
		t.Errorf("object emptied by stripping still reported: %v", got["hmacConfig"])
	}

	responders, ok := got["responders"].([]any)
	if !ok || len(responders) != 1 {
		t.Fatalf("responders = %#v, want a one-element array", got["responders"])
	}
	responder, ok := responders[0].(map[string]any)
	if !ok {
		t.Fatalf("responders[0] = %#v, want map[string]any", responders[0])
	}
	if _, present := responder["apiKey"]; present {
		t.Errorf("redacted key inside an array element reported: %v", responder)
	}
	if responder["type"] != "team" || responder["username"] != "oncall" {
		t.Errorf("array element non-secret keys not preserved: %v", responder)
	}

	// The input must not be mutated - callers keep using the server's view.
	if in["token"] != "[REDACTED]" {
		t.Errorf("stripRedacted mutated its input: %v", in)
	}
}

// Only a value that is exactly the sentinel is a redacted secret. A value that
// merely mentions it is an observable value and stays reported.
func TestStripRedacted_KeepsValuesThatOnlyContainTheSentinel(t *testing.T) {
	in := map[string]any{
		"message":   "alert body: [REDACTED] by policy",
		"title":     "[REDACTED]-ish",
		"maxAlerts": float64(10),
	}

	got, ok := stripRedacted(in).(map[string]any)
	if !ok {
		t.Fatalf("stripRedacted returned %T, want map[string]any", stripRedacted(in))
	}
	if got["message"] != "alert body: [REDACTED] by policy" {
		t.Errorf("substring match stripped: %v", got)
	}
	if got["title"] != "[REDACTED]-ish" {
		t.Errorf("prefix match stripped: %v", got)
	}
	if got["maxAlerts"] != float64(10) {
		t.Errorf("non-string value dropped: %v", got)
	}
}

// Create must report what a later Read will see. Grafana's POST reply flattens
// a nested redacted secret into a dotted sibling key ("hmacConfig.secret"
// beside "hmacConfig") - a shape no GET ever returns, so reporting it would
// leave recorded state permanently diverged.
func TestContactPointCreate_ReportsTheReadView(t *testing.T) {
	client := newStubGrafana(t, stubGrafana{
		postBody: `{"uid":"cp-1","name":"webhook-cp","type":"webhook","disableResolveMessage":false,
			"settings":{"url":"https://example.com/hook","hmacConfig":{},"hmacConfig.secret":"[REDACTED]"}}`,
		getBody: `[{"uid":"cp-1","name":"webhook-cp","type":"webhook","disableResolveMessage":false,
			"settings":{"url":"https://example.com/hook","hmacConfig":{"header":"X-Sig","secret":"[REDACTED]"}}}]`,
	})

	h := &ContactPointHandler{}
	props := json.RawMessage(`{"name":"webhook-cp","contactPointType":"webhook",
		"settings":{"url":"https://example.com/hook","hmacConfig":{"header":"X-Sig","secret":"s3cret"}}}`)

	result, err := h.Create(context.Background(), client, props)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.OperationStatus != resource.OperationStatusSuccess {
		t.Fatalf("status = %v (%s), want success", result.OperationStatus, result.StatusMessage)
	}
	if result.NativeID != "cp-1" {
		t.Errorf("NativeID = %q, want cp-1", result.NativeID)
	}

	settings := reportedSettings(t, result.ResourceProperties)
	if _, present := settings["hmacConfig.secret"]; present {
		t.Errorf("dotted key from the write reply reported: %v", settings)
	}
	if settings["url"] != "https://example.com/hook" {
		t.Errorf("url = %v, want the read view's url", settings["url"])
	}
	hmacConfig, ok := settings["hmacConfig"].(map[string]any)
	if !ok {
		t.Fatalf("hmacConfig = %#v, want the read view's nested object", settings["hmacConfig"])
	}
	if hmacConfig["header"] != "X-Sig" {
		t.Errorf("nested non-secret key = %v, want X-Sig", hmacConfig["header"])
	}
	if _, present := hmacConfig["secret"]; present {
		t.Errorf("nested redacted secret reported: %v", hmacConfig)
	}
}

// The contact point exists by the time the read-back runs, so a failing read
// must never turn a successful create into a failure - that would orphan the
// contact point and invite a colliding retry. The write response, passed
// through the same stripping, is reported instead.
func TestContactPointCreate_FallsBackToStrippedWriteResponseWhenReadFails(t *testing.T) {
	client := newStubGrafana(t, stubGrafana{
		postBody: `{"uid":"cp-2","name":"webhook-cp","type":"webhook","disableResolveMessage":false,
			"settings":{"url":"https://example.com/hook","hmacConfig":{},"hmacConfig.secret":"[REDACTED]"}}`,
		getStatus: http.StatusInternalServerError,
		getBody:   `{"message":"boom"}`,
	})

	h := &ContactPointHandler{}
	props := json.RawMessage(`{"name":"webhook-cp","contactPointType":"webhook",
		"settings":{"url":"https://example.com/hook","hmacConfig":{"secret":"s3cret"}}}`)

	result, err := h.Create(context.Background(), client, props)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.OperationStatus != resource.OperationStatusSuccess {
		t.Fatalf("status = %v (%s), want success despite the failed read-back", result.OperationStatus, result.StatusMessage)
	}
	if result.NativeID != "cp-2" {
		t.Errorf("NativeID = %q, want cp-2", result.NativeID)
	}

	settings := reportedSettings(t, result.ResourceProperties)
	if settings["url"] != "https://example.com/hook" {
		t.Errorf("url = %v, want the write response's url", settings["url"])
	}
	if _, present := settings["hmacConfig.secret"]; present {
		t.Errorf("dotted redacted key reported: %v", settings)
	}
	if _, present := settings["hmacConfig"]; present {
		t.Errorf("object emptied by stripping reported: %v", settings["hmacConfig"])
	}
}

// Read reports the server's view minus every redacted key, so a contact point
// whose non-secret settings are unchanged reconciles as a no-op.
func TestContactPointRead_StripsRedactedSettings(t *testing.T) {
	client := newStubGrafana(t, stubGrafana{
		getBody: `[{"uid":"cp-3","name":"slack-cp","type":"slack","disableResolveMessage":true,
			"settings":{"recipient":"#alerts","token":"[REDACTED]"}}]`,
	})

	h := &ContactPointHandler{}
	result, err := h.Read(context.Background(), client, "cp-3")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ErrorCode != "" {
		t.Fatalf("ErrorCode = %q, want none", result.ErrorCode)
	}

	settings := reportedSettings(t, json.RawMessage(result.Properties))
	if settings["recipient"] != "#alerts" {
		t.Errorf("recipient = %v, want #alerts", settings["recipient"])
	}
	if _, present := settings["token"]; present {
		t.Errorf("redacted token reported: %v", settings)
	}
}

// A contact point whose every option is secret strips down to an empty
// settings object. It must then be left out of the reported properties
// altogether: reporting an empty object would match nothing the author
// declared and drift on every reconcile.
func TestContactPointRead_OmitsSettingsEntirelyWhenEveryOptionIsSecret(t *testing.T) {
	client := newStubGrafana(t, stubGrafana{
		getBody: `[{"uid":"cp-1","name":"oncall","type":"slack","settings":{"url":"[REDACTED]"}}]`,
	})

	result, err := (&ContactPointHandler{}).Read(context.Background(), client, "cp-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ErrorCode != "" {
		t.Fatalf("unexpected error code: %v", result.ErrorCode)
	}

	var reported map[string]any
	if err := json.Unmarshal([]byte(result.Properties), &reported); err != nil {
		t.Fatalf("reported properties are not an object: %v", err)
	}
	if _, ok := reported["settings"]; ok {
		t.Errorf("settings reported as %v, want the key absent", reported["settings"])
	}
	if reported["name"] != "oncall" {
		t.Errorf("name = %v, want oncall", reported["name"])
	}
}
