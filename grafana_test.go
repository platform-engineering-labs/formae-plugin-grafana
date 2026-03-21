//go:build integration

package main

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testTargetConfig() json.RawMessage {
	url := os.Getenv("GRAFANA_URL")
	if url == "" {
		url = "http://localhost:3000"
	}
	return json.RawMessage(`{"Type":"Grafana","Url":"` + url + `"}`)
}

func skipIfNoGrafana(t *testing.T) {
	t.Helper()
	if os.Getenv("GRAFANA_AUTH") == "" {
		t.Skip("GRAFANA_AUTH must be set for integration tests")
	}
}

// --- Folder Tests ---

func TestCreateFolder(t *testing.T) {
	skipIfNoGrafana(t)
	ctx := context.Background()
	p := &Plugin{}

	props, _ := json.Marshal(map[string]any{
		"uid":   "formae-integ-test-folder",
		"title": "Formae Integration Test Folder",
	})

	result, err := p.Create(ctx, &resource.CreateRequest{
		ResourceType: "Grafana::Core::Folder",
		Label:        "test-folder",
		Properties:   props,
		TargetConfig: testTargetConfig(),
	})
	require.NoError(t, err)
	require.NotNil(t, result.ProgressResult)
	assert.Equal(t, resource.OperationStatusSuccess, result.ProgressResult.OperationStatus)
	assert.Equal(t, "formae-integ-test-folder", result.ProgressResult.NativeID)

	t.Cleanup(func() {
		p.Delete(ctx, &resource.DeleteRequest{
			NativeID:     "formae-integ-test-folder",
			ResourceType: "Grafana::Core::Folder",
			TargetConfig: testTargetConfig(),
		})
	})
}

func TestReadFolder(t *testing.T) {
	skipIfNoGrafana(t)
	ctx := context.Background()
	p := &Plugin{}

	// Create first
	props, _ := json.Marshal(map[string]any{
		"uid":   "formae-integ-test-read-folder",
		"title": "Formae Read Test Folder",
	})
	_, err := p.Create(ctx, &resource.CreateRequest{
		ResourceType: "Grafana::Core::Folder",
		Label:        "test-read-folder",
		Properties:   props,
		TargetConfig: testTargetConfig(),
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		p.Delete(ctx, &resource.DeleteRequest{
			NativeID:     "formae-integ-test-read-folder",
			ResourceType: "Grafana::Core::Folder",
			TargetConfig: testTargetConfig(),
		})
	})

	// Read
	result, err := p.Read(ctx, &resource.ReadRequest{
		NativeID:     "formae-integ-test-read-folder",
		ResourceType: "Grafana::Core::Folder",
		TargetConfig: testTargetConfig(),
	})
	require.NoError(t, err)
	assert.Empty(t, result.ErrorCode)
	require.NotEmpty(t, result.Properties)

	var readProps map[string]any
	err = json.Unmarshal([]byte(result.Properties), &readProps)
	require.NoError(t, err)
	assert.Equal(t, "formae-integ-test-read-folder", readProps["uid"])
	assert.Equal(t, "Formae Read Test Folder", readProps["title"])
}

func TestReadFolderNotFound(t *testing.T) {
	skipIfNoGrafana(t)
	ctx := context.Background()
	p := &Plugin{}

	result, err := p.Read(ctx, &resource.ReadRequest{
		NativeID:     "formae-nonexistent-folder-xyz",
		ResourceType: "Grafana::Core::Folder",
		TargetConfig: testTargetConfig(),
	})
	require.NoError(t, err)
	assert.Equal(t, resource.OperationErrorCodeNotFound, result.ErrorCode)
}

