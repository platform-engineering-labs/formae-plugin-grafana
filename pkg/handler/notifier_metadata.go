package handler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/go-openapi/runtime"
	"github.com/go-openapi/strfmt"
	goapi "github.com/grafana/grafana-openapi-client-go/client"
	"github.com/grafana/grafana-openapi-client-go/client/health"
	"github.com/platform-engineering-labs/formae-plugin-grafana/internal/notifiers"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

// metadataFetcher reads the notifier vocabulary a Grafana target accepts.
type metadataFetcher func(ctx context.Context, client *goapi.GrafanaHTTPAPI) ([]notifiers.Notifier, error)

// vocabularyKey identifies the cached vocabulary of one target at one reported
// Grafana version.
//
// The client stands in for the target's identity: exactly one client is built
// and cached per distinct target config, so two targets never share one. Two
// targets can report the same version and still accept different options -
// feature toggles, Enterprise versus OSS builds and disabled integrations all
// change the vocabulary at a fixed version - so keying on the version alone
// would validate one target against another's vocabulary.
//
// The version stays in the key because a target upgraded in place keeps its
// config, and with it its cached client, while its vocabulary changes: without
// the version the pre-upgrade vocabulary would outlive the upgrade and reject
// an option the upgraded target accepts.
type vocabularyKey struct {
	client  *goapi.GrafanaHTTPAPI
	version string
}

// notifierMetadata resolves the notifier vocabulary a contact point's settings
// are validated against, preferring the target's live metadata and falling back
// to the baked vocabulary. Its zero value is ready to use; fetch is a seam that
// lets a test drive the lookup without a live target.
type notifierMetadata struct {
	fetch metadataFetcher

	mu    sync.Mutex
	cache map[vocabularyKey][]notifiers.Notifier
}

