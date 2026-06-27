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
	Register("GRAFANA::Core::Dashboard", &DashboardHandler{})
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

// serverAssignedCounters are top-level dashboard-model keys Grafana assigns and
// increments on every save/reload. Tracking them makes identical content drift
// on each reconcile, so they are stripped from configJson on Read.
var serverAssignedCounters = []string{"id", "version", "iteration"}

// promotedToResourceProps are top-level keys formae surfaces as dedicated
// dashboardProps fields, so they must not be duplicated inside configJson.
var promotedToResourceProps = []string{"uid", "title"}

// dashboardModelToProps converts a Grafana dashboard model plus its metadata
// into the recorded dashboardProps. It surfaces uid/title as dedicated fields,
// strips the server-assigned counters and promoted keys from the model, and
// marshals the remainder into configJson. nativeID is the fallback uid when the
// model carries none.
//
// Stripping is top-level only — map-key deletion never recurses into panels[],
// whose elements carry their own user-authored id. Mutating the model map is
// intentional: it is used only for the subsequent marshal here, and full.Meta
// is read separately.
func dashboardModelToProps(model interface{}, meta *models.DashboardMeta, nativeID string) dashboardProps {
	dashboardMap, _ := model.(map[string]interface{})
	uid := nativeID
	title := ""
	if dashboardMap != nil {
		if u, ok := dashboardMap["uid"].(string); ok {
			uid = u
		}
		if t, ok := dashboardMap["title"].(string); ok {
			title = t
		}
		for _, k := range serverAssignedCounters {
			delete(dashboardMap, k)
		}
		for _, k := range promotedToResourceProps {
			delete(dashboardMap, k)
		}
	}
	dashboardJSON, _ := json.Marshal(model)

	folderUID := ""
	if meta != nil {
		folderUID = meta.FolderUID
	}

	return dashboardProps{
		UID:        uid,
		Title:      title,
		FolderUID:  folderUID,
		ConfigJSON: string(dashboardJSON),
	}
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
		Overwrite: true, // Upsert: create or update if already exists
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
				ResourceType: "GRAFANA::Core::Dashboard",
				ErrorCode:    resource.OperationErrorCodeNotFound,
			}, nil
		}
		return &resource.ReadResult{
			ResourceType: "GRAFANA::Core::Dashboard",
			ErrorCode:    code,
		}, nil
	}

	full := resp.GetPayload()

	out := dashboardModelToProps(full.Dashboard, full.Meta, nativeID)
	outJSON, _ := json.Marshal(out)
	return &resource.ReadResult{
		ResourceType: "GRAFANA::Core::Dashboard",
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
