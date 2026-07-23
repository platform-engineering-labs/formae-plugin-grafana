// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package handler

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"testing"

	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// timeoutError is a net.Error whose Timeout() reports true, standing in for a
// dial/read timeout against an unreachable Grafana.
type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

// A transport failure means Grafana never answered (the endpoint is gone), as
// opposed to a server response we can classify by status. Such failures must
// surface as a reachability signal (NetworkFailure/ServiceTimeout) so the agent
// can distinguish "unreachable" from "deleted" and reap a permanently-gone
// target — never the InternalFailure default, which carries no health signal.
func TestMapAPIError_TransportFailuresAreReachabilitySignals(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want resource.OperationErrorCode
	}{
		{
			name: "connection refused (dial) maps to NetworkFailure",
			err:  &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused")},
			want: resource.OperationErrorCodeNetworkFailure,
		},
		{
			name: "url.Error wrapping a dial failure maps to NetworkFailure",
			err: &url.Error{
				Op:  "Get",
				URL: "http://localhost:3333/api/folders",
				Err: &net.OpError{Op: "dial", Err: errors.New("connection refused")},
			},
			want: resource.OperationErrorCodeNetworkFailure,
		},
		{
			name: "net timeout maps to ServiceTimeout",
			err:  timeoutError{},
			want: resource.OperationErrorCodeServiceTimeout,
		},
		{
			name: "context deadline exceeded maps to ServiceTimeout",
			err:  fmt.Errorf("request failed: %w", context.DeadlineExceeded),
			want: resource.OperationErrorCodeServiceTimeout,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MapAPIError(tt.err); got != tt.want {
				t.Errorf("MapAPIError() = %q, want %q", got, tt.want)
			}
		})
	}
}

// An unclassified error that is NOT a transport failure must still map to
// InternalFailure — we must not mislabel a genuine server-side error as a target
// being unreachable.
func TestMapAPIError_UnclassifiedNonTransportStaysInternalFailure(t *testing.T) {
	if got := MapAPIError(errors.New("something odd happened")); got != resource.OperationErrorCodeInternalFailure {
		t.Errorf("MapAPIError(non-transport) = %q, want %q", got, resource.OperationErrorCodeInternalFailure)
	}
}
