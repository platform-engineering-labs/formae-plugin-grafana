// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package handler

import (
	"encoding/json"
	"testing"
)

func TestContactPoint_resolveSettings_LegacyJSONString(t *testing.T) {
	p := contactPointProps{Settings: `{"integrationKey":"abc"}`}
	got, shape, err := p.resolveSettings()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if shape != settingsShapeString {
		t.Errorf("shape = %v, want settingsShapeString", shape)
	}
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("want map[string]any, got %T", got)
	}
	if m["integrationKey"] != "abc" {
		t.Errorf("integrationKey = %v, want abc", m["integrationKey"])
	}
}

func TestContactPoint_resolveSettings_StructuredMap(t *testing.T) {
	p := contactPointProps{SettingsMap: map[string]string{
		"integrationKey": "from-resolvable",
		"severity":       "critical",
	}}
	got, shape, err := p.resolveSettings()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if shape != settingsShapeMap {
		t.Errorf("shape = %v, want settingsShapeMap", shape)
	}
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("want map[string]any, got %T", got)
	}
	if m["integrationKey"] != "from-resolvable" || m["severity"] != "critical" {
		t.Errorf("missing fields: %v", m)
	}
}

func TestContactPoint_resolveSettings_BothSet_Errors(t *testing.T) {
	p := contactPointProps{
		Settings:    `{"k":"v"}`,
		SettingsMap: map[string]string{"k": "v"},
	}
	if _, _, err := p.resolveSettings(); err == nil {
		t.Fatal("expected error when both settings and settingsMap are set")
	}
}

func TestContactPoint_resolveSettings_NeitherSet_Errors(t *testing.T) {
	p := contactPointProps{}
	if _, _, err := p.resolveSettings(); err == nil {
		t.Fatal("expected error when neither settings nor settingsMap are set")
	}
}

func TestContactPoint_resolveSettings_InvalidJSON_Errors(t *testing.T) {
	p := contactPointProps{Settings: `{bad`}
	if _, _, err := p.resolveSettings(); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// buildResponseProps must echo the user's submitted shape so the conformance
// harness's submitted-vs-returned comparison matches. The previous version
// always returned `settings` even for `settingsMap` submissions, which broke
// Verify/Update/Extract/Sync in nightly CI.
func TestBuildResponseProps_RoundTripsMapShape(t *testing.T) {
	apiResponse := map[string]any{"integrationKey": "abc123"}
	out := buildResponseProps("uid-1", "name", "pagerduty", apiResponse, false, settingsShapeMap)
	if out.Settings != "" {
		t.Errorf("Settings should be empty for map shape; got %q", out.Settings)
	}
	if out.SettingsMap["integrationKey"] != "abc123" {
		t.Errorf("SettingsMap[integrationKey] = %q, want abc123", out.SettingsMap["integrationKey"])
	}
}

func TestBuildResponseProps_RoundTripsStringShape(t *testing.T) {
	apiResponse := map[string]any{"addresses": "ops@example.com"}
	out := buildResponseProps("uid-1", "name", "email", apiResponse, false, settingsShapeString)
	if len(out.SettingsMap) != 0 {
		t.Errorf("SettingsMap should be empty for string shape; got %v", out.SettingsMap)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(out.Settings), &decoded); err != nil {
		t.Fatalf("Settings not valid JSON: %v", err)
	}
	if decoded["addresses"] != "ops@example.com" {
		t.Errorf("addresses round-trip mismatch: %v", decoded)
	}
}

func TestCoerceToStringMap_NonStringValuesJSONEncoded(t *testing.T) {
	in := map[string]any{
		"plainStr":  "hello",
		"intField":  float64(42), // JSON numbers decode as float64
		"boolField": true,
	}
	got := coerceToStringMap(in)
	if got["plainStr"] != "hello" {
		t.Errorf("plainStr = %q", got["plainStr"])
	}
	if got["intField"] != "42" {
		t.Errorf("intField = %q, want 42", got["intField"])
	}
	if got["boolField"] != "true" {
		t.Errorf("boolField = %q, want true", got["boolField"])
	}
}
