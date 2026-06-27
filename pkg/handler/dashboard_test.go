// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package handler

import (
	"encoding/json"
	"testing"
)

// dashboardModelToProps must strip Grafana's top-level server-assigned counters
// (id/version/iteration) and the keys promoted to dedicated resource props
// (uid/title) from configJson, so a dashboard whose content is unchanged
// reconciles as a true 0-change no-op. The strip is top-level only — it must
// never recurse into panels[], whose elements carry their own user-authored id.
func TestDashboardModelToProps_StripsServerKeysPreservesContent(t *testing.T) {
	model := map[string]interface{}{
		"id":            float64(42),
		"uid":           "abc-123",
		"title":         "My Dashboard",
		"version":       float64(7),
		"iteration":     float64(1699999999),
		"schemaVersion": float64(39),
		"panels": []interface{}{
			map[string]interface{}{
				"id":    float64(1),
				"title": "Test Panel",
				"type":  "text",
			},
		},
	}

	out := dashboardModelToProps(model, nil, "fallback-uid")

	// uid/title are surfaced as dedicated dashboardProps fields.
	if out.UID != "abc-123" {
		t.Errorf("UID = %q, want abc-123", out.UID)
	}
	if out.Title != "My Dashboard" {
		t.Errorf("Title = %q, want My Dashboard", out.Title)
	}

	var cfg map[string]interface{}
	if err := json.Unmarshal([]byte(out.ConfigJSON), &cfg); err != nil {
		t.Fatalf("configJson not valid JSON: %v", err)
	}

	// None of the five top-level omitted keys survive in configJson.
	for _, k := range []string{"id", "uid", "title", "version", "iteration"} {
		if _, present := cfg[k]; present {
			t.Errorf("configJson still carries top-level %q: %v", k, cfg)
		}
	}

	// User content is preserved untouched.
	if cfg["schemaVersion"] != float64(39) {
		t.Errorf("schemaVersion = %v, want 39", cfg["schemaVersion"])
	}
	panels, ok := cfg["panels"].([]interface{})
	if !ok || len(panels) != 1 {
		t.Fatalf("panels not preserved: %v", cfg["panels"])
	}
	panel, ok := panels[0].(map[string]interface{})
	if !ok {
		t.Fatalf("panel not a map: %v", panels[0])
	}
	// The panel's own nested id must NOT be stripped.
	if panel["id"] != float64(1) {
		t.Errorf("nested panel id stripped or changed: %v", panel["id"])
	}
	if panel["title"] != "Test Panel" {
		t.Errorf("panel title = %v, want Test Panel", panel["title"])
	}
}

// When the model carries no uid, dashboardModelToProps falls back to the
// nativeID passed by Read.
func TestDashboardModelToProps_FallsBackToNativeID(t *testing.T) {
	model := map[string]interface{}{
		"title":         "No UID Dashboard",
		"schemaVersion": float64(39),
	}

	out := dashboardModelToProps(model, nil, "native-fallback")

	if out.UID != "native-fallback" {
		t.Errorf("UID = %q, want native-fallback", out.UID)
	}
	if out.Title != "No UID Dashboard" {
		t.Errorf("Title = %q, want No UID Dashboard", out.Title)
	}
}
