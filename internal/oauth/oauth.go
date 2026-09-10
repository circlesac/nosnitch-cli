package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

const defaultTimeout = 5 * time.Minute

const (
	providerAnthropic = "anthropic"
	providerOpenAI    = "openai"
)

const (
	anthropicClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	anthropicScope    = "user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"
	anthropicAuthURL  = "https://claude.com/cai/oauth/authorize"
	anthropicTokenURL = "https://platform.claude.com/v1/oauth/token"
	anthropicProfile  = "https://api.anthropic.com/api/oauth/profile"
)

const (
	openAIClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
	openAIScope    = "openid profile email offline_access api.connectors.read api.connectors.invoke"
	openAIOAuthURL = "https://auth.openai.com/oauth/authorize"
	openAITokenURL = "https://auth.openai.com/oauth/token"
)

const (
	openAIRedirectURI  = "http://localhost:1455/auth/callback"
	openAIListenAddr   = "127.0.0.1:1455"
	openAICallbackPath = "/auth/callback"
)

var ErrReauth = errors.New("reauthentication required")

type Token struct {
	AccessToken  string
	RefreshToken string
	IDToken      string
	ExpiresAt    time.Time
}

type Identity struct {
	Subject      string
	Email        string
	Organization string
}

type Client struct {
	HTTP        *http.Client
	OpenBrowser func(string) error
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Error        string `json:"error"`
}

type anthropicProfileResponse struct {
	Account struct {
		UUID  string `json:"uuid"`
		Email string `json:"email"`
	} `json:"account"`
	Organization struct {
		UUID string `json:"uuid"`
	} `json:"organization"`
}

type provider string

func (c Client) Login(ctx context.Context, p string) (Token, Identity, error) {
	provider, err := canonicalProvider(p)
	if err != nil {
		return Token{}, Identity{}, err
	}

	tctx, cancel := defaultContext(ctx)
	defer cancel()

	verifier, err := randomString(48)
	if err != nil {
		return Token{}, Identity{}, fmt.Errorf("generate PKCE verifier: %w", err)
	}
	challenge := pkceChallenge(verifier)
	state, err := randomString(32)
	if err != nil {
		return Token{}, Identity{}, fmt.Errorf("generate OAuth state: %w", err)
	}

	listenAddr := "127.0.0.1:0"
	redirectURI := ""
	path := "/callback"
	expectedHost := ""
	if provider == providerOpenAI {
		listenAddr = openAIListenAddr
		redirectURI = openAIRedirectURI
		path = openAICallbackPath
		expectedHost = "localhost:1455"
	}

	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return Token{}, Identity{}, errors.New("start OAuth callback listener")
	}
	defer listener.Close()

	if redirectURI == "" {
		port := listener.Addr().(*net.TCPAddr).Port
		redirectURI = fmt.Sprintf("http://localhost:%d%s", port, path)
		expectedHost = fmt.Sprintf("localhost:%d", port)
	}

	exchange := make(chan loginOutcome, 1)
	var once sync.Once

	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != path || !callbackHostMatches(r.Host, expectedHost) {
			writeCallbackPage(w, "Awaiting authentication")
			return
		}

		query := r.URL.Query()
		if query.Get("state") != state {
			writeCallbackPage(w, "Awaiting authentication")
			return
		}

		if query.Get("error") != "" {
			message := query.Get("error_description")
			if message == "" {
				message = query.Get("error")
			}
			once.Do(func() {
				exchange <- loginOutcome{err: fmt.Errorf("authorization failed: %s", message)}
			})
			writeCallbackPage(w, "Authentication failed")
			return
		}

		code := strings.TrimSpace(query.Get("code"))
		if code == "" {
			once.Do(func() {
				exchange <- loginOutcome{err: errors.New("callback did not include code")}
			})
			writeCallbackPage(w, "Authentication failed")
			return
		}

		token, tokenErr := c.exchangeAuthCode(r.Context(), provider, code, redirectURI, verifier, state)
		if tokenErr != nil {
			once.Do(func() { exchange <- loginOutcome{err: tokenErr} })
			writeCallbackPage(w, "Authentication failed")
			return
		}
		once.Do(func() { exchange <- loginOutcome{token: token} })
		writeCallbackPage(w, "Authentication complete")
	})

	httpServer := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	serverErrors := make(chan error, 1)
	go func() {
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutdownCancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	authorizeURL, err := buildAuthURL(provider, redirectURI, challenge, state)
	if err != nil {
		return Token{}, Identity{}, err
	}

	opener := c.OpenBrowser
	if opener == nil {
		opener = defaultOpenBrowser
	}
	if err := opener(authorizeURL); err != nil {
		return Token{}, Identity{}, err
	}

	var outcome loginOutcome
	select {
	case outcome = <-exchange:
	case err := <-serverErrors:
		return Token{}, Identity{}, fmt.Errorf("oauth callback server: %w", err)
	case <-tctx.Done():
		return Token{}, Identity{}, errors.New("OAuth login timed out or was cancelled")
	}

	if outcome.err != nil {
		return Token{}, Identity{}, outcome.err
	}

	identity, err := c.Identify(tctx, string(provider), outcome.token)
	if err != nil {
		return Token{}, Identity{}, err
	}
	return outcome.token, identity, nil
}

