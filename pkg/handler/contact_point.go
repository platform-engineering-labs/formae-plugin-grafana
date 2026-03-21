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
	Register("Grafana::Alerting::ContactPoint", &ContactPointHandler{})
}

// ContactPointHandler implements CRUD+List for Grafana contact points.
type ContactPointHandler struct{}

type contactPointProps struct {
	UID                   string `json:"uid,omitempty"`
	Name                  string `json:"name"`
	Type                  string `json:"contactPointType"`
	Settings              string `json:"settings"`
	DisableResolveMessage bool   `json:"disableResolveMessage,omitempty"`
}

func (h *ContactPointHandler) Create(ctx context.Context, client *goapi.GrafanaHTTPAPI, props json.RawMessage) (*resource.ProgressResult, error) {
	var p contactPointProps
	if err := json.Unmarshal(props, &p); err != nil {
		return FailResult(resource.OperationCreate, resource.OperationErrorCodeInvalidRequest, fmt.Sprintf("invalid properties: %v", err)), nil
	}

	var settings any
	if err := json.Unmarshal([]byte(p.Settings), &settings); err != nil {
		return FailResult(resource.OperationCreate, resource.OperationErrorCodeInvalidRequest, fmt.Sprintf("invalid settings JSON: %v", err)), nil
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
	resp, err := client.Provisioning.PostContactpoints(&provisioning.PostContactpointsParams{
		Body:               cp,
		XDisableProvenance: &xDisableProvenance,
		Context:            ctx,
	})
	if err != nil {
		return FailResult(resource.OperationCreate, MapAPIError(err), fmt.Sprintf("failed to create contact point: %v", err)), nil
	}

	created := resp.GetPayload()
	settingsJSON, _ := json.Marshal(created.Settings)

	cpType := ""
	if created.Type != nil {
		cpType = *created.Type
	}

	out := contactPointProps{
		UID:                   created.UID,
		Name:                  created.Name,
		Type:                  cpType,
		Settings:              string(settingsJSON),
		DisableResolveMessage: created.DisableResolveMessage,
	}
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
			ResourceType: "Grafana::Alerting::ContactPoint",
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
				ResourceType: "Grafana::Alerting::ContactPoint",
				Properties:   string(outJSON),
			}, nil
		}
	}

	return &resource.ReadResult{
		ResourceType: "Grafana::Alerting::ContactPoint",
		ErrorCode:    resource.OperationErrorCodeNotFound,
	}, nil
}

func (h *ContactPointHandler) Update(ctx context.Context, client *goapi.GrafanaHTTPAPI, nativeID string, prior, desired json.RawMessage) (*resource.ProgressResult, error) {
	var p contactPointProps
	if err := json.Unmarshal(desired, &p); err != nil {
		return FailResult(resource.OperationUpdate, resource.OperationErrorCodeInvalidRequest, fmt.Sprintf("invalid properties: %v", err)), nil
	}

	var settings any
	if err := json.Unmarshal([]byte(p.Settings), &settings); err != nil {
		return FailResult(resource.OperationUpdate, resource.OperationErrorCodeInvalidRequest, fmt.Sprintf("invalid settings JSON: %v", err)), nil
	}

	cp := &models.EmbeddedContactPoint{
		UID:                   nativeID,
		Name:                  p.Name,
		Type:                  strPtr(p.Type),
		Settings:              settings,
		DisableResolveMessage: p.DisableResolveMessage,
	}

	xDisableProvenance := "true"
	_, err := client.Provisioning.PutContactpoint(&provisioning.PutContactpointParams{
		UID:                nativeID,
		Body:               cp,
		XDisableProvenance: &xDisableProvenance,
		Context:            ctx,
	})
	if err != nil {
		return FailResult(resource.OperationUpdate, MapAPIError(err), fmt.Sprintf("failed to update contact point: %v", err)), nil
	}

	readResult, readErr := h.Read(ctx, client, nativeID)
	if readErr != nil || readResult.ErrorCode != "" {
		outJSON, _ := json.Marshal(p)
		return SuccessResult(resource.OperationUpdate, nativeID, outJSON), nil
	}
	return SuccessResult(resource.OperationUpdate, nativeID, json.RawMessage(readResult.Properties)), nil
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
