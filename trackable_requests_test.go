// Copyright (c) Omlox Client Go Contributors
// SPDX-License-Identifier: MIT

package omlox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/geojson/geometry"
)

// setupTestServer creates a mock server and a client pointing to it for testing.
func setupTestServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *Client) {
	t.Helper() // Marks this as a test helper function

	server := httptest.NewServer(handler)

	client, err := New(server.URL)
	// require stops the test immediately on failure, as a client is essential.
	require.NoError(t, err, "failed to create client")

	return server, client
}

// mockTrackable creates a test trackable for use in tests
func mockTrackable() Trackable {
	return Trackable{
		ID:   uuid.MustParse("9b59961e-2a6a-4712-86e7-aba5a3e8be1f"),
		Type: TrackableTypeOmlox,
		Name: "Test Trackable",
		Geometry: NewPolygon(geometry.NewPoly([]geometry.Point{
			{X: 7.815694, Y: 48.13021599999995},
			{X: 7.815724999999997, Y: 48.13031},
			{X: 7.816582, Y: 48.13018799999995},
			{X: 7.816551, Y: 48.13009399999996},
			{X: 7.815694, Y: 48.13021599999995},
		}, nil, geometry.DefaultIndexOptions)),
		LocationProviders: []string{"ac:23:3f:ac:a3:55"},
		Properties:        json.RawMessage(`{"test": "value"}`),
	}
}

// mockLocation creates a test location for use in tests
func mockLocation() Location {
	now := time.Now().UTC()
	return Location{
		Position:           *NewPointZ(geometry.Point{X: 7.815694, Y: 48.13021599999995}, 1.5),
		Source:             "test-source",
		ProviderType:       LocationProviderTypeUwb,
		ProviderID:         "ac:23:3f:ac:a3:55",
		Trackables:         []uuid.UUID{uuid.MustParse("9b59961e-2a6a-4712-86e7-aba5a3e8be1f")},
		TimestampGenerated: &now,
		TimestampSent:      &now,
	}
}

func TestTrackablesAPI_List(t *testing.T) {
	tests := []struct {
		name           string
		serverResponse string
		serverCode     int
		expected       []Trackable
		expectError    bool
	}{
		{
			name:           "successful list",
			serverResponse: `[{"id":"9b59961e-2a6a-4712-86e7-aba5a3e8be1f","type":"omlox","name":"Test Trackable"}]`,
			serverCode:     http.StatusOK,
			expected: []Trackable{
				{
					ID:   uuid.MustParse("9b59961e-2a6a-4712-86e7-aba5a3e8be1f"),
					Type: TrackableTypeOmlox,
					Name: "Test Trackable",
				},
			},
		},
		{
			name:           "empty list",
			serverResponse: `[]`,
			serverCode:     http.StatusOK,
			expected:       []Trackable{},
		},
		{
			name:           "server error",
			serverResponse: `{"error": "internal server error"}`,
			serverCode:     http.StatusInternalServerError,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "/trackables/summary", r.URL.Path)
				w.WriteHeader(tt.serverCode)
				w.Write([]byte(tt.serverResponse))
			}
			server, client := setupTestServer(t, handler)
			defer server.Close()

			result, err := client.Trackables.List(context.Background())

			if tt.expectError {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTrackablesAPI_IDs(t *testing.T) {
	tests := []struct {
		name           string
		serverResponse string
		serverCode     int
		expected       []uuid.UUID
		expectError    bool
	}{
		{
			name:           "successful ids list",
			serverResponse: `["9b59961e-2a6a-4712-86e7-aba5a3e8be1f", "550e8400-e29b-41d4-a716-446655440000"]`,
			serverCode:     http.StatusOK,
			expected: []uuid.UUID{
				uuid.MustParse("9b59961e-2a6a-4712-86e7-aba5a3e8be1f"),
				uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
			},
		},
		{
			name:           "empty ids list",
			serverResponse: `[]`,
			serverCode:     http.StatusOK,
			expected:       []uuid.UUID{},
		},
		{
			name:           "server error",
			serverResponse: `{"error": "internal server error"}`,
			serverCode:     http.StatusInternalServerError,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "/trackables", r.URL.Path)
				w.WriteHeader(tt.serverCode)
				w.Write([]byte(tt.serverResponse))
			}
			server, client := setupTestServer(t, handler)
			defer server.Close()

			result, err := client.Trackables.IDs(context.Background())

			if tt.expectError {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTrackablesAPI_Create(t *testing.T) {
	trackable := mockTrackable()

	tests := []struct {
		name           string
		input          Trackable
		serverResponse string
		serverCode     int
		expectError    bool
	}{
		{
			name:           "successful create",
			input:          trackable,
			serverResponse: `{"id":"9b59961e-2a6a-4712-86e7-aba5a3e8be1f","type":"omlox","name":"Test Trackable"}`,
			serverCode:     http.StatusCreated,
		},
		{
			name:           "server error",
			input:          trackable,
			serverResponse: `{"error": "validation failed"}`,
			serverCode:     http.StatusBadRequest,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, "/trackables", r.URL.Path)
				contentType := r.Header.Get("Content-Type")
				assert.Condition(t, func() bool {
					return contentType == "" || strings.Contains(contentType, "application/json")
				}, "expected application/json content type if provided, but got %s", contentType)

				w.WriteHeader(tt.serverCode)
				w.Write([]byte(tt.serverResponse))
			}
			server, client := setupTestServer(t, handler)
			defer server.Close()

			result, err := client.Trackables.Create(context.Background(), tt.input)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, result)
				return
			}

			assert.NoError(t, err)
			require.NotNil(t, result)

			assert.Equal(t, tt.input.ID, result.ID)
			assert.Equal(t, tt.input.Type, result.Type)
			assert.Equal(t, tt.input.Name, result.Name)
		})
	}
}