func TestUpdateFolder(t *testing.T) {
	skipIfNoGrafana(t)
	ctx := context.Background()
	p := &Plugin{}

	// Create first
	props, _ := json.Marshal(map[string]any{
		"uid":   "formae-integ-test-update-folder",
		"title": "Original Title",
	})
	_, err := p.Create(ctx, &resource.CreateRequest{
		ResourceType: "Grafana::Core::Folder",
		Label:        "test-update-folder",
		Properties:   props,
		TargetConfig: testTargetConfig(),
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		p.Delete(ctx, &resource.DeleteRequest{
			NativeID:     "formae-integ-test-update-folder",
			ResourceType: "Grafana::Core::Folder",
			TargetConfig: testTargetConfig(),
		})
	})

	// Update
	priorProps, _ := json.Marshal(map[string]any{
		"uid":   "formae-integ-test-update-folder",
		"title": "Original Title",
	})
	desiredProps, _ := json.Marshal(map[string]any{
		"uid":   "formae-integ-test-update-folder",
		"title": "Updated Title",
	})
	result, err := p.Update(ctx, &resource.UpdateRequest{
		NativeID:          "formae-integ-test-update-folder",
		ResourceType:      "Grafana::Core::Folder",
		PriorProperties:   priorProps,
		DesiredProperties: desiredProps,
		TargetConfig:      testTargetConfig(),
	})
	require.NoError(t, err)
	assert.Equal(t, resource.OperationStatusSuccess, result.ProgressResult.OperationStatus)

	// Verify
	readResult, _ := p.Read(ctx, &resource.ReadRequest{
		NativeID:     "formae-integ-test-update-folder",
		ResourceType: "Grafana::Core::Folder",
		TargetConfig: testTargetConfig(),
	})
	var readProps map[string]any
	json.Unmarshal([]byte(readResult.Properties), &readProps)
	assert.Equal(t, "Updated Title", readProps["title"])
}

func TestDeleteFolder(t *testing.T) {
	skipIfNoGrafana(t)
	ctx := context.Background()
	p := &Plugin{}

	// Create first
	props, _ := json.Marshal(map[string]any{
		"uid":   "formae-integ-test-delete-folder",
		"title": "Folder To Delete",
	})
	_, err := p.Create(ctx, &resource.CreateRequest{
		ResourceType: "Grafana::Core::Folder",
		Label:        "test-delete-folder",
		Properties:   props,
		TargetConfig: testTargetConfig(),
	})
	require.NoError(t, err)

	// Delete
	result, err := p.Delete(ctx, &resource.DeleteRequest{
		NativeID:     "formae-integ-test-delete-folder",
		ResourceType: "Grafana::Core::Folder",
		TargetConfig: testTargetConfig(),
	})
	require.NoError(t, err)
	assert.Equal(t, resource.OperationStatusSuccess, result.ProgressResult.OperationStatus)

	// Verify deleted
	readResult, _ := p.Read(ctx, &resource.ReadRequest{
		NativeID:     "formae-integ-test-delete-folder",
		ResourceType: "Grafana::Core::Folder",
		TargetConfig: testTargetConfig(),
	})
	assert.Equal(t, resource.OperationErrorCodeNotFound, readResult.ErrorCode)
}

func TestDeleteFolderNotFound(t *testing.T) {
	skipIfNoGrafana(t)
	ctx := context.Background()
	p := &Plugin{}

	result, err := p.Delete(ctx, &resource.DeleteRequest{
		NativeID:     "formae-nonexistent-folder-for-delete",
		ResourceType: "Grafana::Core::Folder",
		TargetConfig: testTargetConfig(),
	})
	require.NoError(t, err)
	assert.Equal(t, resource.OperationStatusFailure, result.ProgressResult.OperationStatus)
	assert.Equal(t, resource.OperationErrorCodeNotFound, result.ProgressResult.ErrorCode)
}

func TestListFolders(t *testing.T) {
	skipIfNoGrafana(t)
	ctx := context.Background()
	p := &Plugin{}

	// Create a folder to ensure at least one exists
	props, _ := json.Marshal(map[string]any{
		"uid":   "formae-integ-test-list-folder",
		"title": "Folder For List Test",
	})
	_, err := p.Create(ctx, &resource.CreateRequest{
		ResourceType: "Grafana::Core::Folder",
		Label:        "test-list-folder",
		Properties:   props,
		TargetConfig: testTargetConfig(),
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		p.Delete(ctx, &resource.DeleteRequest{
			NativeID:     "formae-integ-test-list-folder",
			ResourceType: "Grafana::Core::Folder",
			TargetConfig: testTargetConfig(),
		})
	})

	result, err := p.List(ctx, &resource.ListRequest{
		ResourceType: "Grafana::Core::Folder",
		TargetConfig: testTargetConfig(),
	})
	require.NoError(t, err)
	assert.Contains(t, result.NativeIDs, "formae-integ-test-list-folder")
}
