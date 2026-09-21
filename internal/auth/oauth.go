// Package auth implements GitHub OAuth sign-in and the session helpers used to
// decide who may claim a GitHub profile.
//
// Trust model: the GitHub API response for GET /user is the only source of truth
// for a user's login. Nothing the browser sends about its own identity is
// believed, which is what makes "mint only your own profile" enforceable on the
// server side.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	authorizeURL = "https://github.com/login/oauth/authorize"
	tokenURL     = "https://github.com/login/oauth/access_token"
	userURL      = "https://api.github.com/user"

	// Only the identity scope is requested. ggstar never needs write access.
	Scope = "read:user"
)

var (
	// ErrNotConfigured is returned when OAuth credentials are missing.
	ErrNotConfigured = errors.New("github oauth is not configured")
	// ErrInvalidState is returned when the OAuth state parameter does not match.
	ErrInvalidState = errors.New("oauth state mismatch")
)

// Config holds the OAuth application credentials.
type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	HTTPTimeout  time.Duration
}

func (c Config) Enabled() bool {
	return c.ClientID != "" && c.ClientSecret != ""
}

// Client talks to GitHub's OAuth endpoints.
type Client struct {
	cfg  Config
	http *http.Client
}

func New(cfg Config) *Client {
	timeout := cfg.HTTPTimeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	return &Client{cfg: cfg, http: &http.Client{Timeout: timeout}}
}

func (c *Client) Enabled() bool { return c.cfg.Enabled() }

// Identity is the authenticated GitHub account.
type Identity struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
}

// NewState returns a random, URL-safe OAuth state value used to prevent CSRF.
func NewState() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// AuthorizeURL builds the URL the browser should be redirected to.
func (c *Client) AuthorizeURL(state string) (string, error) {
	if !c.Enabled() {
		return "", ErrNotConfigured
	}

	u, err := url.Parse(authorizeURL)
	if err != nil {
		return "", err
	}

	q := u.Query()
	q.Set("client_id", c.cfg.ClientID)
	q.Set("redirect_uri", c.cfg.RedirectURL)
	q.Set("scope", Scope)
	q.Set("state", state)
	q.Set("allow_signup", "true")
	u.RawQuery = q.Encode()

	return u.String(), nil
}

type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	TokenType        string `json:"token_type"`
	Scope            string `json:"scope"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// Exchange trades an authorization code for an access token, then loads the
// authenticated identity.
func (c *Client) Exchange(ctx context.Context, code string) (Identity, string, error) {
	var identity Identity

	if !c.Enabled() {
		return identity, "", ErrNotConfigured
	}
	if strings.TrimSpace(code) == "" {
		return identity, "", errors.New("missing authorization code")
	}

	form := url.Values{}
	form.Set("client_id", c.cfg.ClientID)
	form.Set("client_secret", c.cfg.ClientSecret)
	form.Set("code", code)
	form.Set("redirect_uri", c.cfg.RedirectURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return identity, "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return identity, "", err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return identity, "", err
	}

	var token tokenResponse
	if err := json.Unmarshal(raw, &token); err != nil {
		return identity, "", fmt.Errorf("decode token response: %w", err)
	}
	if token.Error != "" {
		return identity, "", fmt.Errorf("github oauth error: %s", token.ErrorDescription)
	}
	if token.AccessToken == "" {
		return identity, "", errors.New("github returned an empty access token")
	}

	identity, err = c.fetchIdentity(ctx, token.AccessToken)
	if err != nil {
		return Identity{}, "", err
	}
	return identity, token.AccessToken, nil
}

func (c *Client) fetchIdentity(ctx context.Context, accessToken string) (Identity, error) {
	var identity Identity

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, userURL, nil)
	if err != nil {
		return identity, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "ggstar-hackathon")

	resp, err := c.http.Do(req)
	if err != nil {
		return identity, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return identity, err
	}
	if resp.StatusCode != http.StatusOK {
		return identity, fmt.Errorf("github /user returned %d", resp.StatusCode)
	}
	if err := json.Unmarshal(raw, &identity); err != nil {
		return identity, fmt.Errorf("decode github user: %w", err)
	}
	if identity.Login == "" {
		return identity, errors.New("github returned an empty login")
	}

	return identity, nil
}
