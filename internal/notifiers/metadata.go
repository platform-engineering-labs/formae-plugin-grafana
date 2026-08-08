// Package notifiers holds the Grafana legacy alert-notifier metadata vocabulary
// mirrored from GET /api/alert-notifiers: the set of notifier types and, for
// each, the option fields it accepts. This vocabulary is used to validate
// notifier settings and to classify which option names carry secrets.
package notifiers

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"
)

//go:embed alert-notifiers.json
var bakedSnapshot []byte

// secretOverrides names options treated as sensitive even where Grafana's
// metadata does not mark them secure. It is intentionally empty: "url" is
// already covered by the secure-in-any-type union below, and no other name
// currently needs an override. Add to it only when a real notifier ships an
// option that carries a secret without Grafana flagging it secure.
var secretOverrides = map[string]struct{}{}

// SelectOption is one choice offered by a "select" element field.
type SelectOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// ShowWhen conditions a field's visibility on the value of another field in
// the same notifier form.
type ShowWhen struct {
	Field string `json:"field"`
	Is    string `json:"is"`
}

// Field describes one option in a notifier's settings form, as returned by
// GET /api/alert-notifiers. SubformOptions holds the nested fields of a
// "subform" or "subform_array" element, so nesting can go arbitrarily deep.
// Only Protected carries omitempty, mirroring Grafana, which omits it when
// false; every other key is always present in the payload, so the tags match
// it exactly and a decode/encode round-trip reproduces the document.
type Field struct {
	Element        string         `json:"element"`
	InputType      string         `json:"inputType"`
	Label          string         `json:"label"`
	Description    string         `json:"description"`
	Placeholder    string         `json:"placeholder"`
	PropertyName   string         `json:"propertyName"`
	SelectOptions  []SelectOption `json:"selectOptions"`
	ShowWhen       ShowWhen       `json:"showWhen"`
	Required       bool           `json:"required"`
	ValidationRule string         `json:"validationRule"`
	Secure         bool           `json:"secure"`
	DependsOn      string         `json:"dependsOn"`
	Protected      bool           `json:"protected,omitempty"`
	SubformOptions []Field        `json:"subformOptions"`
}

// Notifier describes one notifier type's settings form, as returned by
// GET /api/alert-notifiers.
type Notifier struct {
	Type        string  `json:"type"`
	Name        string  `json:"name"`
	Heading     string  `json:"heading"`
	Description string  `json:"description"`
	Info        string  `json:"info"`
	Options     []Field `json:"options"`
}

var (
	bakedOnce sync.Once
	baked     []Notifier
)

// Baked returns the fallback notifier vocabulary embedded from
// alert-notifiers.json, parsed once on first use.
func Baked() []Notifier {
	bakedOnce.Do(func() {
		ns, err := Parse(bakedSnapshot)
		if err != nil {
			panic(fmt.Sprintf("notifiers: embedded alert-notifiers.json is invalid: %v", err))
		}
		baked = ns
	})
	return baked
}

// Parse decodes a GET /api/alert-notifiers response body into the notifier
// vocabulary, for use with metadata fetched from a live Grafana.
func Parse(data []byte) ([]Notifier, error) {
	var ns []Notifier
	if err := json.Unmarshal(data, &ns); err != nil {
		return nil, fmt.Errorf("notifiers: parse alert-notifiers payload: %w", err)
	}
	return ns, nil
}

// OptionPaths returns the set of accepted dotted key paths for the given
// notifier type (e.g. "http_config.oauth2.client_id" for a nested subform
// field), and false if notifierType is not present in ns.
func OptionPaths(ns []Notifier, notifierType string) (map[string]struct{}, bool) {
	for _, n := range ns {
		if n.Type != notifierType {
			continue
		}
		paths := make(map[string]struct{})
		collectPaths(n.Options, "", paths)
		return paths, true
	}
	return nil, false
}

// collectPaths walks a field tree, recording each field's dotted path
// (including intermediate subform paths) into paths.
func collectPaths(fields []Field, prefix string, paths map[string]struct{}) {
	for _, f := range fields {
		path := f.PropertyName
		if prefix != "" {
			path = prefix + "." + f.PropertyName
		}
		paths[path] = struct{}{}
		if len(f.SubformOptions) > 0 {
			collectPaths(f.SubformOptions, path, paths)
		}
	}
}

// SecretNames returns every option name marked secure in any notifier type
// in ns, unioned with secretOverrides.
func SecretNames(ns []Notifier) map[string]struct{} {
	names := make(map[string]struct{})
	for _, n := range ns {
		collectSecretNames(n.Options, names)
	}
	for name := range secretOverrides {
		names[name] = struct{}{}
	}
	return names
}

// collectSecretNames walks a field tree, recording the property name of
// every field marked secure into names.
func collectSecretNames(fields []Field, names map[string]struct{}) {
	for _, f := range fields {
		if f.Secure {
			names[f.PropertyName] = struct{}{}
		}
		if len(f.SubformOptions) > 0 {
			collectSecretNames(f.SubformOptions, names)
		}
	}
}
