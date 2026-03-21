package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	goapi "github.com/grafana/grafana-openapi-client-go/client"
	"github.com/grafana/grafana-openapi-client-go/client/service_accounts"
	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

func init() {
	Register("Grafana::Core::ServiceAccount", &ServiceAccountHandler{})
}

// ServiceAccountHandler implements CRUD+List for Grafana service accounts.
type ServiceAccountHandler struct{}

type serviceAccountProps struct {
	ID         string `json:"id,omitempty"`
	Name       string `json:"name"`
	Role       string `json:"role,omitempty"`
	IsDisabled bool   `json:"isDisabled,omitempty"`
}

func (h *ServiceAccountHandler) Create(ctx context.Context, client *goapi.GrafanaHTTPAPI, props json.RawMessage) (*resource.ProgressResult, error) {
	var p serviceAccountProps
	if err := json.Unmarshal(props, &p); err != nil {
		return FailResult(resource.OperationCreate, resource.OperationErrorCodeInvalidRequest, fmt.Sprintf("invalid properties: %v", err)), nil
	}

	resp, err := client.ServiceAccounts.CreateServiceAccount(&service_accounts.CreateServiceAccountParams{
		Body: &models.CreateServiceAccountForm{
			Name:       p.Name,
			Role:       p.Role,
			IsDisabled: p.IsDisabled,
		},
		Context: ctx,
	})
	if err != nil {
		return FailResult(resource.OperationCreate, MapAPIError(err), fmt.Sprintf("failed to create service account: %v", err)), nil
	}

	sa := resp.GetPayload()
	saID := fmt.Sprintf("%d", sa.ID)

	readResult, readErr := h.Read(ctx, client, saID)
	if readErr != nil || readResult.ErrorCode != "" {
		out := serviceAccountProps{ID: saID, Name: p.Name, Role: p.Role, IsDisabled: p.IsDisabled}
		outJSON, _ := json.Marshal(out)
		return SuccessResult(resource.OperationCreate, saID, outJSON), nil
	}
	return SuccessResult(resource.OperationCreate, saID, json.RawMessage(readResult.Properties)), nil
}

func (h *ServiceAccountHandler) Read(ctx context.Context, client *goapi.GrafanaHTTPAPI, nativeID string) (*resource.ReadResult, error) {
	id, err := strconv.ParseInt(nativeID, 10, 64)
	if err != nil {
		return &resource.ReadResult{
			ResourceType: "Grafana::Core::ServiceAccount",
			ErrorCode:    resource.OperationErrorCodeInvalidRequest,
		}, nil
	}

	resp, err := client.ServiceAccounts.RetrieveServiceAccount(id)
	if err != nil {
		code := MapAPIError(err)
		if code == resource.OperationErrorCodeNotFound {
			return &resource.ReadResult{
				ResourceType: "Grafana::Core::ServiceAccount",
				ErrorCode:    resource.OperationErrorCodeNotFound,
			}, nil
		}
		return &resource.ReadResult{
			ResourceType: "Grafana::Core::ServiceAccount",
			ErrorCode:    code,
		}, nil
	}

	sa := resp.GetPayload()
	out := serviceAccountProps{
		ID:         fmt.Sprintf("%d", sa.ID),
		Name:       sa.Name,
		Role:       sa.Role,
		IsDisabled: sa.IsDisabled,
	}
	outJSON, _ := json.Marshal(out)
	return &resource.ReadResult{
		ResourceType: "Grafana::Core::ServiceAccount",
		Properties:   string(outJSON),
	}, nil
}

func (h *ServiceAccountHandler) Update(ctx context.Context, client *goapi.GrafanaHTTPAPI, nativeID string, prior, desired json.RawMessage) (*resource.ProgressResult, error) {
	var desiredProps serviceAccountProps
	if err := json.Unmarshal(desired, &desiredProps); err != nil {
		return FailResult(resource.OperationUpdate, resource.OperationErrorCodeInvalidRequest, fmt.Sprintf("invalid properties: %v", err)), nil
	}

	id, err := strconv.ParseInt(nativeID, 10, 64)
	if err != nil {
		return FailResult(resource.OperationUpdate, resource.OperationErrorCodeInvalidRequest, fmt.Sprintf("invalid service account ID: %v", err)), nil
	}

	isDisabled := desiredProps.IsDisabled
	_, err = client.ServiceAccounts.UpdateServiceAccount(&service_accounts.UpdateServiceAccountParams{
		ServiceAccountID: id,
		Body: &models.UpdateServiceAccountForm{
			Name:       desiredProps.Name,
			Role:       desiredProps.Role,
			IsDisabled: &isDisabled,
		},
		Context: ctx,
	})
	if err != nil {
		return FailResult(resource.OperationUpdate, MapAPIError(err), fmt.Sprintf("failed to update service account: %v", err)), nil
	}

	readResult, readErr := h.Read(ctx, client, nativeID)
	if readErr != nil || readResult.ErrorCode != "" {
		outJSON, _ := json.Marshal(desiredProps)
		return SuccessResult(resource.OperationUpdate, nativeID, outJSON), nil
	}
	return SuccessResult(resource.OperationUpdate, nativeID, json.RawMessage(readResult.Properties)), nil
}

func (h *ServiceAccountHandler) Delete(ctx context.Context, client *goapi.GrafanaHTTPAPI, nativeID string) (*resource.ProgressResult, error) {
	id, err := strconv.ParseInt(nativeID, 10, 64)
	if err != nil {
		return FailResult(resource.OperationDelete, resource.OperationErrorCodeInvalidRequest, fmt.Sprintf("invalid service account ID: %v", err)), nil
	}

	_, err = client.ServiceAccounts.RetrieveServiceAccount(id)
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
		return FailResult(resource.OperationDelete, code, fmt.Sprintf("failed to check service account existence: %v", err)), nil
	}

	_, err = client.ServiceAccounts.DeleteServiceAccount(id)
	if err != nil {
		return FailResult(resource.OperationDelete, MapAPIError(err), fmt.Sprintf("failed to delete service account: %v", err)), nil
	}

	return &resource.ProgressResult{
		Operation:       resource.OperationDelete,
		OperationStatus: resource.OperationStatusSuccess,
		NativeID:        nativeID,
	}, nil
}

func (h *ServiceAccountHandler) List(ctx context.Context, client *goapi.GrafanaHTTPAPI, pageSize int32, pageToken *string) (*resource.ListResult, error) {
	resp, err := client.ServiceAccounts.SearchOrgServiceAccountsWithPaging(&service_accounts.SearchOrgServiceAccountsWithPagingParams{
		Context: ctx,
	})
	if err != nil {
		return &resource.ListResult{NativeIDs: []string{}}, nil
	}

	var ids []string
	for _, sa := range resp.GetPayload().ServiceAccounts {
		if sa.ID > 0 {
			ids = append(ids, fmt.Sprintf("%d", sa.ID))
		}
	}
	return &resource.ListResult{NativeIDs: ids}, nil
}
