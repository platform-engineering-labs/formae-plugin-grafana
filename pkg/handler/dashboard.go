package handler

import (
	"context"
	"encoding/json"
	"fmt"

	goapi "github.com/grafana/grafana-openapi-client-go/client"
	"github.com/grafana/grafana-openapi-client-go/client/search"
	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

func init() {
	Register("Grafana::Core::Dashboard", &DashboardHandler{})
}

// DashboardHandler implements CRUD+List for Grafana dashboards.
type DashboardHandler struct{}

type dashboardProps struct {
	UID        string `json:"uid,omitempty"`
	Title      string `json:"title"`
	FolderUID  string `json:"folderUid,omitempty"`
	ConfigJSON string `json:"configJson"`
	Message    string `json:"message,omitempty"`
}

func parseDashboardJSON(configJSON string) (map[string]interface{}, error) {
	var dashboard map[string]interface{}
	if err := json.Unmarshal([]byte(configJSON), &dashboard); err != nil {
		return nil, fmt.Errorf("invalid dashboard JSON: %w", err)
	}
	return dashboard, nil
}

func (h *DashboardHandler) Create(ctx context.Context, client *goapi.GrafanaHTTPAPI, props json.RawMessage) (*resource.ProgressResult, error) {
	var p dashboardProps
	if err := json.Unmarshal(props, &p); err != nil {
		return FailResult(resource.OperationCreate, resource.OperationErrorCodeInvalidRequest, fmt.Sprintf("invalid properties: %v", err)), nil
	}

	dashboard, err := parseDashboardJSON(p.ConfigJSON)
	if err != nil {
		return FailResult(resource.OperationCreate, resource.OperationErrorCodeInvalidRequest, err.Error()), nil
	}

	// Inject uid and title from top-level properties
	if p.UID != "" {
		dashboard["uid"] = p.UID
	}
	dashboard["title"] = p.Title
	// Ensure id is null for creation
	dashboard["id"] = nil

	cmd := &models.SaveDashboardCommand{
		Dashboard: dashboard,
		FolderUID: p.FolderUID,
		Overwrite: false,
		Message:   p.Message,
	}

	resp, err := client.Dashboards.PostDashboard(cmd)
	if err != nil {
		return FailResult(resource.OperationCreate, MapAPIError(err), fmt.Sprintf("failed to create dashboard: %v", err)), nil
	}

	body := resp.GetPayload()
	uid := ""
	if body.UID != nil {
		uid = *body.UID
	}

	// Read back the full dashboard to get complete properties
	readResult, err := h.Read(ctx, client, uid)
	if err != nil || readResult.ErrorCode != "" {
		// Return with what we have
		out := dashboardProps{
			UID:        uid,
			Title:      p.Title,
			FolderUID:  p.FolderUID,
			ConfigJSON: p.ConfigJSON,
		}
		outJSON, _ := json.Marshal(out)
		return SuccessResult(resource.OperationCreate, uid, outJSON), nil
	}

	return SuccessResult(resource.OperationCreate, uid, json.RawMessage(readResult.Properties)), nil
}

func (h *DashboardHandler) Read(ctx context.Context, client *goapi.GrafanaHTTPAPI, nativeID string) (*resource.ReadResult, error) {
	resp, err := client.Dashboards.GetDashboardByUID(nativeID)
	if err != nil {
		code := MapAPIError(err)
		if code == resource.OperationErrorCodeNotFound {
			return &resource.ReadResult{
				ResourceType: "Grafana::Core::Dashboard",
				ErrorCode:    resource.OperationErrorCodeNotFound,
			}, nil
		}
		return &resource.ReadResult{
			ResourceType: "Grafana::Core::Dashboard",
			ErrorCode:    code,
		}, nil
	}

	full := resp.GetPayload()

	// Extract uid and title from the dashboard model, then strip
	// server-managed fields so configJson only contains user config.
	dashboardMap, _ := full.Dashboard.(map[string]interface{})
	uid := nativeID
	title := ""
	if dashboardMap != nil {
		if u, ok := dashboardMap["uid"].(string); ok {
			uid = u
		}
		if t, ok := dashboardMap["title"].(string); ok {
			title = t
		}
		// Remove server-managed fields from the dashboard model
		delete(dashboardMap, "id")
		delete(dashboardMap, "uid")
		delete(dashboardMap, "title")
		delete(dashboardMap, "version")
	}
	dashboardJSON, _ := json.Marshal(full.Dashboard)

	folderUID := ""
	if full.Meta != nil {
		folderUID = full.Meta.FolderUID
	}

	out := dashboardProps{
		UID:        uid,
		Title:      title,
		FolderUID:  folderUID,
		ConfigJSON: string(dashboardJSON),
	}
	outJSON, _ := json.Marshal(out)
	return &resource.ReadResult{
		ResourceType: "Grafana::Core::Dashboard",
		Properties:   string(outJSON),
	}, nil
}

func (h *DashboardHandler) Update(ctx context.Context, client *goapi.GrafanaHTTPAPI, nativeID string, prior, desired json.RawMessage) (*resource.ProgressResult, error) {
	var desiredProps dashboardProps
	if err := json.Unmarshal(desired, &desiredProps); err != nil {
		return FailResult(resource.OperationUpdate, resource.OperationErrorCodeInvalidRequest, fmt.Sprintf("invalid properties: %v", err)), nil
	}

	dashboard, err := parseDashboardJSON(desiredProps.ConfigJSON)
	if err != nil {
		return FailResult(resource.OperationUpdate, resource.OperationErrorCodeInvalidRequest, err.Error()), nil
	}

	dashboard["uid"] = nativeID
	dashboard["title"] = desiredProps.Title
	// Set id to null so Grafana uses the uid
	dashboard["id"] = nil

	cmd := &models.SaveDashboardCommand{
		Dashboard: dashboard,
		FolderUID: desiredProps.FolderUID,
		Overwrite: true,
		Message:   desiredProps.Message,
	}

	_, err = client.Dashboards.PostDashboard(cmd)
	if err != nil {
		return FailResult(resource.OperationUpdate, MapAPIError(err), fmt.Sprintf("failed to update dashboard: %v", err)), nil
	}

	// Read back the full state
	readResult, readErr := h.Read(ctx, client, nativeID)
	if readErr != nil || readResult.ErrorCode != "" {
		outJSON, _ := json.Marshal(desiredProps)
		return SuccessResult(resource.OperationUpdate, nativeID, outJSON), nil
	}

	return SuccessResult(resource.OperationUpdate, nativeID, json.RawMessage(readResult.Properties)), nil
}

func (h *DashboardHandler) Delete(ctx context.Context, client *goapi.GrafanaHTTPAPI, nativeID string) (*resource.ProgressResult, error) {
	_, err := client.Dashboards.GetDashboardByUID(nativeID)
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
		return FailResult(resource.OperationDelete, code, fmt.Sprintf("failed to check dashboard existence: %v", err)), nil
	}

	_, err = client.Dashboards.DeleteDashboardByUID(nativeID)
	if err != nil {
		return FailResult(resource.OperationDelete, MapAPIError(err), fmt.Sprintf("failed to delete dashboard: %v", err)), nil
	}

	return &resource.ProgressResult{
		Operation:       resource.OperationDelete,
		OperationStatus: resource.OperationStatusSuccess,
		NativeID:        nativeID,
	}, nil
}

func (h *DashboardHandler) List(ctx context.Context, client *goapi.GrafanaHTTPAPI, pageSize int32, pageToken *string) (*resource.ListResult, error) {
	dashType := "dash-db"
	resp, err := client.Search.Search(&search.SearchParams{
		Type:    &dashType,
		Context: ctx,
	})
	if err != nil {
		return &resource.ListResult{NativeIDs: []string{}}, nil
	}

	var ids []string
	for _, hit := range resp.GetPayload() {
		if string(hit.Type) == "dash-db" && hit.UID != "" {
			ids = append(ids, hit.UID)
		}
	}
	return &resource.ListResult{NativeIDs: ids}, nil
}