type loginOutcome struct {
	token Token
	err   error
}

func (c Client) Refresh(ctx context.Context, p string, token Token) (Token, error) {
	provider, err := canonicalProvider(p)
	if err != nil {
		return Token{}, err
	}
	if strings.TrimSpace(token.RefreshToken) == "" {
		return Token{}, errors.New("missing refresh token")
	}

	var req tokenResponse
	switch provider {
	case providerAnthropic:
		req, err = c.fetchToken(ctx, anthropicTokenURL, "application/json", map[string]any{
			"grant_type":    "refresh_token",
			"refresh_token": token.RefreshToken,
			"client_id":     anthropicClientID,
			"scope":         anthropicScope,
		})
	case providerOpenAI:
		form := url.Values{
			"grant_type":    {"refresh_token"},
			"refresh_token": {token.RefreshToken},
			"client_id":     {openAIClientID},
		}
		req, err = c.fetchToken(ctx, openAITokenURL, "application/x-www-form-urlencoded", form)
	}
	if err != nil {
		return Token{}, err
	}
	if strings.TrimSpace(req.AccessToken) == "" {
		return Token{}, errors.New("token response missing access token")
	}
	refresh := req.RefreshToken
	if refresh == "" {
		refresh = token.RefreshToken
	}

	token = Token{
		AccessToken:  req.AccessToken,
		RefreshToken: refresh,
		IDToken:      req.IDToken,
		ExpiresAt:    time.Now().UTC().Add(durationFromExpiresIn(req.ExpiresIn)),
	}
	return token, nil
}

func (c Client) Identify(ctx context.Context, p string, token Token) (Identity, error) {
	provider, err := canonicalProvider(p)
	if err != nil {
		return Identity{}, err
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return Identity{}, errors.New("missing access token")
	}

	switch provider {
	case providerAnthropic:
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, anthropicProfile, nil)
		if err != nil {
			return Identity{}, fmt.Errorf("build Anthropic identity request: %w", err)
		}
		request.Header.Set("Accept", "application/json")
		request.Header.Set("Authorization", "Bearer "+token.AccessToken)
		request.Header.Set("anthropic-version", "2023-06-01")
		request.Header.Set("anthropic-beta", "oauth-2025-04-20")

		response, err := c.executeRequest(request)
		if err != nil {
			return Identity{}, err
		}
		defer response.Body.Close()

		var profile anthropicProfileResponse
		if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&profile); err != nil {
			return Identity{}, errors.New("identity response was invalid")
		}
		identity := Identity{
			Subject:      strings.TrimSpace(profile.Account.UUID),
			Email:        strings.TrimSpace(profile.Account.Email),
			Organization: strings.TrimSpace(profile.Organization.UUID),
		}
		if identity.Subject == "" {
			return Identity{}, errors.New("identity missing subject")
		}
		if identity.Email == "" {
			return Identity{}, errors.New("identity missing email")
		}
		return identity, nil
	case providerOpenAI:
		claims, ok := parseJWTClaims(token.IDToken)
		if !ok {
			claims, ok = parseJWTClaims(token.AccessToken)
		}
		if !ok {
			return Identity{}, errors.New("identity token could not be verified")
		}

		identity := Identity{
			Subject:      cleanClaim(claims["sub"]),
			Email:        cleanClaim(claims["email"]),
			Organization: cleanClaim(orgFromOpenAIClaims(claims)),
		}
		if identity.Subject == "" {
			if auth, ok := claims["https://api.openai.com/auth"].(map[string]any); ok {
				identity.Subject = cleanClaim(auth["chatgpt_account_id"])
				if identity.Organization == "" {
					identity.Organization = cleanClaim(auth["organization_id"])
				}
			}
		}
		if identity.Email == "" {
			if profile, ok := claims["https://api.openai.com/profile"].(map[string]any); ok {
				identity.Email = cleanClaim(profile["email"])
			}
			if identity.Email == "" {
				identity.Email = cleanClaim(claims["https://api.openai.com/profile.email"])
			}
		}
		if identity.Subject == "" {
			return Identity{}, errors.New("identity missing subject")
		}
		if identity.Email == "" {
			return Identity{}, errors.New("identity missing email")
		}
		return identity, nil
	default:
		return Identity{}, fmt.Errorf("unsupported provider: %s", provider)
	}
}

