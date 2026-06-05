// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package handler

import (
	"testing"
)

func TestContactPoint_resolveSettings_LegacyJSONString(t *testing.T) {
	p := contactPointProps{Settings: `{"integrationKey":"abc"}`}
	got, err := p.resolveSettings()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
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
	got, err := p.resolveSettings()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
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
	if _, err := p.resolveSettings(); err == nil {
		t.Fatal("expected error when both settings and settingsMap are set")
	}
}

func TestContactPoint_resolveSettings_NeitherSet_Errors(t *testing.T) {
	p := contactPointProps{}
	if _, err := p.resolveSettings(); err == nil {
		t.Fatal("expected error when neither settings nor settingsMap are set")
	}
}

func TestContactPoint_resolveSettings_InvalidJSON_Errors(t *testing.T) {
	p := contactPointProps{Settings: `{bad`}
	if _, err := p.resolveSettings(); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}