// validateSettings rejects settings keys the declared notifier type does not
// accept, naming each offending key by its dotted path along with the type.
// Grafana stores and echoes back any key it is given, including an invented
// one, so a wrong-for-type or unsupported option is otherwise accepted silently
// and never reported as drift - this is the only check that binds a contact
// point's settings to its type.
//
// An unknown type is itself a rejection: nothing declares what its settings
// mean, so nothing about the contact point can be checked.
func (m *notifierMetadata) validateSettings(ctx context.Context, client *goapi.GrafanaHTTPAPI, notifierType string, settings map[string]any) error {
	vocabulary := m.vocabulary(ctx, client)

	accepted, known := notifiers.OptionPaths(vocabulary, notifierType)
	if !known {
		return fmt.Errorf("unknown contactPointType %q: no notifier declares it", notifierType)
	}

	invalid := make(map[string]struct{})
	collectInvalidPaths(settings, "", accepted, invalid)
	if len(invalid) == 0 {
		return nil
	}

	// Collected as a set so one bad key repeated across the elements of a
	// subform array is one fault, and sorted so the same declaration always
	// produces the same message: map iteration order would otherwise vary from
	// run to run.
	paths := make([]string, 0, len(invalid))
	for path := range invalid {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	quoted := make([]string, len(paths))
	for i, path := range paths {
		quoted[i] = fmt.Sprintf("%q", path)
	}
	noun := "key"
	if len(quoted) > 1 {
		noun = "keys"
	}
	return fmt.Errorf("settings %s %s not accepted by contact point type %q", noun, strings.Join(quoted, ", "), notifierType)
}

// vocabulary returns the notifier vocabulary to validate against for the target
// behind client. It never returns an empty vocabulary: when the target's
// version or its live metadata cannot be read it returns the baked one, so
// validation degrades to the shipped vocabulary instead of being skipped.
func (m *notifierMetadata) vocabulary(ctx context.Context, client *goapi.GrafanaHTTPAPI) []notifiers.Notifier {
	log := plugin.LoggerFromContext(ctx)

	version, err := grafanaVersion(ctx, client)
	if err != nil {
		// Without a version there is no key to cache a fetched vocabulary
		// under, and caching one under an unknown key would outlive the target
		// it was read from.
		log.Warn("validating contact point settings against the baked notifier vocabulary: the target's Grafana version is unreadable", "error", err)
		return notifiers.Baked()
	}

	key := vocabularyKey{client: client, version: version}

	m.mu.Lock()
	cached, ok := m.cache[key]
	m.mu.Unlock()
	if ok {
		return cached
	}

	// Fetched without the lock held: a hung target would otherwise stall every
	// contact-point validation in the process, and since a failure is
	// deliberately not cached each write would pay the full client timeout in
	// turn. Two lookups racing to fill the same key is harmless - they store
	// the same vocabulary.
	fetch := m.fetch
	if fetch == nil {
		fetch = fetchNotifierMetadata
	}
	fetched, err := fetch(ctx, client)
	if err != nil {
		log.Warn("validating contact point settings against the baked notifier vocabulary: the target's notifier metadata is unreadable",
			"grafana_version", version, "error", err)
		return notifiers.Baked()
	}
	if !declaresANotifier(fetched) {
		// A vocabulary naming no notifier type would reject every contact point
		// written against this target until the process restarts, so a payload
		// that yields none - empty, or of an unexpected shape that parses into
		// typeless entries - is treated as unreadable rather than
		// authoritative, and is not cached.
		log.Warn("validating contact point settings against the baked notifier vocabulary: the target reported no usable notifiers",
			"grafana_version", version)
		return notifiers.Baked()
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cache == nil {
		m.cache = make(map[vocabularyKey][]notifiers.Notifier)
	}
	m.cache[key] = fetched
	return fetched
}

// declaresANotifier reports whether a vocabulary names at least one notifier
// type, which is what makes it usable for validation.
func declaresANotifier(ns []notifiers.Notifier) bool {
	for _, n := range ns {
		if n.Type != "" {
			return true
		}
	}
	return false
}

// collectInvalidPaths walks settings, recording into invalid the dotted path of
// every key the accepted set does not contain. A set, so a key repeated across
// the elements of a subform array is recorded once.
//
// It descends into an object only where the accepted set declares fields below
// that path, which is what distinguishes a nested settings block from a
// key/value map: a map's keys are author-chosen (a webhook's `headers`, a
// PagerDuty `details`), so its contents are opaque and validating them would
// reject every entry. Objects inside an array are the elements of a subform
// array, whose fields hang off the array's own path.
func collectInvalidPaths(settings map[string]any, prefix string, accepted map[string]struct{}, invalid map[string]struct{}) {
	for key, value := range settings {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		if _, ok := accepted[path]; !ok {
			// A key that is not accepted makes its contents moot.
			invalid[path] = struct{}{}
			continue
		}
		if !hasNestedPaths(path, accepted) {
			continue
		}
		switch nested := value.(type) {
		case map[string]any:
			collectInvalidPaths(nested, path, accepted, invalid)
		case []any:
			for _, element := range nested {
				if object, ok := element.(map[string]any); ok {
					collectInvalidPaths(object, path, accepted, invalid)
				}
			}
		}
	}
}

// hasNestedPaths reports whether the accepted set declares any field below
// path.
func hasNestedPaths(path string, accepted map[string]struct{}) bool {
	prefix := path + "."
	for candidate := range accepted {
		if strings.HasPrefix(candidate, prefix) {
			return true
		}
	}
	return false
}

// grafanaVersion reads the target's Grafana version, which keys the notifier
// vocabulary cache.
func grafanaVersion(ctx context.Context, client *goapi.GrafanaHTTPAPI) (string, error) {
	resp, err := client.Health.GetHealthWithParams(health.NewGetHealthParamsWithContext(ctx))
	if err != nil {
		return "", err
	}
	payload := resp.GetPayload()
	if payload == nil || payload.Version == "" {
		return "", fmt.Errorf("health response reports no version")
	}
	return payload.Version, nil
}

// fetchNotifierMetadata reads the target's notifier vocabulary from
// GET /api/alert-notifiers. The generated client does not expose that route, so
// the request goes through the client's own transport: it then carries the
// target's configured credentials, base path, scheme and HTTP client rather
// than a second client's. A Viewer-scoped credential can read the route, and
// any credential able to write a contact point outranks Viewer.
func fetchNotifierMetadata(ctx context.Context, client *goapi.GrafanaHTTPAPI) ([]notifiers.Notifier, error) {
	result, err := client.Transport.Submit(&runtime.ClientOperation{
		ID:                 "getAlertNotifiers",
		Method:             http.MethodGet,
		PathPattern:        "/alert-notifiers",
		ProducesMediaTypes: []string{"application/json"},
		ConsumesMediaTypes: []string{"application/json"},
		Schemes:            []string{"http", "https"},
		Params: runtime.ClientRequestWriterFunc(func(runtime.ClientRequest, strfmt.Registry) error {
			return nil
		}),
		Reader:  runtime.ClientResponseReaderFunc(readNotifierMetadata),
		Context: ctx,
	})
	if err != nil {
		return nil, err
	}
	ns, ok := result.([]notifiers.Notifier)
	if !ok {
		return nil, fmt.Errorf("unexpected alert-notifiers response of type %T", result)
	}
	return ns, nil
}

// readNotifierMetadata decodes an alert-notifiers response body into the
// notifier vocabulary.
func readNotifierMetadata(response runtime.ClientResponse, _ runtime.Consumer) (any, error) {
	body := response.Body()
	defer func() { _ = body.Close() }()

	if response.Code() != http.StatusOK {
		return nil, fmt.Errorf("alert-notifiers returned status %d", response.Code())
	}
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("read alert-notifiers response: %w", err)
	}
	return notifiers.Parse(data)
}
