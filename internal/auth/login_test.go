package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"pi-go/internal/config"
)

func useTempAgentDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(config.AgentDirEnv, dir)
	return dir
}

func fakeJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding
	return enc.EncodeToString([]byte(`{"alg":"none"}`)) + "." + enc.EncodeToString(payload) + ".sig"
}

func TestSaveAPIKey(t *testing.T) {
	dir := useTempAgentDir(t)
	existing := `{"anthropic":{"type":"api_key","key":"keep-me","extra":"field"}}`
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := SaveAPIKey("openai", "  sk-test \n"); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("auth.json mode = %o, want 600", perm)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "auth.json"))
	var got map[string]map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["openai"]["type"] != "api_key" || got["openai"]["key"] != "sk-test" {
		t.Errorf("openai entry = %v", got["openai"])
	}
	if got["anthropic"]["key"] != "keep-me" || got["anthropic"]["extra"] != "field" {
		t.Errorf("unrelated entry changed: %v", got["anthropic"])
	}

	cred, err := ResolveAPIKey("openai")
	if err != nil || cred.Key != "sk-test" || cred.Source != "auth.json" {
		t.Errorf("ResolveAPIKey = %+v, %v", cred, err)
	}
}

func TestSaveAPIKeyRejects(t *testing.T) {
	useTempAgentDir(t)
	for name, tc := range map[string][2]string{
		"empty key":      {"openai", "  "},
		"oauth provider": {"openai-codex", "sk-x"},
		"unknown":        {"nope", "sk-x"},
	} {
		if err := SaveAPIKey(tc[0], tc[1]); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestResolveOAuthProvider(t *testing.T) {
	dir := useTempAgentDir(t)

	if _, err := ResolveAPIKey("openai-codex"); err == nil || !strings.Contains(err.Error(), "pi-go auth login openai-codex") {
		t.Errorf("missing credential error = %v", err)
	}

	write := func(expires time.Time) {
		cred := map[string]storedCredential{"openai-codex": {Type: "oauth", Access: "tok", Refresh: "r", Expires: expires.UnixMilli()}}
		data, _ := json.Marshal(cred)
		if err := os.WriteFile(filepath.Join(dir, "auth.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write(time.Now().Add(time.Hour))
	cred, err := ResolveAPIKey("openai-codex")
	if err != nil || cred.Key != "tok" {
		t.Errorf("valid token: %+v, %v", cred, err)
	}

	write(time.Now().Add(-time.Hour))
	if _, err := ResolveAPIKey("openai-codex"); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Errorf("expired token error = %v", err)
	}
}

func TestPKCE(t *testing.T) {
	verifier, challenge, err := pkcePair()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(verifier))
	if want := base64.RawURLEncoding.EncodeToString(sum[:]); challenge != want {
		t.Errorf("challenge = %q, want %q", challenge, want)
	}
	if strings.ContainsAny(verifier+challenge, "=+/") {
		t.Errorf("not base64url without padding: %q %q", verifier, challenge)
	}
}

func TestParseAuthorizationInput(t *testing.T) {
	for _, tc := range []struct{ in, code, state string }{
		{"http://localhost:1455/auth/callback?code=abc&state=xyz", "abc", "xyz"},
		{"abc#xyz", "abc", "xyz"},
		{"code=abc&state=xyz", "abc", "xyz"},
		{"?code=abc&state=xyz", "abc", "xyz"},
		{"  abc \n", "abc", ""},
		{"", "", ""},
	} {
		code, state := parseAuthorizationInput(tc.in)
		if code != tc.code || state != tc.state {
			t.Errorf("parse(%q) = %q, %q; want %q, %q", tc.in, code, state, tc.code, tc.state)
		}
	}
}

func TestCodexAccountID(t *testing.T) {
	good := fakeJWT(t, map[string]any{codexJWTClaim: map[string]any{"chatgpt_account_id": "acct-1"}})
	if id, err := codexAccountID(good); err != nil || id != "acct-1" {
		t.Errorf("got %q, %v", id, err)
	}
	for name, tok := range map[string]string{
		"no claim":   fakeJWT(t, map[string]any{"sub": "x"}),
		"not a jwt":  "opaque",
		"bad base64": "a.!!!.c",
	} {
		if _, err := codexAccountID(tok); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

// codexServer fakes the token endpoint and records the form it received.
func codexServer(t *testing.T, form *url.Values) {
	t.Helper()
	access := fakeJWT(t, map[string]any{codexJWTClaim: map[string]any{"chatgpt_account_id": "acct-1"}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		*form = r.PostForm
		json.NewEncoder(w).Encode(map[string]any{"access_token": access, "refresh_token": "refresh-1", "expires_in": 3600})
	}))
	t.Cleanup(srv.Close)

	old := codexTokenURL
	codexTokenURL = srv.URL
	t.Cleanup(func() { codexTokenURL = old })

	oldOpen := openBrowserFunc
	openBrowserFunc = func(string) {}
	t.Cleanup(func() { openBrowserFunc = oldOpen })
}

// stateFromOutput pulls the state parameter out of the printed authorize URL.
func stateFromOutput(t *testing.T, out string) string {
	t.Helper()
	for _, f := range strings.Fields(out) {
		if strings.HasPrefix(f, codexAuthorizeURL) {
			u, err := url.Parse(f)
			if err != nil {
				t.Fatal(err)
			}
			return u.Query().Get("state")
		}
	}
	t.Fatalf("no authorize URL in %q", out)
	return ""
}

func TestLoginCodexPaste(t *testing.T) {
	dir := useTempAgentDir(t)
	var form url.Values
	codexServer(t, &form)

	// The state is random, so feed the pasted line from a pipe once the URL is printed.
	pr, pw, _ := os.Pipe()
	defer pr.Close()
	var out syncBuffer
	done := make(chan error, 1)
	go func() { done <- LoginOAuth(context.Background(), "openai-codex", pr, &out) }()

	var state string
	for i := 0; i < 200 && state == ""; i++ {
		if s := out.String(); strings.Contains(s, codexAuthorizeURL) {
			state = stateFromOutput(t, s)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if state == "" {
		t.Fatal("authorize URL never printed")
	}
	pw.WriteString(codexRedirectURI + "?code=the-code&state=" + state + "\n")
	pw.Close()

	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if form.Get("code") != "the-code" || form.Get("grant_type") != "authorization_code" || form.Get("code_verifier") == "" {
		t.Errorf("token request form = %v", form)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "auth.json"))
	var got map[string]storedCredential
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	c := got["openai-codex"]
	if c.Type != "oauth" || c.Refresh != "refresh-1" || c.AccountID != "acct-1" || c.Expires <= time.Now().UnixMilli() {
		t.Errorf("stored credential = %+v", c)
	}
	if _, err := ResolveAPIKey("openai-codex"); err != nil {
		t.Errorf("resolve after login: %v", err)
	}
}

func TestLoginCodexStateMismatch(t *testing.T) {
	dir := useTempAgentDir(t)
	var form url.Values
	codexServer(t, &form)

	in := strings.NewReader(codexRedirectURI + "?code=c&state=wrong\n")
	err := LoginOAuth(context.Background(), "openai-codex", in, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "state mismatch") {
		t.Fatalf("error = %v, want state mismatch", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "auth.json")); statErr == nil {
		t.Error("auth.json written despite failed login")
	}
}

func TestLoginOAuthRejectsAPIKeyProvider(t *testing.T) {
	if err := LoginOAuth(context.Background(), "openai", strings.NewReader(""), &bytes.Buffer{}); err == nil {
		t.Fatal("want error")
	}
}

// syncBuffer is a bytes.Buffer safe for the login goroutine to write while the
// test reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
