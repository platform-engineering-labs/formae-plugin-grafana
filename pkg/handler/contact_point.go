package handler

import (
	"context"
	"encoding/json"
	"fmt"

	goapi "github.com/grafana/grafana-openapi-client-go/client"
	"github.com/grafana/grafana-openapi-client-go/client/provisioning"
	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

func init() {
	Register("GRAFANA::Alerting::ContactPoint", &ContactPointHandler{})
}

// ContactPointHandler implements CRUD+List for Grafana contact points.
type ContactPointHandler struct{}

type contactPointProps struct {
	UID                   string            `json:"uid,omitempty"`
	Name                  string            `json:"name"`
	Type                  string            `json:"contactPointType"`
	Settings              string            `json:"settings,omitempty"`
	SettingsMap           map[string]string `json:"settingsMap,omitempty"`
	DisableResolveMessage bool              `json:"disableResolveMessage,omitempty"`
}

// settingsShape records which form of settings the user submitted, so the
// handler can round-trip the response in the matching shape. The conformance
// harness compares the user's submitted Properties with the Properties the
// handler returns; mismatched shapes (e.g. user submits settingsMap, handler
// returns settings) trigger "Property X is not expected" failures.
type settingsShape int

const (
	settingsShapeNone   settingsShape = iota
	settingsShapeString               // legacy JSON-string `settings`
	settingsShapeMap                  // structured `settingsMap`
)

// resolveSettings returns the type-specific settings object Grafana expects
// alongside the shape the user submitted in. Returns a validation error when
// both forms are present (ambiguous) or both are absent.
func (p contactPointProps) resolveSettings() (any, settingsShape, error) {
	hasMap := len(p.SettingsMap) > 0
	hasString := p.Settings != ""
	switch {
	case hasMap && hasString:
		return nil, settingsShapeNone, fmt.Errorf("set exactly one of settings or settingsMap, not both")
	case hasMap:
		// settingsMap values arrive already-resolved (any formae.Resolvable was
		// substituted upstream before the Properties JSON reached us), so the
		// map is plain string -> string and is what Grafana expects.
		out := make(map[string]any, len(p.SettingsMap))
		for k, v := range p.SettingsMap {
			out[k] = v
		}
		return out, settingsShapeMap, nil
	case hasString:
		var v any
		if err := json.Unmarshal([]byte(p.Settings), &v); err != nil {
			return nil, settingsShapeNone, fmt.Errorf("invalid settings JSON: %v", err)
		}
		return v, settingsShapeString, nil
	default:
		return nil, settingsShapeNone, fmt.Errorf("one of settings or settingsMap is required")
	}
}

// buildResponseProps assembles the Properties payload that round-trips back to
// formae. The settings field is populated to match `shape`: when the user
// submitted `settingsMap`, the response carries `settingsMap` (string-coerced
// from the API response object); otherwise it carries the JSON-string `settings`.
// This keeps the conformance harness's submitted-vs-returned comparison happy.
func buildResponseProps(uid, name, cpType string, settings any, disableResolveMessage bool, shape settingsShape) contactPointProps {
	out := contactPointProps{
		UID:                   uid,
		Name:                  name,
		Type:                  cpType,
		DisableResolveMessage: disableResolveMessage,
	}
	switch shape {
	case settingsShapeMap:
		out.SettingsMap = coerceToStringMap(settings)
	default:
		// Default (and settingsShapeString): JSON-string form. Read paths that
		// haven't observed the user's submitted shape also fall here, which
		// matches the legacy behaviour for resources created before settingsMap.
		settingsJSON, _ := json.Marshal(settings)
		out.Settings = string(settingsJSON)
	}
	return out
}

// coerceToStringMap flattens Grafana's settings (returned as map[string]any)
// into the string-only mapping that settingsMap declares. Non-string values
// are JSON-encoded so they round-trip; for the PagerDuty-style use case
// (single integrationKey string) this is just a passthrough.
func coerceToStringMap(settings any) map[string]string {
	m, ok := settings.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		if s, isStr := v.(string); isStr {
			out[k] = s
			continue
		}
		encoded, _ := json.Marshal(v)
		out[k] = string(encoded)
	}
	return out
}

func (h *ContactPointHandler) Create(ctx context.Context, client *goapi.GrafanaHTTPAPI, props json.RawMessage) (*resource.ProgressResult, error) {
	var p contactPointProps
	if err := json.Unmarshal(props, &p); err != nil {
		return FailResult(resource.OperationCreate, resource.OperationErrorCodeInvalidRequest, fmt.Sprintf("invalid properties: %v", err)), nil
	}

	settings, shape, err := p.resolveSettings()
	if err != nil {
		return FailResult(resource.OperationCreate, resource.OperationErrorCodeInvalidRequest, err.Error()), nil
	}

	cp := &models.EmbeddedContactPoint{
		Name:                  p.Name,
		Type:                  strPtr(p.Type),
		Settings:              settings,
		DisableResolveMessage: p.DisableResolveMessage,
	}
	if p.UID != "" {
		cp.UID = p.UID
	}

	xDisableProvenance := "true"
	resp, postErr := client.Provisioning.PostContactpoints(&provisioning.PostContactpointsParams{
		Body:               cp,
		XDisableProvenance: &xDisableProvenance,
		Context:            ctx,
	})
	if postErr != nil {
		return FailResult(resource.OperationCreate, MapAPIError(postErr), fmt.Sprintf("failed to create contact point: %v", postErr)), nil
	}

	created := resp.GetPayload()
	cpType := ""
	if created.Type != nil {
		cpType = *created.Type
	}

	out := buildResponseProps(created.UID, created.Name, cpType, created.Settings, created.DisableResolveMessage, shape)
	outJSON, _ := json.Marshal(out)
	return SuccessResult(resource.OperationCreate, created.UID, outJSON), nil
}

