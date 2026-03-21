package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-openapi/strfmt"
	goapi "github.com/grafana/grafana-openapi-client-go/client"
	"github.com/grafana/grafana-openapi-client-go/client/provisioning"
	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

func init() {
	Register("Grafana::Alerting::AlertRule", &AlertRuleHandler{})
}

// AlertRuleHandler implements CRUD+List for Grafana alert rules.
type AlertRuleHandler struct{}

type alertRuleProps struct {
	UID          string `json:"uid,omitempty"`
	Title        string `json:"title"`
	FolderUID    string `json:"folderUid"`
	RuleGroup    string `json:"ruleGroup"`
	Condition    string `json:"condition"`
	PendingPeriod string `json:"pendingPeriod,omitempty"`
	Data         string `json:"data"`
	NoDataState  string `json:"noDataState,omitempty"`
	ExecErrState string `json:"execErrState,omitempty"`
	Labels       string `json:"labels,omitempty"`
	Annotations  string `json:"annotations,omitempty"`
}

func parseDuration(s string) strfmt.Duration {
	d, _ := time.ParseDuration(s)
	return strfmt.Duration(d)
}

func formatDuration(d strfmt.Duration) string {
	return time.Duration(d).String()
}

func buildAlertRuleModel(p *alertRuleProps) *models.ProvisionedAlertRule {
	rule := &models.ProvisionedAlertRule{
		Title:        strPtr(p.Title),
		FolderUID:    strPtr(p.FolderUID),
		RuleGroup:    strPtr(p.RuleGroup),
		Condition:    strPtr(p.Condition),
		NoDataState:  strPtr(p.NoDataState),
		ExecErrState: strPtr(p.ExecErrState),
		OrgID:        int64Ptr(1),
	}

	if p.UID != "" {
		rule.UID = p.UID
	}

	forDur := parseDuration(p.PendingPeriod)
	rule.For = &forDur

	// Parse data (alert queries)
	if p.Data != "" {
		var queries []*models.AlertQuery
		if err := json.Unmarshal([]byte(p.Data), &queries); err == nil {
			rule.Data = queries
		}
	}

	// Parse labels
	if p.Labels != "" {
		var labels map[string]string
		if err := json.Unmarshal([]byte(p.Labels), &labels); err == nil {
			rule.Labels = labels
		}
	}

	// Parse annotations
	if p.Annotations != "" {
		var annotations map[string]string
		if err := json.Unmarshal([]byte(p.Annotations), &annotations); err == nil {
			rule.Annotations = annotations
		}
	}

	return rule
}

func alertRuleToProps(rule *models.ProvisionedAlertRule) alertRuleProps {
	p := alertRuleProps{
		UID: rule.UID,
	}
	if rule.Title != nil {
		p.Title = *rule.Title
	}
	if rule.FolderUID != nil {
		p.FolderUID = *rule.FolderUID
	}
	if rule.RuleGroup != nil {
		p.RuleGroup = *rule.RuleGroup
	}
	if rule.Condition != nil {
		p.Condition = *rule.Condition
	}
	if rule.For != nil {
		p.PendingPeriod = formatDuration(*rule.For)
	}
	if rule.NoDataState != nil {
		p.NoDataState = *rule.NoDataState
	}
	if rule.ExecErrState != nil {
		p.ExecErrState = *rule.ExecErrState
	}
	if rule.Data != nil {
		p.Data = normalizeAlertQueryData(rule.Data)
	}
	if rule.Labels != nil {
		labelsJSON, _ := json.Marshal(rule.Labels)
		p.Labels = string(labelsJSON)
	}
	if rule.Annotations != nil {
		annotationsJSON, _ := json.Marshal(rule.Annotations)
		p.Annotations = string(annotationsJSON)
	}
	return p
}

// normalizeAlertQueryData serializes alert query data, stripping server-added
// fields from the model (like intervalMs, maxDataPoints) so that the output
// matches what the user declared.
func normalizeAlertQueryData(queries []*models.AlertQuery) string {
	type normalizedQuery struct {
		RefID             string         `json:"refId,omitempty"`
		DatasourceUID     string         `json:"datasourceUid,omitempty"`
		Model             map[string]any `json:"model,omitempty"`
		QueryType         string         `json:"queryType,omitempty"`
		RelativeTimeRange map[string]any `json:"relativeTimeRange,omitempty"`
	}

	// Fields that Grafana adds to the model that we don't manage
	serverAddedModelFields := map[string]bool{
		"intervalMs":    true,
		"maxDataPoints": true,
	}

	var normalized []normalizedQuery
	for _, q := range queries {
		nq := normalizedQuery{
			RefID:         q.RefID,
			DatasourceUID: q.DatasourceUID,
			QueryType:     q.QueryType,
		}

		// Normalize model: strip server-added fields
		if q.Model != nil {
			if modelMap, ok := q.Model.(map[string]any); ok {
				cleaned := make(map[string]any)
				for k, v := range modelMap {
					if !serverAddedModelFields[k] {
						cleaned[k] = v
					}
				}
				nq.Model = cleaned
			}
		}

		// Normalize relativeTimeRange: always include from and to
		if q.RelativeTimeRange != nil {
			nq.RelativeTimeRange = map[string]any{
				"from": int64(q.RelativeTimeRange.From),
				"to":   int64(q.RelativeTimeRange.To),
			}
		}

		normalized = append(normalized, nq)
	}

	data, _ := json.Marshal(normalized)
	return string(data)
}

