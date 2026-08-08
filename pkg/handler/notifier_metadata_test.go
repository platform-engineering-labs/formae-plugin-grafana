// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	goapi "github.com/grafana/grafana-openapi-client-go/client"
	"github.com/platform-engineering-labs/formae-plugin-grafana/internal/notifiers"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// vocabularyFor builds a one-type notifier vocabulary from the dotted option
// paths that type accepts, so a test can pin a vocabulary exactly.
func vocabularyFor(notifierType string, paths ...string) []notifiers.Notifier {
	var options []notifiers.Field
	for _, path := range paths {
		options = withOptionPath(options, strings.Split(path, "."))
	}
	return []notifiers.Notifier{{Type: notifierType, Options: options}}
}

// withOptionPath adds one dotted path to a field tree, reusing the fields the
// path shares with the ones already present.
func withOptionPath(fields []notifiers.Field, segments []string) []notifiers.Field {
	if len(segments) == 0 {
		return fields
	}
	for i := range fields {
		if fields[i].PropertyName == segments[0] {
			fields[i].SubformOptions = withOptionPath(fields[i].SubformOptions, segments[1:])
			return fields
		}
	}
	added := notifiers.Field{PropertyName: segments[0]}
	added.SubformOptions = withOptionPath(nil, segments[1:])
	return append(fields, added)
}

// stubFetch stands in for the live metadata fetch, handing back the given
// vocabularies in turn (the last one for every later call) and counting the
// calls made, so a test can tell a cached lookup from a re-fetched one.
type stubFetch struct {
	vocabularies [][]notifiers.Notifier
	err          error
	calls        atomic.Int64
}

func (s *stubFetch) fetch(context.Context, *goapi.GrafanaHTTPAPI) ([]notifiers.Notifier, error) {
	call := int(s.calls.Add(1))
	if s.err != nil {
		return nil, s.err
	}
	if call > len(s.vocabularies) {
		call = len(s.vocabularies)
	}
	return s.vocabularies[call-1], nil
}

// vocabularyByTarget stands in for the live metadata fetch, handing each target
// back its own vocabulary, so a test can pin what one target reports
// independently of what another reports.
type vocabularyByTarget struct {
	vocabularies map[*goapi.GrafanaHTTPAPI][]notifiers.Notifier
}

func (v *vocabularyByTarget) fetch(_ context.Context, client *goapi.GrafanaHTTPAPI) ([]notifiers.Notifier, error) {
	vocabulary, ok := v.vocabularies[client]
	if !ok {
		return nil, errors.New("no vocabulary registered for this target")
	}
	return vocabulary, nil
}

// reportedVersion is the Grafana version a stub target answers /api/health
// with, settable between lookups to stand in for an in-place upgrade.
type reportedVersion struct {
	mu      sync.Mutex
	version string
}

func (r *reportedVersion) get() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.version
}

func (r *reportedVersion) set(version string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.version = version
}

