// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package notifiers

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestBaked_ParsesEmbeddedSnapshot(t *testing.T) {
	ns := Baked()

	if len(ns) != 23 {
		t.Fatalf("got %d notifier types, want 23", len(ns))
	}
}

func TestParse_RejectsMalformedJSON(t *testing.T) {
	if _, err := Parse([]byte("not json")); err == nil {
		t.Fatal("expected an error for malformed JSON, got nil")
	}
}

func TestOptionPaths_Email(t *testing.T) {
	paths, ok := OptionPaths(Baked(), "email")
	if !ok {
		t.Fatal("expected email to be a known notifier type")
	}

	for _, want := range []string{"addresses", "singleEmail"} {
		if _, present := paths[want]; !present {
			t.Errorf("expected option path %q for email, got %v", want, paths)
		}
	}
}

func TestOptionPaths_WebhookThreeLevelNesting(t *testing.T) {
	paths, ok := OptionPaths(Baked(), "webhook")
	if !ok {
		t.Fatal("expected webhook to be a known notifier type")
	}

	want := "http_config.oauth2.tls_config.clientKey"
	if _, present := paths[want]; !present {
		t.Errorf("expected option path %q for webhook, got %v", want, paths)
	}
}

func TestOptionPaths_UnknownTypeReportsFalse(t *testing.T) {
	_, ok := OptionPaths(Baked(), "not-a-real-notifier-type")
	if ok {
		t.Fatal("expected unknown notifier type to report false")
	}
}

func TestSecretNames(t *testing.T) {
	names := SecretNames(Baked())

	for _, want := range []string{"password", "url"} {
		if _, present := names[want]; !present {
			t.Errorf("expected %q to be a secret name, got %v", want, names)
		}
	}

	if _, present := names["addresses"]; present {
		t.Error("did not expect addresses to be a secret name")
	}
}

func TestBakedSnapshotSurvivesADecodeEncodeRoundTrip(t *testing.T) {
	ns := Baked()

	encoded, err := json.Marshal(ns)
	if err != nil {
		t.Fatalf("re-encoding the baked vocabulary: %v", err)
	}

	var original, roundTripped any
	if err := json.Unmarshal(bakedSnapshot, &original); err != nil {
		t.Fatalf("decoding the snapshot: %v", err)
	}
	if err := json.Unmarshal(encoded, &roundTripped); err != nil {
		t.Fatalf("decoding the re-encoded vocabulary: %v", err)
	}

	if !reflect.DeepEqual(original, roundTripped) {
		t.Error("re-encoding the baked vocabulary did not reproduce the snapshot")
	}
}
