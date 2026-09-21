package config

import (
	"testing"
)

func TestProxyConfigDisabledByDefault(t *testing.T) {
	t.Setenv("TRUST_PROXY", "")
	t.Setenv("TRUSTED_PROXIES", "172.16.0.0/12")

	cfg := Load()

	enable, proxies := cfg.ProxyConfig()
	if enable {
		t.Error("proxy trust must be off unless explicitly enabled")
	}
	if proxies != nil {
		t.Errorf("trusted proxies must be ignored when disabled, got %v", proxies)
	}
}

func TestProxyConfigEnabled(t *testing.T) {
	t.Setenv("TRUST_PROXY", "true")
	t.Setenv("TRUSTED_PROXIES", "10.0.0.0/8, 172.16.0.0/12 ,192.168.0.1")

	cfg := Load()

	enable, proxies := cfg.ProxyConfig()
	if !enable {
		t.Fatal("expected proxy trust to be enabled")
	}
	if len(proxies) != 3 {
		t.Fatalf("expected 3 trusted proxies, got %v", proxies)
	}
	if proxies[0] != "10.0.0.0/8" || proxies[1] != "172.16.0.0/12" || proxies[2] != "192.168.0.1" {
		t.Errorf("trusted proxies not parsed/trimmed correctly: %v", proxies)
	}
}

func TestProxyConfigEnabledWithoutListStaysSafe(t *testing.T) {
	t.Setenv("TRUST_PROXY", "true")
	t.Setenv("TRUSTED_PROXIES", "")

	cfg := Load()
	enable, proxies := cfg.ProxyConfig()

	// Even when enabled, an empty list must surface as empty so the caller never
	// ends up trusting every X-Forwarded-For header.
	if !enable {
		t.Error("explicit opt-in should report enabled")
	}
	if len(proxies) != 0 {
		t.Errorf("empty list must produce no proxies, got %v", proxies)
	}
}

func TestEnvBoolParsing(t *testing.T) {
	cases := map[string]bool{
		"1": true, "true": true, "TRUE": true, "yes": true, "on": true,
		"0": false, "false": false, "no": false, "off": false, "": false, "banana": false,
	}

	for raw, want := range cases {
		t.Setenv("TRUST_PROXY", raw)
		if got := Load().TrustProxy; got != want {
			t.Errorf("TRUST_PROXY=%q parsed as %v, want %v", raw, got, want)
		}
	}
}

func TestCookieSecureDefault(t *testing.T) {
	t.Setenv("COOKIE_SECURE", "")
	if Load().CookieSecure {
		t.Error("COOKIE_SECURE must default to false for local development")
	}

	t.Setenv("COOKIE_SECURE", "true")
	if !Load().CookieSecure {
		t.Error("COOKIE_SECURE=true must be honoured")
	}
}

func TestOAuthEnabledRequiresBothValues(t *testing.T) {
	t.Setenv("GITHUB_OAUTH_CLIENT_ID", "")
	t.Setenv("GITHUB_OAUTH_CLIENT_SECRET", "")
	if Load().OAuthEnabled() {
		t.Error("missing credentials must disable OAuth")
	}

	t.Setenv("GITHUB_OAUTH_CLIENT_ID", "id")
	t.Setenv("GITHUB_OAUTH_CLIENT_SECRET", "secret")

	cfg := Load()
	if !cfg.OAuthEnabled() {
		t.Error("both credentials present must enable OAuth")
	}

	// The redirect URL is derived from the public base URL, so it must line up.
	want := cfg.PublicBaseURL + "/auth/github/callback"
	if got := cfg.OAuthConfig().RedirectURL; got != want {
		t.Errorf("RedirectURL = %q, want %q", got, want)
	}
}

func TestPublicBaseURLTrimsTrailingSlash(t *testing.T) {
	t.Setenv("PUBLIC_BASE_URL", "https://ggstar.example//")

	if got := Load().PublicBaseURL; got != "https://ggstar.example" {
		t.Errorf("PublicBaseURL = %q, trailing slashes should be trimmed", got)
	}
}
