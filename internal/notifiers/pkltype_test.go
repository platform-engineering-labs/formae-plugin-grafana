// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package notifiers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// propertyType returns the generated Pkl type of class.property, failing the
// test when either the class or the property is absent.
func propertyType(t *testing.T, classes []pklClass, class, property string) string {
	t.Helper()

	for _, c := range classes {
		if c.name != class {
			continue
		}
		for _, p := range c.props {
			if p.name == property {
				return p.typ
			}
		}
		t.Fatalf("class %s has no property %q", class, property)
	}
	t.Fatalf("no generated class named %s", class)
	return ""
}

func classNames(classes []pklClass) []string {
	names := make([]string, 0, len(classes))
	for _, c := range classes {
		names = append(names, c.name)
	}
	return names
}

func TestSettingsClasses_CheckboxIsPlainBoolean(t *testing.T) {
	classes, err := settingsClasses(Baked())
	if err != nil {
		t.Fatalf("building the settings classes: %v", err)
	}

	got := propertyType(t, classes, "ContactPointSettings", "singleEmail")
	if got != "Boolean?" {
		t.Errorf("singleEmail: got %q, want %q", got, "Boolean?")
	}
	if strings.Contains(got, "Resolvable") {
		t.Errorf("singleEmail must not admit a Resolvable; got %q", got)
	}
}

func TestSettingsClasses_SecretOptionAdmitsValueAndSecretValue(t *testing.T) {
	classes, err := settingsClasses(Baked())
	if err != nil {
		t.Fatalf("building the settings classes: %v", err)
	}

	want := "(String|formae.Value|formae.SecretValue|formae.Resolvable)?"
	for _, property := range []string{"password", "url", "integrationKey"} {
		if got := propertyType(t, classes, "ContactPointSettings", property); got != want {
			t.Errorf("%s: got %q, want %q", property, got, want)
		}
	}
}

func TestSettingsClasses_NonSecretStringOptionIsResolvable(t *testing.T) {
	classes, err := settingsClasses(Baked())
	if err != nil {
		t.Fatalf("building the settings classes: %v", err)
	}

	want := "(String|formae.Resolvable)?"
	// input, textarea and select all collapse onto the same Pkl type, so an
	// option whose element varies across notifier types is not a conflict.
	for _, property := range []string{"recipient", "addresses", "httpMethod", "title", "priority"} {
		if got := propertyType(t, classes, "ContactPointSettings", property); got != want {
			t.Errorf("%s: got %q, want %q", property, got, want)
		}
	}
}

func TestSettingsClasses_DetailsAdmitsBothConflictingShapes(t *testing.T) {
	classes, err := settingsClasses(Baked())
	if err != nil {
		t.Fatalf("building the settings classes: %v", err)
	}

	want := "(String|Mapping<String, String>)?"
	if got := propertyType(t, classes, "ContactPointSettings", "details"); got != want {
		t.Errorf("details: got %q, want %q", got, want)
	}
}

func TestSettingsClasses_CollectionElements(t *testing.T) {
	classes, err := settingsClasses(Baked())
	if err != nil {
		t.Fatalf("building the settings classes: %v", err)
	}

	cases := map[string]string{
		"headers":    "Mapping<String, String>?",
		"labels":     "Listing<String>?",
		"responders": "Listing<Responders>?",
		"payload":    "Payload?",
	}
	for property, want := range cases {
		if got := propertyType(t, classes, "ContactPointSettings", property); got != want {
			t.Errorf("%s: got %q, want %q", property, got, want)
		}
	}
}

func TestSettingsClasses_SubformClassesShareIdenticalShapes(t *testing.T) {
	classes, err := settingsClasses(Baked())
	if err != nil {
		t.Fatalf("building the settings classes: %v", err)
	}

	// tlsConfig and tls_config carry identical field sets, so they share one
	// generated class rather than producing two duplicate-shaped ones.
	want := []string{
		"HmacConfig", "Payload", "ProxyConfig", "Responders", "Sigv4", "TlsConfig",
		"Oauth2", "HttpConfig", "ContactPointSettings",
	}
	got := classNames(classes)
	if len(got) != len(want) {
		t.Fatalf("got %d classes %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("class order: got %v, want %v", got, want)
		}
	}

	if got := propertyType(t, classes, "TlsConfig", "clientKey"); !strings.Contains(got, "formae.SecretValue") {
		t.Errorf("TlsConfig.clientKey: got %q, want a secret-bearing union", got)
	}
	if got := propertyType(t, classes, "TlsConfig", "insecureSkipVerify"); got != "Boolean?" {
		t.Errorf("TlsConfig.insecureSkipVerify: got %q, want %q", got, "Boolean?")
	}
	if got := propertyType(t, classes, "HttpConfig", "oauth2"); got != "Oauth2?" {
		t.Errorf("HttpConfig.oauth2: got %q, want %q", got, "Oauth2?")
	}
	if got := propertyType(t, classes, "Oauth2", "tls_config"); got != "TlsConfig?" {
		t.Errorf("Oauth2.tls_config: got %q, want %q", got, "TlsConfig?")
	}
}

func TestSettingsClasses_CoversEveryTopLevelOptionName(t *testing.T) {
	classes, err := settingsClasses(Baked())
	if err != nil {
		t.Fatalf("building the settings classes: %v", err)
	}

	settings := classes[len(classes)-1]
	if settings.name != "ContactPointSettings" {
		t.Fatalf("expected ContactPointSettings last, got %s", settings.name)
	}
	if len(settings.props) != 112 {
		t.Errorf("got %d top-level properties, want 112", len(settings.props))
	}
}

func TestRenderContactPointSettings_BackticksThePklKeyword(t *testing.T) {
	source, err := RenderContactPointSettings(Baked())
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}

	if !strings.Contains(source, "  `class`: (String|formae.Resolvable)?\n") {
		t.Error("expected the `class` option to be emitted as a backtick-quoted property")
	}
}

func TestRenderContactPointSettings_EmitsCaseDistinctNames(t *testing.T) {
	source, err := RenderContactPointSettings(Baked())
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}

	// apiKey/apikey and msgType/msgtype are distinct Grafana option names.
	for _, want := range []string{
		"  apiKey: ", "  apikey: ", "  msgType: ", "  msgtype: ",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("expected the rendered module to declare %q", strings.TrimSpace(want))
		}
	}
}

func TestRenderContactPointSettings_IsDeterministic(t *testing.T) {
	first, err := RenderContactPointSettings(Baked())
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}
	second, err := RenderContactPointSettings(Baked())
	if err != nil {
		t.Fatalf("re-rendering: %v", err)
	}

	if first != second {
		t.Error("two renders of the same vocabulary produced different output")
	}
}

func TestRenderContactPointSettings_MatchesTheCommittedModule(t *testing.T) {
	source, err := RenderContactPointSettings(Baked())
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}

	path := filepath.Join("..", "..", GeneratedSettingsPath)
	committed, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the committed module: %v", err)
	}

	if string(committed) != source {
		t.Errorf("%s is stale; refresh it with `make generate`", GeneratedSettingsPath)
	}
}