func (c Client) exchangeAuthCode(ctx context.Context, provider provider, code, redirectURI, verifier, state string) (Token, error) {
	var response tokenResponse
	var err error
	switch provider {
	case providerAnthropic:
		response, err = c.fetchToken(ctx, anthropicTokenURL, "application/json", map[string]any{
			"grant_type":    "authorization_code",
			"code":          code,
			"redirect_uri":  redirectURI,
			"client_id":     anthropicClientID,
			"code_verifier": verifier,
			"state":         state,
		})
	case providerOpenAI:
		response, err = c.fetchToken(ctx, openAITokenURL, "application/x-www-form-urlencoded", url.Values{
			"grant_type":    {"authorization_code"},
			"code":          {code},
			"redirect_uri":  {redirectURI},
			"client_id":     {openAIClientID},
			"code_verifier": {verifier},
		})
	default:
		return Token{}, fmt.Errorf("unsupported provider: %s", provider)
	}
	if err != nil {
		return Token{}, err
	}

	if strings.TrimSpace(response.AccessToken) == "" {
		return Token{}, errors.New("token response missing access token")
	}
	if strings.TrimSpace(response.RefreshToken) == "" {
		return Token{}, errors.New("token response missing refresh token")
	}
	return Token{
		AccessToken:  response.AccessToken,
		RefreshToken: response.RefreshToken,
		IDToken:      response.IDToken,
		ExpiresAt:    time.Now().UTC().Add(durationFromExpiresIn(response.ExpiresIn)),
	}, nil
}

func (c Client) fetchToken(ctx context.Context, endpoint, contentType string, body any) (tokenResponse, error) {
	requestBody, err := requestBodyForToken(contentType, body)
	if err != nil {
		return tokenResponse{}, err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(requestBody))
	if err != nil {
		return tokenResponse{}, fmt.Errorf("build token request: %w", err)
	}
	request.Header.Set("Content-Type", contentType)

	response, err := c.httpClient().Do(request)
	if err != nil {
		return tokenResponse{}, errors.New("token request could not be completed")
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusUnauthorized {
		return tokenResponse{}, fmt.Errorf("%w", ErrReauth)
	}
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
		return tokenResponse{}, errors.New("token provider temporarily unavailable")
	}

	var parsed tokenResponse
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_ = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&parsed)
		if parsed.Error == "invalid_grant" {
			return tokenResponse{}, fmt.Errorf("%w", ErrReauth)
		}
		return tokenResponse{}, errors.New("token request failed")
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&parsed); err != nil {
		return tokenResponse{}, errors.New("token response was invalid")
	}
	return parsed, nil
}

func requestBodyForToken(contentType string, body any) (string, error) {
	switch contentType {
	case "application/json":
		encoded, err := json.Marshal(body)
		if err != nil {
			return "", fmt.Errorf("encode token request: %w", err)
		}
		return string(encoded), nil
	case "application/x-www-form-urlencoded":
		values, ok := body.(url.Values)
		if !ok {
			return "", errors.New("invalid token request body")
		}
		return values.Encode(), nil
	default:
		return "", fmt.Errorf("unsupported token body content type: %s", contentType)
	}
}

