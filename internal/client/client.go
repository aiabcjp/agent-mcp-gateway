// Package client provides client-side logic for connecting to the MCP gateway,
// including bootstrapping, version checking, proxy forwarding, and OAuth2 PKCE
// authentication.
package client

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"agent-gateway/internal/config"
	"agent-gateway/internal/resources"
)

// BootstrapResponse is the response from the server's /api/bootstrap endpoint.
type BootstrapResponse struct {
	Resources []resources.ResourceInfo `json:"resources"`
	Version   config.VersionConfig     `json:"version"`
}

// Connect performs the full client connection flow:
//  1. Call Bootstrap to get server configuration.
//  2. Check version compatibility.
//  3. Start local MCP proxy.
func Connect(serverURL, token string, logger *slog.Logger) error {
	bootstrap, err := Bootstrap(serverURL, token)
	if err != nil {
		return fmt.Errorf("bootstrap failed: %w", err)
	}

	logger.Info("connected to gateway",
		"resources", len(bootstrap.Resources),
		"server_version", bootstrap.Version.Latest,
	)

	// Check version compatibility.
	if err := CheckVersion("dev", bootstrap.Version); err != nil {
		logger.Warn("version check", "warning", err)
	}

	// Start local proxy.
	listenAddr := "127.0.0.1:8081"
	logger.Info("starting local proxy", "addr", listenAddr)

	ctx := context.Background()
	return StartProxy(ctx, listenAddr, serverURL, token, logger)
}

// Bootstrap calls the server's /api/bootstrap endpoint with the given token
// and returns the parsed configuration response.
func Bootstrap(serverURL, token string) (*BootstrapResponse, error) {
	endpoint := strings.TrimRight(serverURL, "/") + "/api/bootstrap"

	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting bootstrap: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bootstrap returned status %d", resp.StatusCode)
	}

	var result BootstrapResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding bootstrap response: %w", err)
	}

	return &result, nil
}

// StartProxy starts a local HTTP reverse proxy on listenAddr that forwards MCP
// requests to the remote gateway at serverURL, injecting the Bearer token into
// every forwarded request.
func StartProxy(ctx context.Context, listenAddr, serverURL, token string, logger *slog.Logger) error {
	target, err := url.Parse(serverURL)
	if err != nil {
		return fmt.Errorf("parsing server URL: %w", err)
	}

	proxy := httputil.NewSingleHostReverseProxy(target)

	// Wrap the default director to inject the authorization header.
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Header.Set("Authorization", "Bearer "+token)
	}

	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           proxy,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("local proxy listening", "addr", listenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

// CheckVersion compares clientVersion against the server's version requirements.
// Returns an error if the client version is below the minimum required version.
// Logs a warning-level message (via the returned error string) if an update is
// available but not required.
func CheckVersion(clientVersion string, required config.VersionConfig) error {
	if clientVersion == "dev" || required.Minimum == "" {
		return nil
	}

	cmp, err := compareSemver(clientVersion, required.Minimum)
	if err != nil {
		return fmt.Errorf("parsing version: %w", err)
	}
	if cmp < 0 {
		return fmt.Errorf("client version %s is below minimum required %s — update required (download: %s)",
			clientVersion, required.Minimum, required.DownloadURL)
	}

	if required.Latest != "" {
		cmp, err := compareSemver(clientVersion, required.Latest)
		if err != nil {
			return nil // If we can't parse, don't block.
		}
		if cmp < 0 {
			return fmt.Errorf("update available: %s -> %s (download: %s)",
				clientVersion, required.Latest, required.DownloadURL)
		}
	}

	return nil
}

// compareSemver compares two semantic version strings (major.minor.patch).
// Returns -1 if a < b, 0 if a == b, 1 if a > b.
func compareSemver(a, b string) (int, error) {
	aParts, err := parseSemver(a)
	if err != nil {
		return 0, err
	}
	bParts, err := parseSemver(b)
	if err != nil {
		return 0, err
	}

	for i := 0; i < 3; i++ {
		if aParts[i] < bParts[i] {
			return -1, nil
		}
		if aParts[i] > bParts[i] {
			return 1, nil
		}
	}
	return 0, nil
}

// parseSemver splits a version string like "1.2.3" into [1, 2, 3].
func parseSemver(v string) ([3]int, error) {
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 3)
	var result [3]int
	for i := 0; i < len(parts) && i < 3; i++ {
		// Strip any pre-release suffix (e.g., "3-beta").
		clean := parts[i]
		if idx := strings.IndexByte(clean, '-'); idx >= 0 {
			clean = clean[:idx]
		}
		n, err := strconv.Atoi(clean)
		if err != nil {
			return result, fmt.Errorf("invalid version component %q: %w", parts[i], err)
		}
		result[i] = n
	}
	return result, nil
}