func TestTrackablesAPI_Get(t *testing.T) {
	testID := uuid.MustParse("9b59961e-2a6a-4712-86e7-aba5a3e8be1f")
	expectedTrackable := Trackable{
		ID:   testID,
		Type: TrackableTypeOmlox,
		Name: "Test Trackable",
	}

	tests := []struct {
		name           string
		id             uuid.UUID
		serverResponse string
		serverCode     int
		expectError    bool
	}{
		{
			name:           "successful get",
			id:             testID,
			serverResponse: `{"id":"9b59961e-2a6a-4712-86e7-aba5a3e8be1f","type":"omlox","name":"Test Trackable"}`,
			serverCode:     http.StatusOK,
		},
		{
			name:           "not found",
			id:             testID,
			serverResponse: `{"error": "trackable not found"}`,
			serverCode:     http.StatusNotFound,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := func(w http.ResponseWriter, r *http.Request) {
				expectedPath := "/trackables/" + tt.id.String()
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, expectedPath, r.URL.Path)
				w.WriteHeader(tt.serverCode)
				w.Write([]byte(tt.serverResponse))
			}
			server, client := setupTestServer(t, handler)
			defer server.Close()

			result, err := client.Trackables.Get(context.Background(), tt.id)

			if tt.expectError {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, &expectedTrackable, result)
		})
	}
}

func TestTrackablesAPI_Update(t *testing.T) {
	testID := uuid.MustParse("9b59961e-2a6a-4712-86e7-aba5a3e8be1f")
	trackable := mockTrackable()

	tests := []struct {
		name        string
		id          uuid.UUID
		input       Trackable
		serverCode  int
		expectError bool
	}{
		{name: "successful update", id: testID, input: trackable, serverCode: http.StatusOK},
		{name: "not found", id: testID, input: trackable, serverCode: http.StatusNotFound, expectError: true},
		{name: "validation error", id: testID, input: trackable, serverCode: http.StatusBadRequest, expectError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := func(w http.ResponseWriter, r *http.Request) {
				expectedPath := "/trackables/" + tt.id.String()
				assert.Equal(t, http.MethodPut, r.Method)
				assert.Equal(t, expectedPath, r.URL.Path)
				w.WriteHeader(tt.serverCode)
			}
			server, client := setupTestServer(t, handler)
			defer server.Close()

			err := client.Trackables.Update(context.Background(), tt.input, tt.id)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestTrackablesAPI_Delete(t *testing.T) {
	testID := uuid.MustParse("9b59961e-2a6a-4712-86e7-aba5a3e8be1f")

	tests := []struct {
		name        string
		id          uuid.UUID
		serverCode  int
		expectError bool
	}{
		{name: "successful delete", id: testID, serverCode: http.StatusOK},
		{name: "not found", id: testID, serverCode: http.StatusNotFound, expectError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := func(w http.ResponseWriter, r *http.Request) {
				expectedPath := "/trackables/" + tt.id.String()
				assert.Equal(t, http.MethodDelete, r.Method)
				assert.Equal(t, expectedPath, r.URL.Path)
				w.WriteHeader(tt.serverCode)
			}
			server, client := setupTestServer(t, handler)
			defer server.Close()

			err := client.Trackables.Delete(context.Background(), tt.id)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestTrackablesAPI_DeleteAll(t *testing.T) {
	tests := []struct {
		name        string
		serverCode  int
		expectError bool
	}{
		{name: "successful delete all", serverCode: http.StatusOK},
		{name: "server error", serverCode: http.StatusInternalServerError, expectError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodDelete, r.Method)
				assert.Equal(t, "/trackables", r.URL.Path)
				w.WriteHeader(tt.serverCode)
			}
			server, client := setupTestServer(t, handler)
			defer server.Close()

			err := client.Trackables.DeleteAll(context.Background())

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestTrackablesAPI_GetLocation(t *testing.T) {
	testID := uuid.MustParse("9b59961e-2a6a-4712-86e7-aba5a3e8be1f")
	location := mockLocation()
	successResponse := fmt.Sprintf(`{
		"position": {"type": "Point", "coordinates": [%f, %f, %f]},
		"source": "%s", "provider_type": "%s", "provider_id": "%s",
		"trackables": ["%s"]
	}`, location.Position.Base().X, location.Position.Base().Y, location.Position.Z(),
		location.Source, location.ProviderType, location.ProviderID, testID.String())

	tests := []struct {
		name           string
		id             uuid.UUID
		serverResponse string
		serverCode     int
		expectError    bool
	}{
		{name: "successful get location", id: testID, serverResponse: successResponse, serverCode: http.StatusOK},
		{name: "trackable not found", id: testID, serverCode: http.StatusNotFound, expectError: true},
		{name: "no location available", id: testID, serverCode: http.StatusNotFound, expectError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := func(w http.ResponseWriter, r *http.Request) {
				expectedPath := "/trackables/" + tt.id.String() + "/location"
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, expectedPath, r.URL.Path)
				w.WriteHeader(tt.serverCode)
				w.Write([]byte(tt.serverResponse))
			}
			server, client := setupTestServer(t, handler)
			defer server.Close()

			result, err := client.Trackables.GetLocation(context.Background(), tt.id)

			if tt.expectError {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, location.Source, result.Source)
			assert.Equal(t, location.ProviderID, result.ProviderID)
		})
	}
}

func TestTrackablesAPI_ContextCancellation(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond) // Simulate a slow response
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[]`))
	}
	server, client := setupTestServer(t, handler)
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel the context immediately

	_, err := client.Trackables.List(ctx)

	assert.Error(t, err)
	assert.ErrorContains(t, err, "context canceled")
}
