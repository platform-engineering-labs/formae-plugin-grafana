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

// leaf builds a synthetic non-subform option.
func leaf(name, element string) Field {
	return Field{PropertyName: name, Element: element}
}

// subform builds a synthetic subform option carrying the given nested options.
func subform(name string, options ...Field) Field {
	return Field{PropertyName: name, Element: "subform", SubformOptions: options}
}

// vocabulary builds a single-type synthetic notifier vocabulary.
func vocabulary(options ...Field) []Notifier {
	return []Notifier{{Type: "synthetic", Options: options}}
}

// TestRenderContactPointSettings_RejectsUntypableVocabularies drives synthetic
// vocabularies through the renderer to exercise the branches a future snapshot
// refresh could reach, each of which must fail loudly and name the option at
// fault rather than emit a module that cannot be typed.
func TestRenderContactPointSettings_RejectsUntypableVocabularies(t *testing.T) {
	cases := []struct {
		name    string
		ns      []Notifier
		wantErr []string
	}{
		{
			name:    "unsupported element",
			ns:      vocabulary(leaf("mystery", "date_picker")),
			wantErr: []string{`"mystery"`, "unsupported element", `"date_picker"`},
		},
		{
			name: "unresolved cross-type element conflict",
			ns: []Notifier{
				{Type: "a", Options: []Field{leaf("retries", "input")}},
				{Type: "b", Options: []Field{leaf("retries", "checkbox")}},
			},
			wantErr: []string{`"retries"`, "different Pkl types"},
		},
		{
			name: "secure subform option",
			ns: vocabulary(Field{
				PropertyName:   "credentials",
				Element:        "subform",
				Secure:         true,
				SubformOptions: []Field{leaf("token", "input")},
			}),
			wantErr: []string{`"credentials"`, "marked secure"},
		},
		{
			name: "one subform name with two field shapes",
			ns: []Notifier{
				{Type: "a", Options: []Field{subform("auth", leaf("user", "input"))}},
				{Type: "b", Options: []Field{subform("auth", leaf("token", "input"))}},
			},
			wantErr: []string{`"auth"`, "more than one field shape"},
		},
		{
			name: "two subform names generating one class",
			ns: vocabulary(
				subform("tls_config", leaf("caCertificate", "input")),
				subform("tlsConfig", leaf("clientCertificate", "input")),
			),
			wantErr: []string{"tlsConfig", "tls_config", "TlsConfig", "different fields"},
		},
		{
			name:    "property name that is not a Pkl identifier",
			ns:      vocabulary(leaf("http-method", "input")),
			wantErr: []string{`"http-method"`, "not a valid Pkl identifier"},
		},
		{
			name:    "class name that is not a Pkl identifier",
			ns:      vocabulary(subform("9lives", leaf("attempt", "input"))),
			wantErr: []string{`"9lives"`, "not a valid Pkl identifier"},
		},
		{
			name:    "class name shadowing a Pkl builtin type",
			ns:      vocabulary(subform("string", leaf("value", "input"))),
			wantErr: []string{`"string"`, "String", "builtin"},
		},
		{
			name:    "class name colliding with the settings class",
			ns:      vocabulary(subform("contactPointSettings", leaf("value", "input"))),
			wantErr: []string{`"contactPointSettings"`, settingsClassName, "collides"},
		},
		{
			name:    "subform element without subform options",
			ns:      vocabulary(leaf("payload", "subform")),
			wantErr: []string{`"payload"`, "no subform options"},
		},
		{
			name:    "subform options on a non-subform element",
			ns:      vocabulary(Field{PropertyName: "payload", Element: "input", SubformOptions: []Field{leaf("template", "input")}}),
			wantErr: []string{`"payload"`, "nothing references"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := RenderContactPointSettings(tc.ns)
			if err == nil {
				t.Fatalf("expected an error, got nil")
			}
			for _, want := range tc.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
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