// StartPKCEFlow initiates an OAuth2 PKCE authentication flow. It:
//  1. Generates a cryptographic code verifier and challenge.
//  2. Starts a local HTTP server to receive the authorization callback.
//  3. Opens the authorization URL in the user's browser.
//  4. Waits for the callback with the authorization code.
//  5. Exchanges the code for an access token.
//  6. Returns the access token.
func StartPKCEFlow(issuer, clientID string, scopes []string) (string, error) {
	// 1. Generate PKCE code verifier (32 random bytes, base64url-encoded).
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		return "", fmt.Errorf("generating code verifier: %w", err)
	}
	codeVerifier := base64.RawURLEncoding.EncodeToString(verifierBytes)

	// 2. Compute code challenge (SHA256 of verifier, base64url-encoded).
	h := sha256.Sum256([]byte(codeVerifier))
	codeChallenge := base64.RawURLEncoding.EncodeToString(h[:])

	// 3. Start local callback server.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("starting callback listener: %w", err)
	}
	callbackPort := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", callbackPort)

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code == "" {
			errCh <- fmt.Errorf("no authorization code in callback")
			http.Error(w, "Missing code parameter", http.StatusBadRequest)
			return
		}
		codeCh <- code
		_, _ = fmt.Fprint(w, "<html><body><h1>Authentication successful!</h1><p>You can close this window.</p></body></html>")
	})

	callbackServer := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		if err := callbackServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("callback server: %w", err)
		}
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = callbackServer.Shutdown(shutdownCtx)
	}()

	// 4. Build authorization URL.
	authURL := fmt.Sprintf("%s/v1/authorize?"+
		"response_type=code&"+
		"client_id=%s&"+
		"redirect_uri=%s&"+
		"scope=%s&"+
		"code_challenge=%s&"+
		"code_challenge_method=S256",
		strings.TrimRight(issuer, "/"),
		url.QueryEscape(clientID),
		url.QueryEscape(redirectURI),
		url.QueryEscape(strings.Join(scopes, " ")),
		url.QueryEscape(codeChallenge),
	)

	// 5. Open browser.
	if err := openBrowser(authURL); err != nil {
		// Fall back to printing the URL for the user.
		fmt.Printf("Open this URL in your browser:\n%s\n", authURL)
	}

	// 6. Wait for authorization code.
	var authCode string
	select {
	case authCode = <-codeCh:
	case err := <-errCh:
		return "", err
	case <-time.After(5 * time.Minute):
		return "", fmt.Errorf("authentication timed out")
	}

	// 7. Exchange code for tokens.
	tokenURL := strings.TrimRight(issuer, "/") + "/v1/token"
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {authCode},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"code_verifier": {codeVerifier},
	}

	resp, err := http.PostForm(tokenURL, data)
	if err != nil {
		return "", fmt.Errorf("exchanging code for token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token exchange returned status %d", resp.StatusCode)
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("decoding token response: %w", err)
	}

	return tokenResp.AccessToken, nil
}

// openBrowser opens the given URL in the user's default browser.
func openBrowser(u string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{u}
	case "linux":
		cmd = "xdg-open"
		args = []string{u}
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", u}
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	return exec.Command(cmd, args...).Start()
}
