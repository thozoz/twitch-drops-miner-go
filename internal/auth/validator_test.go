package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-resty/resty/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidate_Success(t *testing.T) {
	validateFixture := loadFixture(t, "auth_validate.json")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/oauth2/validate", r.URL.Path)
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "OAuth valid-test-token", r.Header.Get("Authorization"))

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(validateFixture)
	}))
	defer server.Close()

	client := resty.New()
	userID, login, clientID, err := Validate(
		context.Background(),
		client,
		"valid-test-token",
		WithValidateBaseURL(server.URL),
	)

	require.NoError(t, err)
	assert.Equal(t, 12345678, userID)
	assert.Equal(t, "testuser", login)
	assert.Equal(t, AndroidClientID, clientID)
}

func TestValidate_InvalidToken_401(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/oauth2/validate", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"status":401,"message":"invalid access token"}`))
	}))
	defer server.Close()

	client := resty.New()
	_, _, _, err := Validate(
		context.Background(),
		client,
		"expired-test-token",
		WithValidateBaseURL(server.URL),
	)

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrTokenInvalid), "expected ErrTokenInvalid, got %v", err)
}

func TestValidate_ServerError_500(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/oauth2/validate", r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`internal server error`))
	}))
	defer server.Close()

	client := resty.New()
	_, _, _, err := Validate(
		context.Background(),
		client,
		"some-token",
		WithValidateBaseURL(server.URL),
	)

	require.Error(t, err)
	assert.False(t, errors.Is(err, ErrTokenInvalid))
	assert.Contains(t, err.Error(), "500")
}

func TestValidate_ClientIDMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/oauth2/validate", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"client_id": "other_different_client_id",
			"login": "differentuser",
			"scopes": [],
			"user_id": "87654321",
			"expires_in": 12345
		}`))
	}))
	defer server.Close()

	client := resty.New()
	userID, login, clientID, err := Validate(
		context.Background(),
		client,
		"different-token",
		WithValidateBaseURL(server.URL),
	)

	require.NoError(t, err)
	assert.Equal(t, 87654321, userID)
	assert.Equal(t, "differentuser", login)
	assert.Equal(t, "other_different_client_id", clientID)
	// Caller can detect client ID mismatch by comparing clientID != AndroidClientID
	assert.NotEqual(t, AndroidClientID, clientID)
}
