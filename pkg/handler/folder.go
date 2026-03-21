package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	goapi "github.com/grafana/grafana-openapi-client-go/client"
	"github.com/grafana/grafana-openapi-client-go/client/folders"
	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

func init() {
	Register("Grafana::Core::Folder", &FolderHandler{})
}

// FolderHandler implements CRUD+List for Grafana folders.
type FolderHandler struct{}

type folderProps struct {
	UID       string `json:"uid,omitempty"`
	Title     string `json:"title"`
	ParentUID string `json:"parentUid,omitempty"`
}

func (h *FolderHandler) Create(ctx context.Context, client *goapi.GrafanaHTTPAPI, props json.RawMessage) (*resource.ProgressResult, error) {
	var p folderProps
	if err := json.Unmarshal(props, &p); err != nil {
		return FailResult(resource.OperationCreate, resource.OperationErrorCodeInvalidRequest, fmt.Sprintf("invalid properties: %v", err)), nil
	}

	cmd := &models.CreateFolderCommand{
		Title: p.Title,
		UID:   p.UID,
	}
	if p.ParentUID != "" {
		cmd.ParentUID = p.ParentUID
	}

	resp, err := client.Folders.CreateFolder(cmd)
	if err != nil {
		code := MapAPIError(err)
		// Grafana returns 412 (Precondition Failed) when a folder with the same UID
		// already exists. Map this to AlreadyExists for proper handling.
		if strings.Contains(err.Error(), "412") || strings.Contains(err.Error(), "precondition") {
			code = resource.OperationErrorCodeAlreadyExists
		}
		return FailResult(resource.OperationCreate, code, fmt.Sprintf("failed to create folder: %v", err)), nil
	}

	folder := resp.GetPayload()
	out := folderProps{
		UID:       folder.UID,
		Title:     folder.Title,
		ParentUID: folder.ParentUID,
	}
	outJSON, _ := json.Marshal(out)
	return SuccessResult(resource.OperationCreate, folder.UID, outJSON), nil
}

func (h *FolderHandler) Read(ctx context.Context, client *goapi.GrafanaHTTPAPI, nativeID string) (*resource.ReadResult, error) {
	resp, err := client.Folders.GetFolderByUID(nativeID)
	if err != nil {
		code := MapAPIError(err)
		if code == resource.OperationErrorCodeNotFound {
			return &resource.ReadResult{
				ResourceType: "Grafana::Core::Folder",
				ErrorCode:    resource.OperationErrorCodeNotFound,
			}, nil
		}
		return &resource.ReadResult{
			ResourceType: "Grafana::Core::Folder",
			ErrorCode:    code,
		}, nil
	}

	folder := resp.GetPayload()
	out := folderProps{
		UID:       folder.UID,
		Title:     folder.Title,
		ParentUID: folder.ParentUID,
	}
	outJSON, _ := json.Marshal(out)
	return &resource.ReadResult{
		ResourceType: "Grafana::Core::Folder",
		Properties:   string(outJSON),
	}, nil
}

func (h *FolderHandler) Update(ctx context.Context, client *goapi.GrafanaHTTPAPI, nativeID string, prior, desired json.RawMessage) (*resource.ProgressResult, error) {
	var desiredProps folderProps
	if err := json.Unmarshal(desired, &desiredProps); err != nil {
		return FailResult(resource.OperationUpdate, resource.OperationErrorCodeInvalidRequest, fmt.Sprintf("invalid properties: %v", err)), nil
	}

	// Read current state to get the version for optimistic concurrency
	readResp, err := client.Folders.GetFolderByUID(nativeID)
	if err != nil {
		return FailResult(resource.OperationUpdate, MapAPIError(err), fmt.Sprintf("failed to read folder for update: %v", err)), nil
	}
	currentFolder := readResp.GetPayload()

	// Check if parent needs to change
	var priorProps folderProps
	_ = json.Unmarshal(prior, &priorProps)
	if priorProps.ParentUID != desiredProps.ParentUID {
		_, err := client.Folders.MoveFolder(nativeID, &models.MoveFolderCommand{
			ParentUID: desiredProps.ParentUID,
		})
		if err != nil {
			return FailResult(resource.OperationUpdate, MapAPIError(err), fmt.Sprintf("failed to move folder: %v", err)), nil
		}
	}

	// Update title
	resp, err := client.Folders.UpdateFolder(nativeID, &models.UpdateFolderCommand{
		Title:   desiredProps.Title,
		Version: currentFolder.Version,
	})
	if err != nil {
		return FailResult(resource.OperationUpdate, MapAPIError(err), fmt.Sprintf("failed to update folder: %v", err)), nil
	}

	folder := resp.GetPayload()
	out := folderProps{
		UID:       folder.UID,
		Title:     folder.Title,
		ParentUID: folder.ParentUID,
	}
	outJSON, _ := json.Marshal(out)
	return SuccessResult(resource.OperationUpdate, folder.UID, outJSON), nil
}

func (h *FolderHandler) Delete(ctx context.Context, client *goapi.GrafanaHTTPAPI, nativeID string) (*resource.ProgressResult, error) {
	// Check if exists first
	_, err := client.Folders.GetFolderByUID(nativeID)
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
		return FailResult(resource.OperationDelete, code, fmt.Sprintf("failed to check folder existence: %v", err)), nil
	}

	_, err = client.Folders.DeleteFolder(&folders.DeleteFolderParams{
		FolderUID: nativeID,
		Context:   ctx,
	})
	if err != nil {
		return FailResult(resource.OperationDelete, MapAPIError(err), fmt.Sprintf("failed to delete folder: %v", err)), nil
	}

	return &resource.ProgressResult{
		Operation:       resource.OperationDelete,
		OperationStatus: resource.OperationStatusSuccess,
		NativeID:        nativeID,
	}, nil
}

func (h *FolderHandler) List(ctx context.Context, client *goapi.GrafanaHTTPAPI, pageSize int32, pageToken *string) (*resource.ListResult, error) {
	resp, err := client.Folders.GetFolders(&folders.GetFoldersParams{
		Context: ctx,
	})
	if err != nil {
		return &resource.ListResult{NativeIDs: []string{}}, nil
	}

	var ids []string
	for _, f := range resp.GetPayload() {
		ids = append(ids, f.UID)
	}
	return &resource.ListResult{NativeIDs: ids}, nil
}