func int64Ptr(i int64) *int64 { return &i }

func (h *AlertRuleHandler) Create(ctx context.Context, client *goapi.GrafanaHTTPAPI, props json.RawMessage) (*resource.ProgressResult, error) {
	var p alertRuleProps
	if err := json.Unmarshal(props, &p); err != nil {
		return FailResult(resource.OperationCreate, resource.OperationErrorCodeInvalidRequest, fmt.Sprintf("invalid properties: %v", err)), nil
	}

	rule := buildAlertRuleModel(&p)

	xDisableProvenance := "true"
	resp, err := client.Provisioning.PostAlertRule(&provisioning.PostAlertRuleParams{
		Body:               rule,
		XDisableProvenance: &xDisableProvenance,
		Context:            ctx,
	})
	if err != nil {
		return FailResult(resource.OperationCreate, MapAPIError(err), fmt.Sprintf("failed to create alert rule: %v", err)), nil
	}

	created := resp.GetPayload()
	out := alertRuleToProps(created)
	outJSON, _ := json.Marshal(out)
	return SuccessResult(resource.OperationCreate, created.UID, outJSON), nil
}

func (h *AlertRuleHandler) Read(ctx context.Context, client *goapi.GrafanaHTTPAPI, nativeID string) (*resource.ReadResult, error) {
	resp, err := client.Provisioning.GetAlertRule(nativeID)
	if err != nil {
		code := MapAPIError(err)
		if code == resource.OperationErrorCodeNotFound {
			return &resource.ReadResult{
				ResourceType: "Grafana::Alerting::AlertRule",
				ErrorCode:    resource.OperationErrorCodeNotFound,
			}, nil
		}
		return &resource.ReadResult{
			ResourceType: "Grafana::Alerting::AlertRule",
			ErrorCode:    code,
		}, nil
	}

	rule := resp.GetPayload()
	out := alertRuleToProps(rule)
	outJSON, _ := json.Marshal(out)
	return &resource.ReadResult{
		ResourceType: "Grafana::Alerting::AlertRule",
		Properties:   string(outJSON),
	}, nil
}

func (h *AlertRuleHandler) Update(ctx context.Context, client *goapi.GrafanaHTTPAPI, nativeID string, prior, desired json.RawMessage) (*resource.ProgressResult, error) {
	var p alertRuleProps
	if err := json.Unmarshal(desired, &p); err != nil {
		return FailResult(resource.OperationUpdate, resource.OperationErrorCodeInvalidRequest, fmt.Sprintf("invalid properties: %v", err)), nil
	}

	rule := buildAlertRuleModel(&p)

	xDisableProvenance := "true"
	resp, err := client.Provisioning.PutAlertRule(&provisioning.PutAlertRuleParams{
		UID:                nativeID,
		Body:               rule,
		XDisableProvenance: &xDisableProvenance,
		Context:            ctx,
	})
	if err != nil {
		return FailResult(resource.OperationUpdate, MapAPIError(err), fmt.Sprintf("failed to update alert rule: %v", err)), nil
	}

	updated := resp.GetPayload()
	out := alertRuleToProps(updated)
	outJSON, _ := json.Marshal(out)
	return SuccessResult(resource.OperationUpdate, updated.UID, outJSON), nil
}

func (h *AlertRuleHandler) Delete(ctx context.Context, client *goapi.GrafanaHTTPAPI, nativeID string) (*resource.ProgressResult, error) {
	_, err := client.Provisioning.GetAlertRule(nativeID)
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
		return FailResult(resource.OperationDelete, code, fmt.Sprintf("failed to check alert rule existence: %v", err)), nil
	}

	_, err = client.Provisioning.DeleteAlertRule(&provisioning.DeleteAlertRuleParams{
		UID:     nativeID,
		Context: ctx,
	})
	if err != nil {
		return FailResult(resource.OperationDelete, MapAPIError(err), fmt.Sprintf("failed to delete alert rule: %v", err)), nil
	}

	return &resource.ProgressResult{
		Operation:       resource.OperationDelete,
		OperationStatus: resource.OperationStatusSuccess,
		NativeID:        nativeID,
	}, nil
}

func (h *AlertRuleHandler) List(ctx context.Context, client *goapi.GrafanaHTTPAPI, pageSize int32, pageToken *string) (*resource.ListResult, error) {
	resp, err := client.Provisioning.GetAlertRules()
	if err != nil {
		return &resource.ListResult{NativeIDs: []string{}}, nil
	}

	var ids []string
	for _, rule := range resp.GetPayload() {
		if rule.UID != "" {
			ids = append(ids, rule.UID)
		}
	}
	return &resource.ListResult{NativeIDs: ids}, nil
}
