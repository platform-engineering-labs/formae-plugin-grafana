package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	goapi "github.com/grafana/grafana-openapi-client-go/client"
	"github.com/grafana/grafana-openapi-client-go/client/provisioning"
	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/platform-engineering-labs/formae-plugin-grafana/internal/notifiers"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

func init() {
	Register("GRAFANA::Alerting::ContactPoint", &ContactPointHandler{})
}

// ContactPointHandler implements CRUD+List for Grafana contact points.
type ContactPointHandler struct {
	metadata notifierMetadata
}

type contactPointProps struct {
	UID                   string         `json:"uid,omitempty"`
	Name                  string         `json:"name"`
	Type                  string         `json:"contactPointType"`
	Settings              map[string]any `json:"settings,omitempty"`
	DisableResolveMessage bool           `json:"disableResolveMessage,omitempty"`
}

// redactedSentinel is what Grafana returns in place of an option it classifies
// as secret: those are stored encrypted and never readable back.
const redactedSentinel = "[REDACTED]"

// secretOptionNames is the set of option names Grafana classifies as secret,
// taken from the embedded notifier snapshot and computed once. Deciding whether
// a value is a redacted secret must not put a network call on the read path.
var secretOptionNames = sync.OnceValue(func() map[string]struct{} {
	return notifiers.SecretNames(notifiers.Baked())
})

// isRedactedSecret reports whether the option at key holds a secret Grafana
// redacted, which takes two signals together: the value is exactly the
// sentinel, and the option's own name is one Grafana classifies as secret. The
// name is the last segment of key, which is how the classification is keyed, so
// a nested option matches under its own name whether it arrives inside its
// parent object or flattened into a dotted key.
//
// Neither signal carries on its own. The value alone would hide a non-secret
// option an operator set to that literal - a webhook title of "[REDACTED]" is a
// value Grafana hands back verbatim, and dropping it would exempt it from the
// drift check. The classification alone would hide a webhook `url`: the name is
// secure for the types that redact it (slack, discord, victorops), but webhook,
// pagerduty, teams and oncall return the real endpoint, which stays reported
// precisely because its value is then not the sentinel.
func isRedactedSecret(key string, value any) bool {
	s, ok := value.(string)
	if !ok || s != redactedSentinel {
		return false
	}
	name := key
	if i := strings.LastIndex(key, "."); i >= 0 {
		name = key[i+1:]
	}
	_, secret := secretOptionNames()[name]
	return secret
}

// stripRedacted returns v with every key holding a redacted secret removed,
// recursing through nested objects and through objects inside
// arrays. A stripped key is one the plugin declines to report, so formae's
// property merge keeps the author's declared value while every observable key
// stays reported and drift-checked individually. An object left empty by
// stripping is dropped from its parent as well - reporting an empty object no
// declaration can match would drift on every reconcile - while a sibling that
// survives stripping is preserved. An object inside an array keeps its
// position even when stripping empties it, because dropping it would renumber
// the elements around it. Only an exact match is a redacted secret: a value
// that merely contains the sentinel (an alert message quoting it) is an
// observable value and stays.
func stripRedacted(v any) any {
	switch val := v.(type) {
	case map[string]any:
		cleaned := make(map[string]any, len(val))
		for k, item := range val {
			if isRedactedSecret(k, item) {
				continue
			}
			stripped := stripRedacted(item)
			if m, ok := stripped.(map[string]any); ok && len(m) == 0 {
				continue
			}
			cleaned[k] = stripped
		}
		return cleaned
	case []any:
		cleaned := make([]any, len(val))
		for i, item := range val {
			cleaned[i] = stripRedacted(item)
		}
		return cleaned
	default:
		return v
	}
}

