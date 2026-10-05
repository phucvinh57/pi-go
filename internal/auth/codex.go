package auth

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// OpenAI Codex (ChatGPT Plus/Pro) OAuth: authorization code flow with PKCE,
// using the public client ID and loopback redirect of the Codex CLI.
const (
	codexClientID     = "app_EMoamEEZ73f0CkXaXp7hrann"
	codexAuthorizeURL = "https://auth.openai.com/oauth/authorize"
	codexRedirectURI  = "http://localhost:1455/auth/callback"
	codexListenAddr   = "127.0.0.1:1455"
	codexCallbackPath = "/auth/callback"
	codexScope        = "openid profile email offline_access"
	codexJWTClaim     = "https://api.openai.com/auth"
)

// codexTokenURL is a variable so tests can point it at a local server.
var codexTokenURL = "https://auth.openai.com/oauth/token"

var httpClient = &http.Client{Timeout: 30 * time.Second}

// pkcePair returns a PKCE code verifier and its S256 challenge.
func pkcePair() (verifier, challenge string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

func randomState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func codexAuthorizeLink(challenge, state string) string {
	q := url.Values{
		"response_type":              {"code"},
		"client_id":                  {codexClientID},
		"redirect_uri":               {codexRedirectURI},
		"scope":                      {codexScope},
		"code_challenge":             {challenge},
		"code_challenge_method":      {"S256"},
		"state":                      {state},
		"id_token_add_organizations": {"true"},
		"codex_cli_simplified_flow":  {"true"},
		"originator":                 {"pi"},
	}
	return codexAuthorizeURL + "?" + q.Encode()
}

// parseAuthorizationInput extracts the code and state from what a user pastes:
// the full redirect URL, "code#state", a query string, or a bare code.
func parseAuthorizationInput(input string) (code, state string) {
	v := strings.TrimSpace(input)
	if v == "" {
		return "", ""
	}
	if u, err := url.Parse(v); err == nil && u.Scheme != "" && u.Host != "" {
		return u.Query().Get("code"), u.Query().Get("state")
	}
	if c, s, ok := strings.Cut(v, "#"); ok {
		return c, s
	}
	if strings.Contains(v, "code=") {
		if q, err := url.ParseQuery(strings.TrimPrefix(v, "?")); err == nil {
			return q.Get("code"), q.Get("state")
		}
	}
	return v, ""
}

// codexAccountID reads the ChatGPT account ID from the access token's JWT
// payload. The signature is not checked: the token came straight from the
// token endpoint and the ID is only a request header.
func codexAccountID(accessToken string) (string, error) {
	parts := strings.Split(accessToken, ".")
	if len(parts) != 3 {
		return "", errors.New("access token is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return "", fmt.Errorf("decode access token: %w", err)
	}
	var claims map[string]json.RawMessage
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("decode access token: %w", err)
	}
	var auth struct {
		AccountID string `json:"chatgpt_account_id"`
	}
	if err := json.Unmarshal(claims[codexJWTClaim], &auth); err != nil || auth.AccountID == "" {
		return "", errors.New("access token has no ChatGPT account ID")
	}
	return auth.AccountID, nil
}

// codexExchange trades an authorization code for tokens. Errors carry the
// status and the server's message, never the code, verifier or tokens we sent.
func codexExchange(ctx context.Context, code, verifier string) (storedCredential, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {codexClientID},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {codexRedirectURI},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return storedCredential{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpClient.Do(req)
	if err != nil {
		return storedCredential{}, fmt.Errorf("token exchange: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return storedCredential{}, fmt.Errorf("token exchange: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return storedCredential{}, fmt.Errorf("token exchange failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var tok struct {
		Access    string `json:"access_token"`
		Refresh   string `json:"refresh_token"`
		ExpiresIn *int64 `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return storedCredential{}, fmt.Errorf("token exchange: invalid response: %w", err)
	}
	if tok.Access == "" || tok.Refresh == "" || tok.ExpiresIn == nil {
		return storedCredential{}, errors.New("token exchange: response is missing access_token, refresh_token or expires_in")
	}
	accountID, err := codexAccountID(tok.Access)
	if err != nil {
		return storedCredential{}, err
	}
	return storedCredential{
		Type:      "oauth",
		Access:    tok.Access,
		Refresh:   tok.Refresh,
		Expires:   time.Now().UnixMilli() + *tok.ExpiresIn*1000,
		AccountID: accountID,
	}, nil
}

// callbackResult is what the loopback server or the pasted line produced.
type callbackResult struct {
	code, state string
	err         error
}

// startCodexCallbackServer listens for the browser redirect. It returns nil
// when the port is taken (the Codex CLI uses the same one); the caller then
// relies on the pasted URL alone.
func startCodexCallbackServer(state string) (results <-chan callbackResult, stop func()) {
	ln, err := net.Listen("tcp", codexListenAddr)
	if err != nil {
		return nil, func() {}
	}
	ch := make(chan callbackResult, 1)
	send := func(r callbackResult) {
		select {
		case ch <- r:
		default:
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc(codexCallbackPath, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch {
		case q.Get("state") != state:
			page(w, http.StatusBadRequest, "State mismatch.")
		case q.Get("error") != "":
			msg := q.Get("error_description")
			if msg == "" {
				msg = q.Get("error")
			}
			page(w, http.StatusBadRequest, "Authorization failed: "+msg)
			send(callbackResult{err: fmt.Errorf("authorization failed: %s", msg)})
		case q.Get("code") == "":
			page(w, http.StatusBadRequest, "Missing authorization code.")
		default:
			page(w, http.StatusOK, "Signed in. You may now close this page.")
			send(callbackResult{code: q.Get("code"), state: q.Get("state")})
		}
	})

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(ln)
	return ch, func() { srv.Close() }
}

func page(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	fmt.Fprintf(w, "<!doctype html><title>pi</title><p>%s</p>", html.EscapeString(msg))
}

// openBrowser makes a best-effort attempt to open link; failure is ignored
// because the URL is also printed.
func openBrowser(link string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", link)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", link)
	default:
		cmd = exec.Command("xdg-open", link)
	}
	if cmd.Start() == nil {
		go cmd.Wait()
	}
}

// openBrowserFunc is a variable so tests do not launch a browser.
var openBrowserFunc = openBrowser

func loginCodex(ctx context.Context, in io.Reader, out io.Writer) error {
	verifier, challenge, err := pkcePair()
	if err != nil {
		return err
	}
	state, err := randomState()
	if err != nil {
		return err
	}

	callback, stop := startCodexCallbackServer(state)
	defer stop()

	link := codexAuthorizeLink(challenge, state)
	fmt.Fprintf(out, "Open this URL to sign in with ChatGPT:\n\n  %s\n\n", link)
	openBrowserFunc(link)
	fmt.Fprintln(out, "Complete login in your browser, or paste the redirect URL here:")

	pasted := make(chan string, 1)
	go func() {
		line, err := bufio.NewReader(in).ReadString('\n')
		// At EOF with nothing typed (stdin closed), keep waiting for the browser.
		if err != nil && strings.TrimSpace(line) == "" {
			return
		}
		pasted <- line
	}()

	var res callbackResult
	select {
	case res = <-callback: // nil channel when the server could not start: blocks forever
	case line := <-pasted:
		res.code, res.state = parseAuthorizationInput(line)
		// A pasted bare code carries no state; only check one that is present.
		if res.state != "" && res.state != state {
			return errors.New("state mismatch")
		}
	case <-ctx.Done():
		return errors.New("login cancelled")
	}
	if res.err != nil {
		return res.err
	}
	if res.code == "" {
		return errors.New("missing authorization code")
	}

	cred, err := codexExchange(ctx, res.code, verifier)
	if err != nil {
		return err
	}
	return saveCredential(AgentDir(), "openai-codex", cred)
}
