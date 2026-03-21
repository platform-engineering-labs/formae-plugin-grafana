package handler

import (
	"context"
	"encoding/json"
	"fmt"

	goapi "github.com/grafana/grafana-openapi-client-go/client"
	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

func init() {
	Register("Grafana::Core::DataSource", &DataSourceHandler{})
}

// DataSourceHandler implements CRUD+List for Grafana data sources.
type DataSourceHandler struct{}

type dataSourceProps struct {
	UID            string `json:"uid,omitempty"`
	Name           string `json:"name"`
	Type           string `json:"datasourceType"`
	URL            string `json:"url,omitempty"`
	Access         string `json:"access,omitempty"`
	IsDefault      bool   `json:"isDefault,omitempty"`
	BasicAuth      bool   `json:"basicAuth,omitempty"`
	BasicAuthUser  string `json:"basicAuthUser,omitempty"`
	JSONData       string `json:"jsonData,omitempty"`
	SecureJSONData string `json:"secureJsonData,omitempty"`
}

func (h *DataSourceHandler) Create(ctx context.Context, client *goapi.GrafanaHTTPAPI, props json.RawMessage) (*resource.ProgressResult, error) {
	var p dataSourceProps
	if err := json.Unmarshal(props, &p); err != nil {
		return FailResult(resource.OperationCreate, resource.OperationErrorCodeInvalidRequest, fmt.Sprintf("invalid properties: %v", err)), nil
	}

	cmd := &models.AddDataSourceCommand{
		Name:      p.Name,
		Type:      p.Type,
		URL:       p.URL,
		Access:    models.DsAccess(p.Access),
		UID:       p.UID,
		IsDefault: p.IsDefault,
		BasicAuth: p.BasicAuth,
	}

	if p.BasicAuthUser != "" {
		cmd.BasicAuthUser = p.BasicAuthUser
	}

	if p.JSONData != "" {
		var jsonData map[string]interface{}
		if err := json.Unmarshal([]byte(p.JSONData), &jsonData); err == nil {
			cmd.JSONData = jsonData
		}
	}

	if p.SecureJSONData != "" {
		var secureData map[string]string
		if err := json.Unmarshal([]byte(p.SecureJSONData), &secureData); err == nil {
			cmd.SecureJSONData = secureData
		}
	}

	resp, err := client.Datasources.AddDataSource(cmd)
	if err != nil {
		return FailResult(resource.OperationCreate, MapAPIError(err), fmt.Sprintf("failed to create data source: %v", err)), nil
	}

	ds := resp.GetPayload().Datasource
	uid := ""
	if ds != nil {
		uid = ds.UID
	}

	// Read back to get full state
	readResult, readErr := h.Read(ctx, client, uid)
	if readErr != nil || readResult.ErrorCode != "" {
		out := dataSourceProps{
			UID:  uid,
			Name: p.Name,
			Type: p.Type,
			URL:  p.URL,
		}
		outJSON, _ := json.Marshal(out)
		return SuccessResult(resource.OperationCreate, uid, outJSON), nil
	}
	return SuccessResult(resource.OperationCreate, uid, json.RawMessage(readResult.Properties)), nil
}

func (h *DataSourceHandler) Read(ctx context.Context, client *goapi.GrafanaHTTPAPI, nativeID string) (*resource.ReadResult, error) {
	resp, err := client.Datasources.GetDataSourceByUID(nativeID)
	if err != nil {
		code := MapAPIError(err)
		if code == resource.OperationErrorCodeNotFound {
			return &resource.ReadResult{
				ResourceType: "Grafana::Core::DataSource",
				ErrorCode:    resource.OperationErrorCodeNotFound,
			}, nil
		}
		return &resource.ReadResult{
			ResourceType: "Grafana::Core::DataSource",
			ErrorCode:    code,
		}, nil
	}

	ds := resp.GetPayload()
	var jsonDataStr string
	if ds.JSONData != nil {
		jsonDataBytes, _ := json.Marshal(ds.JSONData)
		jsonDataStr = string(jsonDataBytes)
	}

	out := dataSourceProps{
		UID:           ds.UID,
		Name:          ds.Name,
		Type:          ds.Type,
		URL:           ds.URL,
		Access:        string(ds.Access),
		IsDefault:     ds.IsDefault,
		BasicAuth:     ds.BasicAuth,
		BasicAuthUser: ds.BasicAuthUser,
		JSONData:      jsonDataStr,
		// SecureJSONData is never returned by the API (write-only)
	}
	outJSON, _ := json.Marshal(out)
	return &resource.ReadResult{
		ResourceType: "Grafana::Core::DataSource",
		Properties:   string(outJSON),
	}, nil
}

func (h *DataSourceHandler) Update(ctx context.Context, client *goapi.GrafanaHTTPAPI, nativeID string, prior, desired json.RawMessage) (*resource.ProgressResult, error) {
	var desiredProps dataSourceProps
	if err := json.Unmarshal(desired, &desiredProps); err != nil {
		return FailResult(resource.OperationUpdate, resource.OperationErrorCodeInvalidRequest, fmt.Sprintf("invalid properties: %v", err)), nil
	}

	cmd := &models.UpdateDataSourceCommand{
		Name:      desiredProps.Name,
		Type:      desiredProps.Type,
		URL:       desiredProps.URL,
		Access:    models.DsAccess(desiredProps.Access),
		IsDefault: desiredProps.IsDefault,
		BasicAuth: desiredProps.BasicAuth,
	}

	if desiredProps.BasicAuthUser != "" {
		cmd.BasicAuthUser = desiredProps.BasicAuthUser
	}

	if desiredProps.JSONData != "" {
		var jsonData map[string]interface{}
		if err := json.Unmarshal([]byte(desiredProps.JSONData), &jsonData); err == nil {
			cmd.JSONData = jsonData
		}
	}

	if desiredProps.SecureJSONData != "" {
		var secureData map[string]string
		if err := json.Unmarshal([]byte(desiredProps.SecureJSONData), &secureData); err == nil {
			cmd.SecureJSONData = secureData
		}
	}

	_, err := client.Datasources.UpdateDataSourceByUID(nativeID, cmd)
	if err != nil {
		return FailResult(resource.OperationUpdate, MapAPIError(err), fmt.Sprintf("failed to update data source: %v", err)), nil
	}

	// Read back full state
	readResult, readErr := h.Read(ctx, client, nativeID)
	if readErr != nil || readResult.ErrorCode != "" {
		outJSON, _ := json.Marshal(desiredProps)
		return SuccessResult(resource.OperationUpdate, nativeID, outJSON), nil
	}
	return SuccessResult(resource.OperationUpdate, nativeID, json.RawMessage(readResult.Properties)), nil
}

func (h *DataSourceHandler) Delete(ctx context.Context, client *goapi.GrafanaHTTPAPI, nativeID string) (*resource.ProgressResult, error) {
	_, err := client.Datasources.GetDataSourceByUID(nativeID)
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
		return FailResult(resource.OperationDelete, code, fmt.Sprintf("failed to check data source existence: %v", err)), nil
	}

	_, err = client.Datasources.DeleteDataSourceByUID(nativeID)
	if err != nil {
		return FailResult(resource.OperationDelete, MapAPIError(err), fmt.Sprintf("failed to delete data source: %v", err)), nil
	}

	return &resource.ProgressResult{
		Operation:       resource.OperationDelete,
		OperationStatus: resource.OperationStatusSuccess,
		NativeID:        nativeID,
	}, nil
}

func (h *DataSourceHandler) List(ctx context.Context, client *goapi.GrafanaHTTPAPI, pageSize int32, pageToken *string) (*resource.ListResult, error) {
	resp, err := client.Datasources.GetDataSources()
	if err != nil {
		return &resource.ListResult{NativeIDs: []string{}}, nil
	}

	var ids []string
	for _, ds := range resp.GetPayload() {
		if ds.UID != "" {
			ids = append(ids, ds.UID)
		}
	}
	return &resource.ListResult{NativeIDs: ids}, nil
}