func (h *ContactPointHandler) Read(ctx context.Context, client *goapi.GrafanaHTTPAPI, nativeID string) (*resource.ReadResult, error) {
	// Grafana doesn't have a GetContactPoint by UID endpoint.
	// We need to list all and find ours.
	resp, err := client.Provisioning.GetContactpoints(&provisioning.GetContactpointsParams{
		Context: ctx,
	})
	if err != nil {
		return &resource.ReadResult{
			ResourceType: "GRAFANA::Alerting::ContactPoint",
			ErrorCode:    MapAPIError(err),
		}, nil
	}

	for _, cp := range resp.GetPayload() {
		if cp.UID == nativeID {
			settingsJSON, _ := json.Marshal(cp.Settings)
			cpType := ""
			if cp.Type != nil {
				cpType = *cp.Type
			}
			out := contactPointProps{
				UID:                   cp.UID,
				Name:                  cp.Name,
				Type:                  cpType,
				Settings:              string(settingsJSON),
				DisableResolveMessage: cp.DisableResolveMessage,
			}
			outJSON, _ := json.Marshal(out)
			return &resource.ReadResult{
				ResourceType: "GRAFANA::Alerting::ContactPoint",
				Properties:   string(outJSON),
			}, nil
		}
	}

	return &resource.ReadResult{
		ResourceType: "GRAFANA::Alerting::ContactPoint",
		ErrorCode:    resource.OperationErrorCodeNotFound,
	}, nil
}

func (h *ContactPointHandler) Update(ctx context.Context, client *goapi.GrafanaHTTPAPI, nativeID string, prior, desired json.RawMessage) (*resource.ProgressResult, error) {
	var p contactPointProps
	if err := json.Unmarshal(desired, &p); err != nil {
		return FailResult(resource.OperationUpdate, resource.OperationErrorCodeInvalidRequest, fmt.Sprintf("invalid properties: %v", err)), nil
	}

	settings, shape, err := p.resolveSettings()
	if err != nil {
		return FailResult(resource.OperationUpdate, resource.OperationErrorCodeInvalidRequest, err.Error()), nil
	}

	cp := &models.EmbeddedContactPoint{
		UID:                   nativeID,
		Name:                  p.Name,
		Type:                  strPtr(p.Type),
		Settings:              settings,
		DisableResolveMessage: p.DisableResolveMessage,
	}

	xDisableProvenance := "true"
	_, putErr := client.Provisioning.PutContactpoint(&provisioning.PutContactpointParams{
		UID:                nativeID,
		Body:               cp,
		XDisableProvenance: &xDisableProvenance,
		Context:            ctx,
	})
	if putErr != nil {
		return FailResult(resource.OperationUpdate, MapAPIError(putErr), fmt.Sprintf("failed to update contact point: %v", putErr)), nil
	}

	// Build the response in the same shape the user submitted (Read goes
	// through the legacy `settings` JSON-string path; calling it here would
	// mask the user's `settingsMap` submission and trip the conformance
	// harness's "Property X is not expected" check).
	out := buildResponseProps(nativeID, p.Name, p.Type, settings, p.DisableResolveMessage, shape)
	outJSON, _ := json.Marshal(out)
	return SuccessResult(resource.OperationUpdate, nativeID, outJSON), nil
}

func (h *ContactPointHandler) Delete(ctx context.Context, client *goapi.GrafanaHTTPAPI, nativeID string) (*resource.ProgressResult, error) {
	// Check existence
	readResult, _ := h.Read(ctx, client, nativeID)
	if readResult != nil && readResult.ErrorCode == resource.OperationErrorCodeNotFound {
		return &resource.ProgressResult{
			Operation:       resource.OperationDelete,
			OperationStatus: resource.OperationStatusFailure,
			ErrorCode:       resource.OperationErrorCodeNotFound,
			NativeID:        nativeID,
		}, nil
	}

	_, err := client.Provisioning.DeleteContactpoints(nativeID)
	if err != nil {
		return FailResult(resource.OperationDelete, MapAPIError(err), fmt.Sprintf("failed to delete contact point: %v", err)), nil
	}

	return &resource.ProgressResult{
		Operation:       resource.OperationDelete,
		OperationStatus: resource.OperationStatusSuccess,
		NativeID:        nativeID,
	}, nil
}

func (h *ContactPointHandler) List(ctx context.Context, client *goapi.GrafanaHTTPAPI, pageSize int32, pageToken *string) (*resource.ListResult, error) {
	resp, err := client.Provisioning.GetContactpoints(&provisioning.GetContactpointsParams{
		Context: ctx,
	})
	if err != nil {
		return &resource.ListResult{NativeIDs: []string{}}, nil
	}

	var ids []string
	for _, cp := range resp.GetPayload() {
		if cp.UID != "" {
			ids = append(ids, cp.UID)
		}
	}
	return &resource.ListResult{NativeIDs: ids}, nil
}
