package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	goapi "github.com/grafana/grafana-openapi-client-go/client"
	"github.com/grafana/grafana-openapi-client-go/client/teams"
	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

func init() {
	Register("Grafana::Core::Team", &TeamHandler{})
}

// TeamHandler implements CRUD+List for Grafana teams.
type TeamHandler struct{}

type teamProps struct {
	ID    string `json:"id,omitempty"`
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
}

func strPtr(s string) *string { return &s }

func (h *TeamHandler) Create(ctx context.Context, client *goapi.GrafanaHTTPAPI, props json.RawMessage) (*resource.ProgressResult, error) {
	var p teamProps
	if err := json.Unmarshal(props, &p); err != nil {
		return FailResult(resource.OperationCreate, resource.OperationErrorCodeInvalidRequest, fmt.Sprintf("invalid properties: %v", err)), nil
	}

	resp, err := client.Teams.CreateTeam(&models.CreateTeamCommand{
		Name:  strPtr(p.Name),
		Email: p.Email,
	})
	if err != nil {
		return FailResult(resource.OperationCreate, MapAPIError(err), fmt.Sprintf("failed to create team: %v", err)), nil
	}

	body := resp.GetPayload()
	teamID := fmt.Sprintf("%d", body.TeamID)

	// Read back the full team
	readResult, readErr := h.Read(ctx, client, teamID)
	if readErr != nil || readResult.ErrorCode != "" {
		out := teamProps{ID: teamID, Name: p.Name, Email: p.Email}
		outJSON, _ := json.Marshal(out)
		return SuccessResult(resource.OperationCreate, teamID, outJSON), nil
	}
	return SuccessResult(resource.OperationCreate, teamID, json.RawMessage(readResult.Properties)), nil
}

func (h *TeamHandler) Read(ctx context.Context, client *goapi.GrafanaHTTPAPI, nativeID string) (*resource.ReadResult, error) {
	resp, err := client.Teams.GetTeamByID(&teams.GetTeamByIDParams{
		TeamID:  nativeID,
		Context: ctx,
	})
	if err != nil {
		code := MapAPIError(err)
		if code == resource.OperationErrorCodeNotFound {
			return &resource.ReadResult{
				ResourceType: "Grafana::Core::Team",
				ErrorCode:    resource.OperationErrorCodeNotFound,
			}, nil
		}
		return &resource.ReadResult{
			ResourceType: "Grafana::Core::Team",
			ErrorCode:    code,
		}, nil
	}

	team := resp.GetPayload()
	id := nativeID
	if team.ID != nil {
		id = fmt.Sprintf("%d", *team.ID)
	}
	name := ""
	if team.Name != nil {
		name = *team.Name
	}

	out := teamProps{
		ID:    id,
		Name:  name,
		Email: team.Email,
	}
	outJSON, _ := json.Marshal(out)
	return &resource.ReadResult{
		ResourceType: "Grafana::Core::Team",
		Properties:   string(outJSON),
	}, nil
}

func (h *TeamHandler) Update(ctx context.Context, client *goapi.GrafanaHTTPAPI, nativeID string, prior, desired json.RawMessage) (*resource.ProgressResult, error) {
	var desiredProps teamProps
	if err := json.Unmarshal(desired, &desiredProps); err != nil {
		return FailResult(resource.OperationUpdate, resource.OperationErrorCodeInvalidRequest, fmt.Sprintf("invalid properties: %v", err)), nil
	}

	_, err := client.Teams.UpdateTeam(nativeID, &models.UpdateTeamCommand{
		Name:  desiredProps.Name,
		Email: desiredProps.Email,
	})
	if err != nil {
		return FailResult(resource.OperationUpdate, MapAPIError(err), fmt.Sprintf("failed to update team: %v", err)), nil
	}

	readResult, readErr := h.Read(ctx, client, nativeID)
	if readErr != nil || readResult.ErrorCode != "" {
		outJSON, _ := json.Marshal(desiredProps)
		return SuccessResult(resource.OperationUpdate, nativeID, outJSON), nil
	}
	return SuccessResult(resource.OperationUpdate, nativeID, json.RawMessage(readResult.Properties)), nil
}

func (h *TeamHandler) Delete(ctx context.Context, client *goapi.GrafanaHTTPAPI, nativeID string) (*resource.ProgressResult, error) {
	_, err := client.Teams.GetTeamByID(&teams.GetTeamByIDParams{
		TeamID:  nativeID,
		Context: ctx,
	})
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
		return FailResult(resource.OperationDelete, code, fmt.Sprintf("failed to check team existence: %v", err)), nil
	}

	_, err = client.Teams.DeleteTeamByID(nativeID)
	if err != nil {
		return FailResult(resource.OperationDelete, MapAPIError(err), fmt.Sprintf("failed to delete team: %v", err)), nil
	}

	return &resource.ProgressResult{
		Operation:       resource.OperationDelete,
		OperationStatus: resource.OperationStatusSuccess,
		NativeID:        nativeID,
	}, nil
}

func (h *TeamHandler) List(ctx context.Context, client *goapi.GrafanaHTTPAPI, pageSize int32, pageToken *string) (*resource.ListResult, error) {
	page := int64(1)
	if pageToken != nil {
		if p, err := strconv.ParseInt(*pageToken, 10, 64); err == nil {
			page = p
		}
	}

	perPage := int64(1000)
	resp, err := client.Teams.SearchTeams(&teams.SearchTeamsParams{
		Page:    &page,
		Perpage: &perPage,
		Context: ctx,
	})
	if err != nil {
		return &resource.ListResult{NativeIDs: []string{}}, nil
	}

	var ids []string
	for _, team := range resp.GetPayload().Teams {
		if team.ID != nil {
			ids = append(ids, fmt.Sprintf("%d", *team.ID))
		}
	}
	return &resource.ListResult{NativeIDs: ids}, nil
}
