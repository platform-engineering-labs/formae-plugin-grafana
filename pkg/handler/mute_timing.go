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
	Register("Grafana::Alerting::MuteTiming", &MuteTimingHandler{})
}

// MuteTimingHandler implements CRUD+List for Grafana mute timings.
type MuteTimingHandler struct{}

type muteTimingProps struct {
	Name          string `json:"name"`
	TimeIntervals string `json:"timeIntervals"`
}

func (h *MuteTimingHandler) Create(ctx context.Context, client *goapi.GrafanaHTTPAPI, props json.RawMessage) (*resource.ProgressResult, error) {
	var p muteTimingProps
	if err := json.Unmarshal(props, &p); err != nil {
		return FailResult(resource.OperationCreate, resource.OperationErrorCodeInvalidRequest, fmt.Sprintf("invalid properties: %v", err)), nil
	}

	var timeIntervals []*models.TimeIntervalItem
	if err := json.Unmarshal([]byte(p.TimeIntervals), &timeIntervals); err != nil {
		return FailResult(resource.OperationCreate, resource.OperationErrorCodeInvalidRequest, fmt.Sprintf("invalid time intervals: %v", err)), nil
	}

	mt := &models.MuteTimeInterval{
		Name:          p.Name,
		TimeIntervals: timeIntervals,
	}

	xDisableProvenance := "true"
	resp, err := client.Provisioning.PostMuteTiming(&provisioning.PostMuteTimingParams{
		Body:               mt,
		XDisableProvenance: &xDisableProvenance,
		Context:            ctx,
	})
	if err != nil {
		return FailResult(resource.OperationCreate, MapAPIError(err), fmt.Sprintf("failed to create mute timing: %v", err)), nil
	}

	created := resp.GetPayload()
	intervalsJSON, _ := json.Marshal(created.TimeIntervals)
	out := muteTimingProps{
		Name:          created.Name,
		TimeIntervals: string(intervalsJSON),
	}
	outJSON, _ := json.Marshal(out)
	return SuccessResult(resource.OperationCreate, created.Name, outJSON), nil
}

func (h *MuteTimingHandler) Read(ctx context.Context, client *goapi.GrafanaHTTPAPI, nativeID string) (*resource.ReadResult, error) {
	resp, err := client.Provisioning.GetMuteTiming(nativeID)
	if err != nil {
		code := MapAPIError(err)
		if code == resource.OperationErrorCodeNotFound {
			return &resource.ReadResult{
				ResourceType: "Grafana::Alerting::MuteTiming",
				ErrorCode:    resource.OperationErrorCodeNotFound,
			}, nil
		}
		return &resource.ReadResult{
			ResourceType: "Grafana::Alerting::MuteTiming",
			ErrorCode:    code,
		}, nil
	}

	mt := resp.GetPayload()
	intervalsJSON, _ := json.Marshal(mt.TimeIntervals)
	out := muteTimingProps{
		Name:          mt.Name,
		TimeIntervals: string(intervalsJSON),
	}
	outJSON, _ := json.Marshal(out)
	return &resource.ReadResult{
		ResourceType: "Grafana::Alerting::MuteTiming",
		Properties:   string(outJSON),
	}, nil
}

func (h *MuteTimingHandler) Update(ctx context.Context, client *goapi.GrafanaHTTPAPI, nativeID string, prior, desired json.RawMessage) (*resource.ProgressResult, error) {
	var p muteTimingProps
	if err := json.Unmarshal(desired, &p); err != nil {
		return FailResult(resource.OperationUpdate, resource.OperationErrorCodeInvalidRequest, fmt.Sprintf("invalid properties: %v", err)), nil
	}

	var timeIntervals []*models.TimeIntervalItem
	if err := json.Unmarshal([]byte(p.TimeIntervals), &timeIntervals); err != nil {
		return FailResult(resource.OperationUpdate, resource.OperationErrorCodeInvalidRequest, fmt.Sprintf("invalid time intervals: %v", err)), nil
	}

	mt := &models.MuteTimeInterval{
		Name:          p.Name,
		TimeIntervals: timeIntervals,
	}

	xDisableProvenance := "true"
	resp, err := client.Provisioning.PutMuteTiming(&provisioning.PutMuteTimingParams{
		Name:               nativeID,
		Body:               mt,
		XDisableProvenance: &xDisableProvenance,
		Context:            ctx,
	})
	if err != nil {
		return FailResult(resource.OperationUpdate, MapAPIError(err), fmt.Sprintf("failed to update mute timing: %v", err)), nil
	}

	updated := resp.GetPayload()
	intervalsJSON, _ := json.Marshal(updated.TimeIntervals)
	out := muteTimingProps{
		Name:          updated.Name,
		TimeIntervals: string(intervalsJSON),
	}
	outJSON, _ := json.Marshal(out)
	return SuccessResult(resource.OperationUpdate, updated.Name, outJSON), nil
}

func (h *MuteTimingHandler) Delete(ctx context.Context, client *goapi.GrafanaHTTPAPI, nativeID string) (*resource.ProgressResult, error) {
	_, err := client.Provisioning.GetMuteTiming(nativeID)
	if err != nil {
		code := MapAPIError(err)
		if code == resource.OperationErrorCodeNotFound {
			return &resource.ProgressResult{
				Operation:       resource.OperationDelete,
				OperationStatus: resource.OperationStatusFailure,
				ErrorCode:       resource.OperationErrorCodeNotFound,
				NativeID:        nativeID,
			}, nil
		}
		return FailResult(resource.OperationDelete, code, fmt.Sprintf("failed to check mute timing existence: %v", err)), nil
	}

	_, err = client.Provisioning.DeleteMuteTiming(&provisioning.DeleteMuteTimingParams{
		Name:    nativeID,
		Context: ctx,
	})
	if err != nil {
		return FailResult(resource.OperationDelete, MapAPIError(err), fmt.Sprintf("failed to delete mute timing: %v", err)), nil
	}

	return &resource.ProgressResult{
		Operation:       resource.OperationDelete,
		OperationStatus: resource.OperationStatusSuccess,
		NativeID:        nativeID,
	}, nil
}

func (h *MuteTimingHandler) List(ctx context.Context, client *goapi.GrafanaHTTPAPI, pageSize int32, pageToken *string) (*resource.ListResult, error) {
	resp, err := client.Provisioning.GetMuteTimings()
	if err != nil {
		return &resource.ListResult{NativeIDs: []string{}}, nil
	}

	var ids []string
	for _, mt := range resp.GetPayload() {
		if mt.Name != "" {
			ids = append(ids, mt.Name)
		}
	}
	return &resource.ListResult{NativeIDs: ids}, nil
}