// contactPointResponseProps assembles the Properties payload that round-trips
// back to formae, reporting settings as the server's view minus every redacted
// key.
//
// A contact point whose every option is secret - a Slack one declaring only
// `url`, a PagerDuty one declaring only `integrationKey` - strips down to an
// empty object, and the omitempty on Settings then leaves it out of the
// payload entirely. That is what makes the merge keep the declared values:
// reporting an empty settings object instead would drift on every reconcile.
func contactPointResponseProps(uid, name, cpType string, settings any, disableResolveMessage bool) contactPointProps {
	// A server view that is not an object leaves settings unreported, which is
	// the safe direction: the merge then keeps what the author declared.
	reported, _ := stripRedacted(settings).(map[string]any)
	return contactPointProps{
		UID:                   uid,
		Name:                  name,
		Type:                  cpType,
		Settings:              reported,
		DisableResolveMessage: disableResolveMessage,
	}
}

func (h *ContactPointHandler) Create(ctx context.Context, client *goapi.GrafanaHTTPAPI, props json.RawMessage) (*resource.ProgressResult, error) {
	var p contactPointProps
	if err := json.Unmarshal(props, &p); err != nil {
		return FailResult(resource.OperationCreate, resource.OperationErrorCodeInvalidRequest, fmt.Sprintf("invalid properties: %v", err)), nil
	}

	if err := h.metadata.validateSettings(ctx, client, p.Type, p.Settings); err != nil {
		return FailResult(resource.OperationCreate, resource.OperationErrorCodeInvalidRequest, err.Error()), nil
	}

	cp := &models.EmbeddedContactPoint{
		Name:                  p.Name,
		Type:                  strPtr(p.Type),
		Settings:              p.Settings,
		DisableResolveMessage: p.DisableResolveMessage,
	}
	if p.UID != "" {
		cp.UID = p.UID
	}

	xDisableProvenance := "true"
	resp, postErr := client.Provisioning.PostContactpoints(&provisioning.PostContactpointsParams{
		Body:               cp,
		XDisableProvenance: &xDisableProvenance,
		Context:            ctx,
	})
	if postErr != nil {
		return FailResult(resource.OperationCreate, MapAPIError(postErr), fmt.Sprintf("failed to create contact point: %v", postErr)), nil
	}

	created := resp.GetPayload()
	cpType := ""
	if created.Type != nil {
		cpType = *created.Type
	}

	// Report the server's read view rather than the write reply. The reply
	// flattens a nested redacted secret into a dotted sibling key
	// ("hmacConfig.secret" beside "hmacConfig"), a shape no later read ever
	// returns, so recording it would leave state permanently diverged.
	readResult, readErr := h.Read(ctx, client, created.UID)
	if readErr != nil || readResult == nil || readResult.ErrorCode != "" {
		// The contact point already exists by now, so a failing read-back must
		// not fail the create: that would orphan it and invite a colliding
		// retry. Report the write response through the same stripping instead.
		out := contactPointResponseProps(created.UID, created.Name, cpType, created.Settings, created.DisableResolveMessage)
		outJSON, _ := json.Marshal(out)
		return SuccessResult(resource.OperationCreate, created.UID, outJSON), nil
	}
	return SuccessResult(resource.OperationCreate, created.UID, json.RawMessage(readResult.Properties)), nil
}

func (h *ContactPointHandler) Read(ctx context.Context, client *goapi.GrafanaHTTPAPI, nativeID string) (*resource.ReadResult, error) {
	// Grafana doesn't have a GetContactPoint by UID endpoint.
	// We need to list all and find ours.
	resp, err := client.Provisioning.GetContactpoints(&provisioning.GetContactpointsParams{
		Context: ctx,
	})
	if err != nil {
		return &resource.ReadResult{
			ResourceType: "GRAFANA::Alerting::ContactPoint",
			ErrorCode:    MapAPIError(err),
		}, nil
	}

	for _, cp := range resp.GetPayload() {
		if cp.UID == nativeID {
			cpType := ""
			if cp.Type != nil {
				cpType = *cp.Type
			}
			out := contactPointResponseProps(cp.UID, cp.Name, cpType, cp.Settings, cp.DisableResolveMessage)
			outJSON, _ := json.Marshal(out)
			return &resource.ReadResult{
				ResourceType: "GRAFANA::Alerting::ContactPoint",
				Properties:   string(outJSON),
			}, nil
		}
	}

	return &resource.ReadResult{
		ResourceType: "GRAFANA::Alerting::ContactPoint",
		ErrorCode:    resource.OperationErrorCodeNotFound,
	}, nil
}