// newVersionedGrafana returns a client for a target that answers /api/health
// with whatever version reports - an empty string answers 500, standing in for
// a target whose version cannot be read - and 404s every other read. Any write
// reaching it fails the test: invalid settings must be rejected before the
// contact point is submitted.
func newVersionedGrafana(t *testing.T, version func() string) *goapi.GrafanaHTTPAPI {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected %s %s: settings validation must reject before any write", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotImplemented)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/health") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		reported := version()
		if reported == "" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"database":"ok","version":%q}`, reported)
	}))
	t.Cleanup(server.Close)
	return clientForStub(t, server.URL)
}

// staticVersion reports one Grafana version for every lookup.
func staticVersion(version string) func() string {
	return func() string { return version }
}

// A key the declared type does not accept is rejected, and the message names
// both the key and the type: Grafana stores and echoes back any key it is
// given, so a wrong-for-type option would otherwise be accepted silently and
// never show as drift.
func TestValidateSettings_RejectsAKeyTheTypeDoesNotAccept(t *testing.T) {
	metadata := &notifierMetadata{fetch: (&stubFetch{
		vocabularies: [][]notifiers.Notifier{vocabularyFor("webhook", "url", "maxAlerts")},
	}).fetch}

	err := metadata.validateSettings(context.Background(), newVersionedGrafana(t, staticVersion("12.0.0")), "webhook",
		map[string]any{"url": "https://example.com/hook", "singleEmail": true})
	if err == nil {
		t.Fatal("a key the type does not accept was accepted")
	}
	if !strings.Contains(err.Error(), `"singleEmail"`) {
		t.Errorf("error does not name the offending key: %v", err)
	}
	if !strings.Contains(err.Error(), `"webhook"`) {
		t.Errorf("error does not name the notifier type: %v", err)
	}
}

// Every key the type declares passes, including one whose value is an object
// the vocabulary declares no fields for - a key/value map such as a webhook's
// `headers` accepts author-chosen keys, so its contents are opaque.
func TestValidateSettings_AcceptsDeclaredKeysAndOpaqueMaps(t *testing.T) {
	metadata := &notifierMetadata{fetch: (&stubFetch{
		vocabularies: [][]notifiers.Notifier{vocabularyFor("webhook", "url", "headers")},
	}).fetch}

	err := metadata.validateSettings(context.Background(), newVersionedGrafana(t, staticVersion("12.0.0")), "webhook",
		map[string]any{
			"url":     "https://example.com/hook",
			"headers": map[string]any{"X-Scope-OrgID": "formae"},
		})
	if err != nil {
		t.Errorf("declared keys rejected: %v", err)
	}
}

// A nested subform key is validated by its dotted path, which is how the
// vocabulary names it.
func TestValidateSettings_ValidatesNestedKeysByDottedPath(t *testing.T) {
	metadata := &notifierMetadata{fetch: (&stubFetch{
		vocabularies: [][]notifiers.Notifier{vocabularyFor("webhook", "url", "http_config.oauth2.client_id")},
	}).fetch}
	client := newVersionedGrafana(t, staticVersion("12.0.0"))

	valid := map[string]any{"http_config": map[string]any{"oauth2": map[string]any{"client_id": "formae"}}}
	if err := metadata.validateSettings(context.Background(), client, "webhook", valid); err != nil {
		t.Errorf("nested declared key rejected: %v", err)
	}

	invalid := map[string]any{"http_config": map[string]any{"oauth2": map[string]any{"clientId": "formae"}}}
	err := metadata.validateSettings(context.Background(), client, "webhook", invalid)
	if err == nil {
		t.Fatal("a nested key the type does not accept was accepted")
	}
	if !strings.Contains(err.Error(), `"http_config.oauth2.clientId"`) {
		t.Errorf("error does not name the offending key by its dotted path: %v", err)
	}
}

// A subform array's elements are validated against the subform's paths, so an
// invalid key inside one element is caught and named by its dotted path.
func TestValidateSettings_ValidatesSubformArrayElements(t *testing.T) {
	metadata := &notifierMetadata{fetch: (&stubFetch{
		vocabularies: [][]notifiers.Notifier{vocabularyFor("opsgenie", "responders.type", "responders.username")},
	}).fetch}

	settings := map[string]any{"responders": []any{
		map[string]any{"type": "team"},
		map[string]any{"escalation": "oncall"},
	}}
	err := metadata.validateSettings(context.Background(), newVersionedGrafana(t, staticVersion("12.0.0")), "opsgenie", settings)
	if err == nil {
		t.Fatal("a key the subform does not accept was accepted inside an array element")
	}
	if !strings.Contains(err.Error(), `"responders.escalation"`) {
		t.Errorf("error does not name the offending key by its dotted path: %v", err)
	}
}

// A type no notifier declares is itself a rejection: nothing binds its
// settings, so nothing about the contact point can be validated.
func TestValidateSettings_RejectsAnUnknownContactPointType(t *testing.T) {
	metadata := &notifierMetadata{fetch: (&stubFetch{
		vocabularies: [][]notifiers.Notifier{vocabularyFor("webhook", "url")},
	}).fetch}

	err := metadata.validateSettings(context.Background(), newVersionedGrafana(t, staticVersion("12.0.0")), "wehbook",
		map[string]any{"url": "https://example.com/hook"})
	if err == nil {
		t.Fatal("an unknown contactPointType was accepted")
	}
	if !strings.Contains(err.Error(), `"wehbook"`) {
		t.Errorf("error does not name the unknown type: %v", err)
	}
}

// Several invalid keys are reported in a stable order, so the same declaration
// always produces the same message.
func TestValidateSettings_ReportsSeveralInvalidKeysInAStableOrder(t *testing.T) {
	metadata := &notifierMetadata{fetch: (&stubFetch{
		vocabularies: [][]notifiers.Notifier{vocabularyFor("webhook", "url")},
	}).fetch}
	client := newVersionedGrafana(t, staticVersion("12.0.0"))
	settings := map[string]any{"url": "https://example.com/hook", "singleEmail": true, "addresses": "ops@example.com"}

	first := metadata.validateSettings(context.Background(), client, "webhook", settings)
	if first == nil {
		t.Fatal("keys the type does not accept were accepted")
	}
	if !strings.Contains(first.Error(), `"addresses", "singleEmail"`) {
		t.Errorf("invalid keys not reported in sorted order: %v", first)
	}
	for range 5 {
		again := metadata.validateSettings(context.Background(), client, "webhook", settings)
		if again == nil || again.Error() != first.Error() {
			t.Fatalf("message varies between runs: %v then %v", first, again)
		}
	}
}

// An unreadable live vocabulary falls back to the baked one rather than
// skipping validation - that fallback still catches a wrong-for-type key,
// which is the only check binding a contact point's settings to its type.
func TestValidateSettings_FallsBackToTheBakedVocabularyWhenTheFetchFails(t *testing.T) {
	metadata := &notifierMetadata{fetch: (&stubFetch{err: errors.New("alert-notifiers returned status 404")}).fetch}
	client := newVersionedGrafana(t, staticVersion("12.0.0"))

	err := metadata.validateSettings(context.Background(), client, "webhook", map[string]any{"singleEmail": true})
	if err == nil {
		t.Fatal("validation failed open when the live vocabulary was unreadable")
	}
	if !strings.Contains(err.Error(), `"singleEmail"`) || !strings.Contains(err.Error(), `"webhook"`) {
		t.Errorf("error does not name the key and the type: %v", err)
	}

	if err := metadata.validateSettings(context.Background(), client, "webhook", map[string]any{"url": "https://example.com/hook"}); err != nil {
		t.Errorf("the baked vocabulary rejected a key webhook accepts: %v", err)
	}
}

// A version the target will not report leaves nothing to cache under, so the
// baked vocabulary is used and no fetch is attempted. Validation still runs.
func TestValidateSettings_UsesTheBakedVocabularyWhenTheVersionIsUnreadable(t *testing.T) {
	fetch := &stubFetch{vocabularies: [][]notifiers.Notifier{vocabularyFor("webhook", "url", "singleEmail")}}
	metadata := &notifierMetadata{fetch: fetch.fetch}
	client := newVersionedGrafana(t, staticVersion(""))

	err := metadata.validateSettings(context.Background(), client, "webhook", map[string]any{"singleEmail": true})
	if err == nil {
		t.Fatal("validation failed open when the target's version was unreadable")
	}
	if !strings.Contains(err.Error(), `"singleEmail"`) {
		t.Errorf("error does not name the offending key: %v", err)
	}
	if fetch.calls.Load() != 0 {
		t.Errorf("fetches = %d, want none: there is no version to cache a fetched vocabulary under", fetch.calls.Load())
	}
}

// A target's vocabulary is cached against the version it reported: a second
// lookup at the same version is served from the cache, and a version change
// re-fetches, so a target upgraded in place is validated against its new
// vocabulary.
func TestValidateSettings_CachesPerGrafanaVersion(t *testing.T) {
	fetch := &stubFetch{vocabularies: [][]notifiers.Notifier{
		vocabularyFor("webhook", "url"),
		vocabularyFor("webhook", "url", "maxAlerts"),
	}}
	metadata := &notifierMetadata{fetch: fetch.fetch}
	version := &reportedVersion{version: "12.0.0"}
	client := newVersionedGrafana(t, version.get)
	settings := map[string]any{"maxAlerts": "10"}

	if err := metadata.validateSettings(context.Background(), client, "webhook", settings); err == nil {
		t.Fatal("the pre-upgrade vocabulary accepted a key it does not declare")
	}
	if err := metadata.validateSettings(context.Background(), client, "webhook", settings); err == nil {
		t.Fatal("the cached vocabulary accepted a key it does not declare")
	}
	if fetch.calls.Load() != 1 {
		t.Errorf("fetches = %d, want 1: the second lookup must be served from the cache", fetch.calls.Load())
	}

	version.set("12.1.0")
	if err := metadata.validateSettings(context.Background(), client, "webhook", settings); err != nil {
		t.Errorf("a key the upgraded target's vocabulary declares was rejected: %v", err)
	}
	if fetch.calls.Load() != 2 {
		t.Errorf("fetches = %d, want 2: a changed version must re-fetch", fetch.calls.Load())
	}
}

// Two targets reporting the same Grafana version can still accept different
// options - feature toggles, Enterprise versus OSS builds and disabled
// integrations all change the vocabulary at a fixed version - so each is
// validated against the vocabulary it reported, not against whichever target
// was looked up first.
func TestValidateSettings_ValidatesEachTargetAgainstItsOwnVocabulary(t *testing.T) {
	first := newVersionedGrafana(t, staticVersion("12.0.0"))
	second := newVersionedGrafana(t, staticVersion("12.0.0"))
	metadata := &notifierMetadata{fetch: (&vocabularyByTarget{
		vocabularies: map[*goapi.GrafanaHTTPAPI][]notifiers.Notifier{
			first:  vocabularyFor("webhook", "url", "maxAlerts"),
			second: append(vocabularyFor("webhook", "url", "singleEmail"), vocabularyFor("oncall", "url")...),
		},
	}).fetch}

	if err := metadata.validateSettings(context.Background(), first, "webhook", map[string]any{"maxAlerts": "10"}); err != nil {
		t.Errorf("a key the first target's vocabulary declares was rejected: %v", err)
	}
	if err := metadata.validateSettings(context.Background(), second, "webhook", map[string]any{"singleEmail": true}); err != nil {
		t.Errorf("a key the second target's vocabulary declares was rejected: %v", err)
	}
	if err := metadata.validateSettings(context.Background(), second, "webhook", map[string]any{"maxAlerts": "10"}); err == nil {
		t.Error("the second target accepted a key only the first target's vocabulary declares")
	}

	// A notifier type one target declares and the other does not is the same
	// bleed seen through the type rather than through an option.
	if err := metadata.validateSettings(context.Background(), second, "oncall", map[string]any{"url": "https://example.com/oncall"}); err != nil {
		t.Errorf("a type the second target's vocabulary declares was rejected: %v", err)
	}
	if err := metadata.validateSettings(context.Background(), first, "oncall", map[string]any{"url": "https://example.com/oncall"}); err == nil {
		t.Error("the first target accepted a type only the second target's vocabulary declares")
	}
}

// A payload that parses but declares no notifier type at all is as unusable as
// an unreadable one: caching it would reject every contact point written
// against that target until the process restarts. It falls back to the baked
// vocabulary - which still rejects a wrong-for-type key - and is not cached.
func TestValidateSettings_FallsBackWhenTheFetchedVocabularyDeclaresNoNotifier(t *testing.T) {
	unusable, err := notifiers.Parse([]byte(`[{"unexpected":"shape"}]`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	fetch := &stubFetch{vocabularies: [][]notifiers.Notifier{unusable}}
	metadata := &notifierMetadata{fetch: fetch.fetch}
	client := newVersionedGrafana(t, staticVersion("12.0.0"))

	invalid := metadata.validateSettings(context.Background(), client, "webhook", map[string]any{"singleEmail": true})
	if invalid == nil {
		t.Fatal("validation failed open when the fetched vocabulary declared no notifier")
	}
	if !strings.Contains(invalid.Error(), `"singleEmail"`) || !strings.Contains(invalid.Error(), `"webhook"`) {
		t.Errorf("error does not name the key and the type: %v", invalid)
	}
	if err := metadata.validateSettings(context.Background(), client, "webhook", map[string]any{"url": "https://example.com/hook"}); err != nil {
		t.Errorf("the baked vocabulary rejected a key webhook accepts: %v", err)
	}
	if fetch.calls.Load() != 2 {
		t.Errorf("fetches = %d, want 2: an unusable vocabulary must not be cached", fetch.calls.Load())
	}
}

// An empty vocabulary would reject every contact point, so it is treated the
// same way: the baked vocabulary is used, a wrong-for-type key is still
// rejected, and nothing is cached.
func TestValidateSettings_FallsBackWhenTheFetchedVocabularyIsEmpty(t *testing.T) {
	fetch := &stubFetch{vocabularies: [][]notifiers.Notifier{{}}}
	metadata := &notifierMetadata{fetch: fetch.fetch}
	client := newVersionedGrafana(t, staticVersion("12.0.0"))

	invalid := metadata.validateSettings(context.Background(), client, "webhook", map[string]any{"singleEmail": true})
	if invalid == nil {
		t.Fatal("validation failed open when the fetched vocabulary was empty")
	}
	if !strings.Contains(invalid.Error(), `"singleEmail"`) || !strings.Contains(invalid.Error(), `"webhook"`) {
		t.Errorf("error does not name the key and the type: %v", invalid)
	}
	if err := metadata.validateSettings(context.Background(), client, "webhook", map[string]any{"url": "https://example.com/hook"}); err != nil {
		t.Errorf("the baked vocabulary rejected a key webhook accepts: %v", err)
	}
	if fetch.calls.Load() != 2 {
		t.Errorf("fetches = %d, want 2: an empty vocabulary must not be cached", fetch.calls.Load())
	}
}

// One bad key repeated across the elements of a subform array is one fault, so
// it is named once and counted once.
func TestValidateSettings_ReportsARepeatedInvalidKeyOnce(t *testing.T) {
	metadata := &notifierMetadata{fetch: (&stubFetch{
		vocabularies: [][]notifiers.Notifier{vocabularyFor("opsgenie", "responders.type", "responders.username")},
	}).fetch}

	settings := map[string]any{"responders": []any{
		map[string]any{"escalation": "primary"},
		map[string]any{"escalation": "secondary"},
		map[string]any{"escalation": "tertiary"},
	}}
	err := metadata.validateSettings(context.Background(), newVersionedGrafana(t, staticVersion("12.0.0")), "opsgenie", settings)
	if err == nil {
		t.Fatal("a key the subform does not accept was accepted inside an array element")
	}
	if got := strings.Count(err.Error(), `"responders.escalation"`); got != 1 {
		t.Errorf("offending key named %d times, want once: %v", got, err)
	}
	if !strings.Contains(err.Error(), "settings key ") {
		t.Errorf("one repeated key reported as several: %v", err)
	}
}

// The handler is used from several goroutines at once, so the cache is shared
// state: concurrent lookups must be safe, and once one has stored a vocabulary
// the later ones are served from the cache.
func TestValidateSettings_IsSafeUnderConcurrentLookups(t *testing.T) {
	fetch := &stubFetch{vocabularies: [][]notifiers.Notifier{vocabularyFor("webhook", "url")}}
	metadata := &notifierMetadata{fetch: fetch.fetch}
	client := newVersionedGrafana(t, staticVersion("12.0.0"))

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := metadata.validateSettings(context.Background(), client, "webhook", map[string]any{"url": "https://example.com/hook"}); err != nil {
				t.Errorf("declared key rejected: %v", err)
			}
		}()
	}
	wg.Wait()

	// Lookups racing to fill an empty cache may each fetch, which is harmless -
	// they store the same vocabulary - but once one has stored it no further
	// fetch is made.
	fetched := fetch.calls.Load()
	if fetched == 0 {
		t.Fatal("fetches = 0, want at least 1: the live vocabulary was never read")
	}
	if err := metadata.validateSettings(context.Background(), client, "webhook", map[string]any{"url": "https://example.com/hook"}); err != nil {
		t.Errorf("declared key rejected: %v", err)
	}
	if fetch.calls.Load() != fetched {
		t.Errorf("fetches = %d, want %d: a lookup after the burst must be served from the cache", fetch.calls.Load(), fetched)
	}
}

// A hung target must not stall every other contact-point validation in the
// process: a fetch in flight for one target holds no lock another target's
// lookup needs. Failures are deliberately not cached, so a lock held across the
// fetch would cost every write the full client timeout, one after another.
func TestValidateSettings_DoesNotBlockAnotherTargetBehindAnInFlightFetch(t *testing.T) {
	slow := newVersionedGrafana(t, staticVersion("12.0.0"))
	other := newVersionedGrafana(t, staticVersion("12.0.0"))
	started := make(chan struct{})
	release := make(chan struct{})
	metadata := &notifierMetadata{fetch: func(_ context.Context, client *goapi.GrafanaHTTPAPI) ([]notifiers.Notifier, error) {
		if client == slow {
			close(started)
			<-release
		}
		return vocabularyFor("webhook", "url"), nil
	}}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := metadata.validateSettings(context.Background(), slow, "webhook", map[string]any{"url": "https://example.com/hook"}); err != nil {
			t.Errorf("declared key rejected: %v", err)
		}
	}()
	<-started

	done := make(chan error, 1)
	go func() {
		done <- metadata.validateSettings(context.Background(), other, "webhook", map[string]any{"url": "https://example.com/hook"})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("declared key rejected: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("a lookup for another target blocked behind an in-flight fetch")
	}

	close(release)
	wg.Wait()
}

// The live vocabulary is read through the client's own transport, so it reaches
// the target's configured base path carrying the target's credentials rather
// than a second client's.
func TestFetchNotifierMetadata_ReadsThroughTheConfiguredTransport(t *testing.T) {
	var mu sync.Mutex
	var gotPath, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"type":"webhook","name":"Webhook","options":[
			{"propertyName":"url","element":"input"},
			{"propertyName":"http_config","element":"subform","subformOptions":[
				{"propertyName":"oauth2","element":"subform","subformOptions":[
					{"propertyName":"client_id","element":"input"}]}]}]}]`))
	}))
	t.Cleanup(server.Close)

	got, err := fetchNotifierMetadata(context.Background(), clientForStub(t, server.URL))
	if err != nil {
		t.Fatalf("fetchNotifierMetadata: %v", err)
	}

	paths, ok := notifiers.OptionPaths(got, "webhook")
	if !ok {
		t.Fatalf("fetched vocabulary does not declare webhook: %#v", got)
	}
	if _, present := paths["http_config.oauth2.client_id"]; !present {
		t.Errorf("nested option path missing from the fetched vocabulary: %v", paths)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotPath != "/api/alert-notifiers" {
		t.Errorf("requested path = %q, want /api/alert-notifiers", gotPath)
	}
	if gotAuth != "Bearer stub-token" {
		t.Errorf("Authorization = %q, want the target's configured credential", gotAuth)
	}
}

