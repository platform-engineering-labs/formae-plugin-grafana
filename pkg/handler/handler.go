// Package handler defines the resource handler interface and registry.
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	goapi "github.com/grafana/grafana-openapi-client-go/client"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// ResourceHandler defines CRUD+List operations for a single resource type.
type ResourceHandler interface {
	Create(ctx context.Context, client *goapi.GrafanaHTTPAPI, props json.RawMessage) (*resource.ProgressResult, error)
	Read(ctx context.Context, client *goapi.GrafanaHTTPAPI, nativeID string) (*resource.ReadResult, error)
	Update(ctx context.Context, client *goapi.GrafanaHTTPAPI, nativeID string, prior, desired json.RawMessage) (*resource.ProgressResult, error)
	Delete(ctx context.Context, client *goapi.GrafanaHTTPAPI, nativeID string) (*resource.ProgressResult, error)
	List(ctx context.Context, client *goapi.GrafanaHTTPAPI, pageSize int32, pageToken *string) (*resource.ListResult, error)
}

// Registry maps resource type strings to their handlers.
var Registry = map[string]ResourceHandler{}

// Register adds a handler for a resource type.
func Register(resourceType string, h ResourceHandler) {
	Registry[resourceType] = h
}

// Get returns the handler for the given resource type.
func Get(resourceType string) (ResourceHandler, error) {
	h, ok := Registry[resourceType]
	if !ok {
		return nil, fmt.Errorf("no handler registered for resource type %q", resourceType)
	}
	return h, nil
}

// MapAPIError maps Grafana API errors to formae OperationErrorCodes.
func MapAPIError(err error) resource.OperationErrorCode {
	if err == nil {
		return ""
	}

	// Use the Code() interface if available
	type coder interface {
		Code() int
	}
	if c, ok := err.(coder); ok {
		switch c.Code() {
		case 404:
			return resource.OperationErrorCodeNotFound
		case 401:
			return resource.OperationErrorCodeInvalidCredentials
		case 403:
			return resource.OperationErrorCodeAccessDenied
		case 409:
			return resource.OperationErrorCodeAlreadyExists
		case 412:
			return resource.OperationErrorCodeResourceConflict
		case 422:
			return resource.OperationErrorCodeInvalidRequest
		case 429:
			return resource.OperationErrorCodeThrottling
		case 500:
			return resource.OperationErrorCodeServiceInternalError
		case 503:
			return resource.OperationErrorCodeServiceInternalError
		}
	}

	errStr := strings.ToLower(err.Error())
	switch {
	case strings.Contains(errStr, "not found"):
		return resource.OperationErrorCodeNotFound
	case strings.Contains(errStr, "unauthorized"):
		return resource.OperationErrorCodeInvalidCredentials
	case strings.Contains(errStr, "forbidden"):
		return resource.OperationErrorCodeAccessDenied
	case strings.Contains(errStr, "conflict"):
		return resource.OperationErrorCodeAlreadyExists
	case strings.Contains(errStr, "rate limit"):
		return resource.OperationErrorCodeThrottling
	}

	// No HTTP status and no recognized server-error substring. Distinguish a
	// transport failure (Grafana never answered — the endpoint is gone) from an
	// opaque server-side error: a transport failure must surface as a
	// reachability signal (NetworkFailure/ServiceTimeout) so the agent can tell
	// "unreachable" from "deleted" and reap a permanently-gone target. An
	// InternalFailure carries no health signal and would leave the target
	// wrongly marked reachable forever.
	if code, ok := transportErrorCode(err); ok {
		return code
	}
	return resource.OperationErrorCodeInternalFailure
}

// transportErrorCode classifies a client-side transport failure — one where no
// HTTP response was received from Grafana — into a reachability signal. It
// returns (code, true) for a genuine transport failure and ("", false)
// otherwise, so callers can fall back to a server-error classification.
func transportErrorCode(err error) (resource.OperationErrorCode, bool) {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return resource.OperationErrorCodeServiceTimeout, true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return resource.OperationErrorCodeServiceTimeout, true
		}
		return resource.OperationErrorCodeNetworkFailure, true
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return resource.OperationErrorCodeNetworkFailure, true
	}
	return "", false
}

// FailResult creates a failure ProgressResult for the given operation.
func FailResult(op resource.Operation, code resource.OperationErrorCode, msg string) *resource.ProgressResult {
	return &resource.ProgressResult{
		Operation:       op,
		OperationStatus: resource.OperationStatusFailure,
		ErrorCode:       code,
		StatusMessage:   msg,
	}
}

// stripNulls removes null-valued keys from JSON objects and arrays of objects.
// Grafana's API returns null for unset optional fields; stripping them keeps
// the stored properties minimal and matching what the user declared.
func stripNulls(data json.RawMessage) json.RawMessage {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return data
	}
	cleaned := stripNullsRecursive(raw)
	out, err := json.Marshal(cleaned)
	if err != nil {
		return data
	}
	return out
}

func stripNullsRecursive(v any) any {
	switch val := v.(type) {
	case map[string]any:
		cleaned := make(map[string]any)
		for k, v := range val {
			if v != nil {
				cleaned[k] = stripNullsRecursive(v)
			}
		}
		return cleaned
	case []any:
		cleaned := make([]any, len(val))
		for i, v := range val {
			cleaned[i] = stripNullsRecursive(v)
		}
		return cleaned
	default:
		return v
	}
}

// SuccessResult creates a success ProgressResult for the given operation.
func SuccessResult(op resource.Operation, nativeID string, props json.RawMessage) *resource.ProgressResult {
	return &resource.ProgressResult{
		Operation:          op,
		OperationStatus:    resource.OperationStatusSuccess,
		NativeID:           nativeID,
		ResourceProperties: props,
	}
}