func (h *ContactPointHandler) Update(ctx context.Context, client *goapi.GrafanaHTTPAPI, nativeID string, prior, desired json.RawMessage) (*resource.ProgressResult, error) {
	var p contactPointProps
	if err := json.Unmarshal(desired, &p); err != nil {
		return FailResult(resource.OperationUpdate, resource.OperationErrorCodeInvalidRequest, fmt.Sprintf("invalid properties: %v", err)), nil
	}

	if err := h.metadata.validateSettings(ctx, client, p.Type, p.Settings); err != nil {
		return FailResult(resource.OperationUpdate, resource.OperationErrorCodeInvalidRequest, err.Error()), nil
	}

	cp := &models.EmbeddedContactPoint{
		UID:                   nativeID,
		Name:                  p.Name,
		Type:                  strPtr(p.Type),
		Settings:              p.Settings,
		DisableResolveMessage: p.DisableResolveMessage,
	}

	xDisableProvenance := "true"
	_, putErr := client.Provisioning.PutContactpoint(&provisioning.PutContactpointParams{
		UID:                nativeID,
		Body:               cp,
		XDisableProvenance: &xDisableProvenance,
		Context:            ctx,
	})
	if putErr != nil {
		return FailResult(resource.OperationUpdate, MapAPIError(putErr), fmt.Sprintf("failed to update contact point: %v", putErr)), nil
	}

	// Report the server's view rather than the settings we just sent. Grafana
	// stores the secret options of a contact point encrypted (the Slack `url`,
	// a PagerDuty `integrationKey`, a webhook `password`) and returns them as
	// the literal "[REDACTED]" on every read path, so echoing the submitted
	// settings back would write the plaintext secret into recorded state and
	// leave it permanently diverged from what Read reports. Create reports via
	// Read for the same reason.
	//
	// The contact point is already updated by now, so a failing read-back must
	// not fail the update: it reports the submitted properties instead.
	readResult, readErr := h.Read(ctx, client, nativeID)
	if readErr != nil || readResult == nil || readResult.ErrorCode != "" {
		out := contactPointResponseProps(nativeID, p.Name, p.Type, p.Settings, p.DisableResolveMessage)
		outJSON, _ := json.Marshal(out)
		return SuccessResult(resource.OperationUpdate, nativeID, outJSON), nil
	}
	return SuccessResult(resource.OperationUpdate, nativeID, json.RawMessage(readResult.Properties)), nil
}

func (h *ContactPointHandler) Delete(ctx context.Context, client *goapi.GrafanaHTTPAPI, nativeID string) (*resource.ProgressResult, error) {
	// Check existence
	readResult, _ := h.Read(ctx, client, nativeID)
	if readResult != nil && readResult.ErrorCode == resource.OperationErrorCodeNotFound {
		return &resource.ProgressResult{
			Operation:       resource.OperationDelete,
			OperationStatus: resource.OperationStatusFailure,
			ErrorCode:       resource.OperationErrorCodeNotFound,
			NativeID:        nativeID,
		}, nil
	}

	_, err := client.Provisioning.DeleteContactpoints(nativeID)
	if err != nil {
		return FailResult(resource.OperationDelete, MapAPIError(err), fmt.Sprintf("failed to delete contact point: %v", err)), nil
	}

	return &resource.ProgressResult{
		Operation:       resource.OperationDelete,
		OperationStatus: resource.OperationStatusSuccess,
		NativeID:        nativeID,
	}, nil
}

func (h *ContactPointHandler) List(ctx context.Context, client *goapi.GrafanaHTTPAPI, pageSize int32, pageToken *string) (*resource.ListResult, error) {
	resp, err := client.Provisioning.GetContactpoints(&provisioning.GetContactpointsParams{
		Context: ctx,
	})
	if err != nil {
		return &resource.ListResult{NativeIDs: []string{}}, nil
	}

	var ids []string
	for _, cp := range resp.GetPayload() {
		if cp.UID != "" {
			ids = append(ids, cp.UID)
		}
	}
	return &resource.ListResult{NativeIDs: ids}, nil
}
