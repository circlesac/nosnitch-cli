package oauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestPKCEChallengeIsS256Base64URL(t *testing.T) {
	if got, want := pkceChallenge("verifier"), "iMnq5o6zALKXGivsnlom_0F5_WYda32GHkxlV7mq7hQ"; got != want {
		t.Fatalf("pkceChallenge() = %q, want %q", got, want)
	}
}

func TestTokenRequestRedactsProviderFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"refresh token secret-value"}`))
	}))
	defer server.Close()

	_, err := (Client{HTTP: server.Client()}).fetchToken(context.Background(), server.URL,
		"application/x-www-form-urlencoded", url.Values{"refresh_token": {"secret-value"}})
	if !errors.Is(err, ErrReauth) {
		t.Fatalf("fetchToken() error = %v, want ErrReauth", err)
	}
	if strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("fetchToken() leaked token value: %v", err)
	}
}

func TestTokenRequestParsesAccessAndRotation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Fatalf("content type = %q", r.Header.Get("Content-Type"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"access","refresh_token":"rotated","expires_in":120}`))
	}))
	defer server.Close()

	parsed, err := (Client{HTTP: server.Client()}).fetchToken(context.Background(), server.URL,
		"application/x-www-form-urlencoded", url.Values{"grant_type": {"refresh_token"}})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.AccessToken != "access" || parsed.RefreshToken != "rotated" || parsed.ExpiresIn != 120 {
		t.Fatalf("token response = %#v", parsed)
	}
}

func TestBuildAuthURLContainsPKCEAndState(t *testing.T) {
	target, err := buildAuthURL(providerOpenAI, "http://127.0.0.1:1234/callback", "challenge", "state")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	for key, want := range map[string]string{
		"code_challenge":        "challenge",
		"code_challenge_method": "S256",
		"state":                 "state",
		"redirect_uri":          "http://127.0.0.1:1234/callback",
	} {
		if query.Get(key) != want {
			t.Fatalf("%s = %q, want %q", key, query.Get(key), want)
		}
	}
}
