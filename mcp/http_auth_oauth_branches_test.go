package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// These tests exercise the previously-uncovered failure and fallback branches of
// authenticateOAuth (OAuth 2.1 introspection). Each is a security-relevant
// rejection path: a bug here could accept a token it should reject.

func oauthAuthForURL(url string) *HTTPAuth {
	return NewHTTPAuth(HTTPServerOptions{OAuthIntrospectionURL: url})
}

func TestOAuthIntrospection_Non200Rejected(t *testing.T) {
	t.Parallel()
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer idp.Close()
	if _, ok := oauthAuthForURL(idp.URL).Authenticate(context.Background(), "tok"); ok {
		t.Error("expected rejection on non-200 introspection response")
	}
}

func TestOAuthIntrospection_MalformedJSONRejected(t *testing.T) {
	t.Parallel()
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not-json{{"))
	}))
	defer idp.Close()
	if _, ok := oauthAuthForURL(idp.URL).Authenticate(context.Background(), "tok"); ok {
		t.Error("expected rejection on malformed introspection JSON")
	}
}

func TestOAuthIntrospection_NetworkErrorRejected(t *testing.T) {
	t.Parallel()
	idp := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := idp.URL
	idp.Close() // force a connection-refused error on Do
	if _, ok := oauthAuthForURL(url).Authenticate(context.Background(), "tok"); ok {
		t.Error("expected rejection when introspection endpoint is unreachable")
	}
}

func TestOAuthIntrospection_ExpiredTokenRejected(t *testing.T) {
	t.Parallel()
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// active, with scope, but expired an hour ago.
		exp := time.Now().Add(-time.Hour).Unix()
		_, _ = w.Write([]byte(`{"active":true,"scope":"trvl:read","exp":` + strconv.FormatInt(exp, 10) + `}`))
	}))
	defer idp.Close()
	if _, ok := oauthAuthForURL(idp.URL).Authenticate(context.Background(), "tok"); ok {
		t.Error("expected rejection on expired token")
	}
}

func TestOAuthIntrospection_SubjectFallbackToUsernameThenClientID(t *testing.T) {
	t.Parallel()
	// sub absent -> falls back to username.
	idpUser := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"active":true,"scope":"trvl:read","username":"alice"}`))
	}))
	defer idpUser.Close()
	acc, ok := oauthAuthForURL(idpUser.URL).Authenticate(context.Background(), "tok")
	if !ok || acc.Subject != "alice" {
		t.Errorf("subject = %q ok=%v, want alice", acc.Subject, ok)
	}

	// sub + username absent -> falls back to client_id.
	idpClient := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"active":true,"scope":"trvl:write","client_id":"svc-42"}`))
	}))
	defer idpClient.Close()
	acc2, ok2 := oauthAuthForURL(idpClient.URL).Authenticate(context.Background(), "tok")
	if !ok2 || acc2.Subject != "svc-42" {
		t.Errorf("subject = %q ok=%v, want svc-42", acc2.Subject, ok2)
	}
}

