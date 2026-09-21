package auth

import (
	"strings"
	"testing"
)

func testConfig() Config {
	return Config{
		ClientID:     "client-123",
		ClientSecret: "secret-456",
		RedirectURL:  "https://example.com/auth/github/callback",
	}
}

func TestConfigEnabled(t *testing.T) {
	if !testConfig().Enabled() {
		t.Error("config with both credentials should be enabled")
	}

	if (Config{ClientID: "x"}).Enabled() {
		t.Error("missing client secret must disable sign-in")
	}
	if (Config{ClientSecret: "x"}).Enabled() {
		t.Error("missing client id must disable sign-in")
	}
	if (Config{}).Enabled() {
		t.Error("empty config must disable sign-in")
	}
}

func TestAuthorizeURLRequiresCredentials(t *testing.T) {
	c := New(Config{})

	if _, err := c.AuthorizeURL("state"); err != ErrNotConfigured {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestAuthorizeURLContainsRequiredParams(t *testing.T) {
	c := New(testConfig())

	got, err := c.AuthorizeURL("state-abc")
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"https://github.com/login/oauth/authorize",
		"client_id=client-123",
		"state=state-abc",
		"scope=read%3Auser",
		"redirect_uri=https%3A%2F%2Fexample.com%2Fauth%2Fgithub%2Fcallback",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("authorize URL missing %q\n%s", want, got)
		}
	}

	// The client secret must never travel in the browser-facing URL.
	if strings.Contains(got, "secret-456") {
		t.Error("client secret must not appear in the authorize URL")
	}
}

// TestScopeIsReadOnly guards the promise that ggstar never requests write access.
func TestScopeIsReadOnly(t *testing.T) {
	if Scope != "read:user" {
		t.Errorf("scope = %q; the app must only request read:user", Scope)
	}
	if strings.Contains(Scope, "repo") || strings.Contains(Scope, "write") || strings.Contains(Scope, "delete") {
		t.Errorf("scope %q requests write access", Scope)
	}
}

func TestNewStateIsRandomAndURLSafe(t *testing.T) {
	seen := map[string]bool{}

	for i := 0; i < 200; i++ {
		state, err := NewState()
		if err != nil {
			t.Fatal(err)
		}
		if len(state) < 32 {
			t.Fatalf("state %q is too short to resist guessing", state)
		}
		for _, r := range state {
			if !strings.ContainsRune("0123456789abcdef", r) {
				t.Fatalf("state %q contains a non URL-safe character %q", state, r)
			}
		}
		if seen[state] {
			t.Fatalf("NewState returned a duplicate: %q", state)
		}
		seen[state] = true
	}
}

func TestExchangeRejectsMissingCode(t *testing.T) {
	c := New(testConfig())

	if _, _, err := c.Exchange(t.Context(), ""); err == nil {
		t.Error("an empty authorization code must be rejected")
	}
}

func TestExchangeRequiresCredentials(t *testing.T) {
	c := New(Config{})

	if _, _, err := c.Exchange(t.Context(), "code"); err != ErrNotConfigured {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}