func (c Client) executeRequest(request *http.Request) (*http.Response, error) {
	response, err := c.httpClient().Do(request)
	if err != nil {
		return nil, errors.New("request could not be completed")
	}
	if response.StatusCode == http.StatusUnauthorized {
		_ = response.Body.Close()
		return nil, fmt.Errorf("%w", ErrReauth)
	}
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
		_ = response.Body.Close()
		return nil, errors.New("request temporarily unavailable")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_ = response.Body.Close()
		return nil, errors.New("request failed")
	}
	return response, nil
}

func buildAuthURL(provider provider, redirectURI, challenge, state string) (string, error) {
	query := url.Values{
		"response_type":         {"code"},
		"redirect_uri":          {redirectURI},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
	}

	switch provider {
	case providerAnthropic:
		query.Set("code", "true")
		query.Set("client_id", anthropicClientID)
		query.Set("scope", anthropicScope)
		return anthropicAuthURL + "?" + query.Encode(), nil
	case providerOpenAI:
		query.Set("client_id", openAIClientID)
		query.Set("scope", openAIScope)
		query.Set("id_token_add_organizations", "true")
		query.Set("originator", "Codex Desktop")
		query.Set("codex_cli_simplified_flow", "true")
		return openAIOAuthURL + "?" + query.Encode(), nil
	default:
		return "", fmt.Errorf("unsupported provider: %s", provider)
	}
}

func (c Client) httpClient() *http.Client {
	if c.HTTP == nil {
		return &http.Client{
			Transport:     &http.Transport{Proxy: http.ProxyFromEnvironment},
			Timeout:       30 * time.Second,
			CheckRedirect: disallowRedirects,
		}
	}

	client := *c.HTTP
	if client.Timeout == 0 {
		client.Timeout = 30 * time.Second
	}
	if client.CheckRedirect == nil {
		client.CheckRedirect = disallowRedirects
	} else {
		prev := client.CheckRedirect
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if err := prev(req, via); err != nil {
				return err
			}
			return disallowRedirects(req, via)
		}
	}
	if client.Transport == nil {
		client.Transport = &http.Transport{Proxy: http.ProxyFromEnvironment}
	}
	return &client
}

func disallowRedirects(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

func cleanClaim(v any) string {
	claim, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(claim)
}

func orgFromOpenAIClaims(claims map[string]any) string {
	if organizations, ok := claims["organizations"].([]any); ok {
		for _, raw := range organizations {
			organization, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if value := cleanClaim(organization["id"]); value != "" {
				return value
			}
			if value := cleanClaim(organization["organization_id"]); value != "" {
				return value
			}
		}
	}
	if auth, ok := claims["https://api.openai.com/auth"].(map[string]any); ok {
		if value := cleanClaim(auth["organization_id"]); value != "" {
			return value
		}
	}
	return cleanClaim(claims["https://api.openai.com/profile.organization"])
}

func parseJWTClaims(value string) (map[string]any, bool) {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return nil, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payload, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return nil, false
		}
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, false
	}
	return claims, true
}

func durationFromExpiresIn(expiresIn int64) time.Duration {
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	return time.Duration(expiresIn) * time.Second
}

func writeCallbackPage(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte("<!doctype html><meta charset=utf-8><title>nosnitch</title><body>" + message + "</body>"))
}

func callbackHostMatches(actual, expected string) bool {
	if actual == expected {
		return true
	}

	expectedHost, expectedPort, err := net.SplitHostPort(expected)
	if err != nil {
		return false
	}
	actualHost, actualPort, err := net.SplitHostPort(actual)
	if err != nil || actualPort != expectedPort {
		return false
	}

	return isLoopbackHost(actualHost) && isLoopbackHost(expectedHost)
}

func isLoopbackHost(host string) bool {
	switch strings.Trim(host, "[]") {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

func defaultOpenBrowser(target string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", target)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		command = exec.Command("xdg-open", target)
	}
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}

func canonicalProvider(value string) (provider, error) {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case providerAnthropic:
		return providerAnthropic, nil
	case providerOpenAI:
		return providerOpenAI, nil
	default:
		return "", fmt.Errorf("unsupported provider: %s", value)
	}
}

func randomString(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func defaultContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, defaultTimeout)
}
