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
	Register("Grafana::Alerting::MessageTemplate", &MessageTemplateHandler{})
}

// MessageTemplateHandler implements CRUD+List for Grafana message templates.
type MessageTemplateHandler struct{}

type messageTemplateProps struct {
	Name     string `json:"name"`
	Template string `json:"template"`
}

func (h *MessageTemplateHandler) Create(ctx context.Context, client *goapi.GrafanaHTTPAPI, props json.RawMessage) (*resource.ProgressResult, error) {
	var p messageTemplateProps
	if err := json.Unmarshal(props, &p); err != nil {
		return FailResult(resource.OperationCreate, resource.OperationErrorCodeInvalidRequest, fmt.Sprintf("invalid properties: %v", err)), nil
	}

	// Grafana only has PUT for templates (create-or-update)
	xDisableProvenance := "true"
	resp, err := client.Provisioning.PutTemplate(&provisioning.PutTemplateParams{
		Name: p.Name,
		Body: &models.NotificationTemplateContent{
			Template: p.Template,
		},
		XDisableProvenance: &xDisableProvenance,
		Context:            ctx,
	})
	if err != nil {
		return FailResult(resource.OperationCreate, MapAPIError(err), fmt.Sprintf("failed to create message template: %v", err)), nil
	}

	created := resp.GetPayload()
	out := messageTemplateProps{
		Name:     created.Name,
		Template: created.Template,
	}
	outJSON, _ := json.Marshal(out)
	return SuccessResult(resource.OperationCreate, created.Name, outJSON), nil
}

func (h *MessageTemplateHandler) Read(ctx context.Context, client *goapi.GrafanaHTTPAPI, nativeID string) (*resource.ReadResult, error) {
	resp, err := client.Provisioning.GetTemplate(nativeID)
	if err != nil {
		code := MapAPIError(err)
		if code == resource.OperationErrorCodeNotFound {
			return &resource.ReadResult{
				ResourceType: "Grafana::Alerting::MessageTemplate",
				ErrorCode:    resource.OperationErrorCodeNotFound,
			}, nil
		}
		return &resource.ReadResult{
			ResourceType: "Grafana::Alerting::MessageTemplate",
			ErrorCode:    code,
		}, nil
	}

	tmpl := resp.GetPayload()
	out := messageTemplateProps{
		Name:     tmpl.Name,
		Template: tmpl.Template,
	}
	outJSON, _ := json.Marshal(out)
	return &resource.ReadResult{
		ResourceType: "Grafana::Alerting::MessageTemplate",
		Properties:   string(outJSON),
	}, nil
}

func (h *MessageTemplateHandler) Update(ctx context.Context, client *goapi.GrafanaHTTPAPI, nativeID string, prior, desired json.RawMessage) (*resource.ProgressResult, error) {
	var p messageTemplateProps
	if err := json.Unmarshal(desired, &p); err != nil {
		return FailResult(resource.OperationUpdate, resource.OperationErrorCodeInvalidRequest, fmt.Sprintf("invalid properties: %v", err)), nil
	}

	xDisableProvenance := "true"
	resp, err := client.Provisioning.PutTemplate(&provisioning.PutTemplateParams{
		Name: nativeID,
		Body: &models.NotificationTemplateContent{
			Template: p.Template,
		},
		XDisableProvenance: &xDisableProvenance,
		Context:            ctx,
	})
	if err != nil {
		return FailResult(resource.OperationUpdate, MapAPIError(err), fmt.Sprintf("failed to update message template: %v", err)), nil
	}

	updated := resp.GetPayload()
	out := messageTemplateProps{
		Name:     updated.Name,
		Template: updated.Template,
	}
	outJSON, _ := json.Marshal(out)
	return SuccessResult(resource.OperationUpdate, updated.Name, outJSON), nil
}

func (h *MessageTemplateHandler) Delete(ctx context.Context, client *goapi.GrafanaHTTPAPI, nativeID string) (*resource.ProgressResult, error) {
	_, err := client.Provisioning.GetTemplate(nativeID)
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
		return FailResult(resource.OperationDelete, code, fmt.Sprintf("failed to check message template existence: %v", err)), nil
	}

	_, err = client.Provisioning.DeleteTemplate(&provisioning.DeleteTemplateParams{
		Name:    nativeID,
		Context: ctx,
	})
	if err != nil {
		return FailResult(resource.OperationDelete, MapAPIError(err), fmt.Sprintf("failed to delete message template: %v", err)), nil
	}

	return &resource.ProgressResult{
		Operation:       resource.OperationDelete,
		OperationStatus: resource.OperationStatusSuccess,
		NativeID:        nativeID,
	}, nil
}

func (h *MessageTemplateHandler) List(ctx context.Context, client *goapi.GrafanaHTTPAPI, pageSize int32, pageToken *string) (*resource.ListResult, error) {
	resp, err := client.Provisioning.GetTemplates()
	if err != nil {
		return &resource.ListResult{NativeIDs: []string{}}, nil
	}

	var ids []string
	for _, tmpl := range resp.GetPayload() {
		if tmpl.Name != "" {
			ids = append(ids, tmpl.Name)
		}
	}
	return &resource.ListResult{NativeIDs: ids}, nil
}