// A target that does not serve the route reports an error, which is what sends
// the caller to the baked vocabulary.
func TestFetchNotifierMetadata_FailsWhenTheRouteIsUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)

	if _, err := fetchNotifierMetadata(context.Background(), clientForStub(t, server.URL)); err == nil {
		t.Fatal("an unavailable route was read as a vocabulary")
	}
}

// Create rejects invalid settings before submitting the contact point: the stub
// target fails the test if a write reaches it.
func TestContactPointCreate_RejectsInvalidSettingsBeforeWriting(t *testing.T) {
	h := &ContactPointHandler{metadata: notifierMetadata{fetch: (&stubFetch{
		vocabularies: [][]notifiers.Notifier{vocabularyFor("webhook", "url")},
	}).fetch}}
	props := json.RawMessage(`{"name":"webhook-cp","contactPointType":"webhook",
		"settings":{"url":"https://example.com/hook","singleEmail":true}}`)

	result, err := h.Create(context.Background(), newVersionedGrafana(t, staticVersion("12.0.0")), props)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.OperationStatus != resource.OperationStatusFailure {
		t.Fatalf("status = %v, want failure", result.OperationStatus)
	}
	if result.ErrorCode != resource.OperationErrorCodeInvalidRequest {
		t.Errorf("ErrorCode = %q, want %q", result.ErrorCode, resource.OperationErrorCodeInvalidRequest)
	}
	if !strings.Contains(result.StatusMessage, `"singleEmail"`) || !strings.Contains(result.StatusMessage, `"webhook"`) {
		t.Errorf("StatusMessage does not name the key and the type: %q", result.StatusMessage)
	}
}

// Update rejects invalid settings before submitting the contact point.
func TestContactPointUpdate_RejectsInvalidSettingsBeforeWriting(t *testing.T) {
	h := &ContactPointHandler{metadata: notifierMetadata{fetch: (&stubFetch{
		vocabularies: [][]notifiers.Notifier{vocabularyFor("webhook", "url")},
	}).fetch}}
	desired := json.RawMessage(`{"name":"webhook-cp","contactPointType":"webhook",
		"settings":{"url":"https://example.com/hook","singleEmail":true}}`)

	result, err := h.Update(context.Background(), newVersionedGrafana(t, staticVersion("12.0.0")), "cp-1", nil, desired)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.OperationStatus != resource.OperationStatusFailure {
		t.Fatalf("status = %v, want failure", result.OperationStatus)
	}
	if result.ErrorCode != resource.OperationErrorCodeInvalidRequest {
		t.Errorf("ErrorCode = %q, want %q", result.ErrorCode, resource.OperationErrorCodeInvalidRequest)
	}
	if !strings.Contains(result.StatusMessage, `"singleEmail"`) || !strings.Contains(result.StatusMessage, `"webhook"`) {
		t.Errorf("StatusMessage does not name the key and the type: %q", result.StatusMessage)
	}
}
