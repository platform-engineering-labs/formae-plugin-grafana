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

// resolveSettings returns the type-specific settings object Grafana expects.
// `settingsMap` is submission-only sugar that lets each value carry a
// `formae.Resolvable` for cross-plugin wiring; once the Resolvable substitution
// runs upstream, the map collapses into the same shape `settings` produces.
// All persisted state is the JSON-string `settings` form, so Read and Sync see
// one canonical representation regardless of which form the user submitted.
// Returns a validation error when both forms are present or both are absent.
func (p contactPointProps) resolveSettings() (any, error) {
	hasMap := len(p.SettingsMap) > 0
	hasString := p.Settings != ""
	switch {
	case hasMap && hasString:
		return nil, fmt.Errorf("set exactly one of settings or settingsMap, not both")
	case hasMap:
		out := make(map[string]any, len(p.SettingsMap))
		for k, v := range p.SettingsMap {
			out[k] = v
		}
		return out, nil
	case hasString:
		var v any
		if err := json.Unmarshal([]byte(p.Settings), &v); err != nil {
			return nil, fmt.Errorf("invalid settings JSON: %v", err)
		}
		return v, nil
	default:
		return nil, fmt.Errorf("one of settings or settingsMap is required")
	}
}

// buildResponseProps assembles the Properties payload that round-trips back to
// formae. `settings` is the canonical persisted shape — even when the user
// submitted via `settingsMap`. The schema marks `settings` as
// hasProviderDefault so the conformance harness tolerates its appearance in
// actual when only `settingsMap` was declared in source.
func buildResponseProps(uid, name, cpType string, settings any, disableResolveMessage bool) contactPointProps {
	settingsJSON, _ := json.Marshal(settings)
	return contactPointProps{
		UID:                   uid,
		Name:                  name,
		Type:                  cpType,
		Settings:              string(settingsJSON),
		DisableResolveMessage: disableResolveMessage,
	}
}

func (h *ContactPointHandler) Create(ctx context.Context, client *goapi.GrafanaHTTPAPI, props json.RawMessage) (*resource.ProgressResult, error) {
	var p contactPointProps
	if err := json.Unmarshal(props, &p); err != nil {
		return FailResult(resource.OperationCreate, resource.OperationErrorCodeInvalidRequest, fmt.Sprintf("invalid properties: %v", err)), nil
	}

	settings, err := p.resolveSettings()
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

	out := buildResponseProps(created.UID, created.Name, cpType, created.Settings, created.DisableResolveMessage)
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

	settings, err := p.resolveSettings()
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

	// Always echo `settings` (the canonical persisted shape) regardless of
	// whether the user submitted via settings or settingsMap. The schema's
	// hasProviderDefault on `settings` tells the conformance harness this is
	// the provider-canonical form, so a settingsMap submission with a
	// settings response is tolerated.
	out := buildResponseProps(nativeID, p.Name, p.Type, settings, p.DisableResolveMessage)
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
