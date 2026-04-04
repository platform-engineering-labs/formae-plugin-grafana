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
	Register("Grafana::Alerting::NotificationPolicy", &NotificationPolicyHandler{})
}

// NotificationPolicyHandler implements CRUD for the Grafana notification policy tree.
// This is a singleton resource — there is exactly one per organization.
type NotificationPolicyHandler struct{}

type notificationPolicyProps struct {
	Receiver       string `json:"receiver"`
	GroupBy        string `json:"groupBy,omitempty"`
	GroupWait      string `json:"groupWait,omitempty"`
	GroupInterval  string `json:"groupInterval,omitempty"`
	RepeatInterval string `json:"repeatInterval,omitempty"`
	Routes         string `json:"routes,omitempty"`
}

func routeToProps(route *models.Route) notificationPolicyProps {
	p := notificationPolicyProps{
		Receiver:       route.Receiver,
		GroupWait:      route.GroupWait,
		GroupInterval:  route.GroupInterval,
		RepeatInterval: route.RepeatInterval,
	}
	if route.GroupBy != nil {
		groupByJSON, _ := json.Marshal(route.GroupBy)
		p.GroupBy = string(groupByJSON)
	}
	if route.Routes != nil {
		routesJSON, _ := json.Marshal(route.Routes)
		p.Routes = string(routesJSON)
	}
	return p
}

func propsToRoute(p *notificationPolicyProps) *models.Route {
	route := &models.Route{
		Receiver:       p.Receiver,
		GroupWait:      p.GroupWait,
		GroupInterval:  p.GroupInterval,
		RepeatInterval: p.RepeatInterval,
	}
	if p.GroupBy != "" {
		var groupBy []string
		if err := json.Unmarshal([]byte(p.GroupBy), &groupBy); err == nil {
			route.GroupBy = groupBy
		}
	}
	if p.Routes != "" {
		var routes []*models.Route
		if err := json.Unmarshal([]byte(p.Routes), &routes); err == nil {
			route.Routes = routes
		}
	}
	return route
}

func (h *NotificationPolicyHandler) Create(ctx context.Context, client *goapi.GrafanaHTTPAPI, props json.RawMessage) (*resource.ProgressResult, error) {
	var p notificationPolicyProps
	if err := json.Unmarshal(props, &p); err != nil {
		return FailResult(resource.OperationCreate, resource.OperationErrorCodeInvalidRequest, fmt.Sprintf("invalid properties: %v", err)), nil
	}

	route := propsToRoute(&p)

	xDisableProvenance := "true"
	_, err := client.Provisioning.PutPolicyTree(&provisioning.PutPolicyTreeParams{
		Body:               route,
		XDisableProvenance: &xDisableProvenance,
		Context:            ctx,
	})
	if err != nil {
		return FailResult(resource.OperationCreate, MapAPIError(err), fmt.Sprintf("failed to set notification policy: %v", err)), nil
	}

	// Read back the state
	readResult, readErr := h.Read(ctx, client, p.Receiver)
	if readErr != nil || readResult.ErrorCode != "" {
		outJSON, _ := json.Marshal(p)
		return SuccessResult(resource.OperationCreate, p.Receiver, outJSON), nil
	}
	return SuccessResult(resource.OperationCreate, p.Receiver, json.RawMessage(readResult.Properties)), nil
}

func (h *NotificationPolicyHandler) Read(ctx context.Context, client *goapi.GrafanaHTTPAPI, nativeID string) (*resource.ReadResult, error) {
	resp, err := client.Provisioning.GetPolicyTree()
	if err != nil {
		return &resource.ReadResult{
			ResourceType: "Grafana::Alerting::NotificationPolicy",
			ErrorCode:    MapAPIError(err),
		}, nil
	}

	route := resp.GetPayload()
	out := routeToProps(route)

	// If the receiver doesn't match, the policy was deleted (Grafana reverts
	// to the default receiver). Report as not found so sync can tombstone it.
	if out.Receiver != nativeID {
		return &resource.ReadResult{
			ResourceType: "Grafana::Alerting::NotificationPolicy",
			ErrorCode:    resource.OperationErrorCodeNotFound,
		}, nil
	}

	outJSON, _ := json.Marshal(out)
	return &resource.ReadResult{
		ResourceType: "Grafana::Alerting::NotificationPolicy",
		Properties:   string(outJSON),
	}, nil
}

func (h *NotificationPolicyHandler) Update(ctx context.Context, client *goapi.GrafanaHTTPAPI, nativeID string, prior, desired json.RawMessage) (*resource.ProgressResult, error) {
	var p notificationPolicyProps
	if err := json.Unmarshal(desired, &p); err != nil {
		return FailResult(resource.OperationUpdate, resource.OperationErrorCodeInvalidRequest, fmt.Sprintf("invalid properties: %v", err)), nil
	}

	route := propsToRoute(&p)

	xDisableProvenance := "true"
	_, err := client.Provisioning.PutPolicyTree(&provisioning.PutPolicyTreeParams{
		Body:               route,
		XDisableProvenance: &xDisableProvenance,
		Context:            ctx,
	})
	if err != nil {
		return FailResult(resource.OperationUpdate, MapAPIError(err), fmt.Sprintf("failed to update notification policy: %v", err)), nil
	}

	readResult, readErr := h.Read(ctx, client, nativeID)
	if readErr != nil || readResult.ErrorCode != "" {
		outJSON, _ := json.Marshal(p)
		return SuccessResult(resource.OperationUpdate, nativeID, outJSON), nil
	}
	return SuccessResult(resource.OperationUpdate, nativeID, json.RawMessage(readResult.Properties)), nil
}

func (h *NotificationPolicyHandler) Delete(ctx context.Context, client *goapi.GrafanaHTTPAPI, nativeID string) (*resource.ProgressResult, error) {
	// Reset to default notification policy
	_, err := client.Provisioning.ResetPolicyTree()
	if err != nil {
		return FailResult(resource.OperationDelete, MapAPIError(err), fmt.Sprintf("failed to reset notification policy: %v", err)), nil
	}

	return &resource.ProgressResult{
		Operation:       resource.OperationDelete,
		OperationStatus: resource.OperationStatusSuccess,
		NativeID:        nativeID,
	}, nil
}

func (h *NotificationPolicyHandler) List(ctx context.Context, client *goapi.GrafanaHTTPAPI, pageSize int32, pageToken *string) (*resource.ListResult, error) {
	// Notification policy is a singleton; return the default receiver as the single ID
	resp, err := client.Provisioning.GetPolicyTree()
	if err != nil {
		return &resource.ListResult{NativeIDs: []string{}}, nil
	}

	route := resp.GetPayload()
	if route.Receiver != "" {
		return &resource.ListResult{NativeIDs: []string{route.Receiver}}, nil
	}
	return &resource.ListResult{NativeIDs: []string{}}, nil
}
