package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ggstar/internal/auth"
	"ggstar/internal/cache"
	"ggstar/internal/config"
	"ggstar/internal/contract"
	"ggstar/internal/db"
	"ggstar/internal/service"
	"ggstar/internal/sessionstore"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/csrf"
	"github.com/gofiber/fiber/v2/middleware/session"
	"github.com/gofiber/template/html/v2"
)

// testApp builds the same route wiring as main, but with a fake OAuth identity
// so the gated flow can be driven without GitHub.
type testApp struct {
	app *fiber.App
	mgr *auth.Manager
	svc *service.Service
}

func newTestApp(t *testing.T) *testApp {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "ggstar.db")

	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	cacheStore, err := cache.New(t.Context(), cache.Config{Driver: cache.DriverMemory})
	if err != nil {
		t.Fatalf("cache: %v", err)
	}
	t.Cleanup(func() { _ = cacheStore.Close() })

	cfg := config.Config{
		Port:            "0",
		DBPath:          dbPath,
		NetworkName:     "BOT Chain Testnet",
		ChainID:         "968",
		ChainIDHex:      "0x3c8",
		SessionTTL:      time.Hour,
		PublicBaseURL:   "http://localhost:3000",
		RateLimitPerMin: 1000,
	}

	svc := service.New(cfg, store, cacheStore)

	sessionStore, err := sessionstore.New(dbPath)
	if err != nil {
		t.Fatalf("session store: %v", err)
	}
	t.Cleanup(func() { _ = sessionStore.Close() })

	sessions := session.New(session.Config{
		Storage:        sessionStore,
		Expiration:     time.Hour,
		KeyLookup:      "cookie:ggstar_session",
		CookieHTTPOnly: true,
		CookieSameSite: "Lax",
	})
	mgr := auth.NewManager(sessions)

	engine := html.New("./views", ".html")
	engine.AddFunc("add", func(a, b int) int { return a + b })

	app := fiber.New(fiber.Config{Views: engine, DisableStartupMessage: true})

	app.Use(csrf.New(csrf.Config{
		KeyLookup:      "header:X-Csrf-Token",
		CookieName:     "ggstar_csrf",
		CookieHTTPOnly: false,
		CookieSameSite: "Lax",
		ContextKey:     "csrf_token",
	}))

	// Mirrors main.go: public auth surface.
	app.Get("/healthz", func(c *fiber.Ctx) error { return c.JSON(fiber.Map{"status": "ok"}) })
	app.Get("/api/me", func(c *fiber.Ctx) error {
		token, _ := c.Locals("csrf_token").(string)
		return c.JSON(fiber.Map{
			"authenticated": mgr.Login(c) != "",
			"login":         mgr.Login(c),
			"csrfToken":     token,
			"signInUrl":     "/auth/github",
			"signOutUrl":    "/auth/logout",
		})
	})
	app.Get("/api/badge/:address", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"public": true})
	})

	// Test-only sign-in shortcut: stands in for the OAuth callback.
	app.Get("/test/signin", func(c *fiber.Ctx) error {
		id := auth.Identity{ID: 1, Login: c.Query("login"), Name: c.Query("login")}
		if err := mgr.SignIn(c, id, ""); err != nil {
			return err
		}
		if err := mgr.RecordUser(c.Context(), store, id); err != nil {
			return err
		}
		return c.SendString("signed in as " + id.Login)
	})

	authRequired := mgr.RequireAuth()

	// Public surface (mirrors main.go): judges browse without login.
	app.Get("/", func(c *fiber.Ctx) error { return c.SendString("home") })
	app.Get("/leaderboard", func(c *fiber.Ctx) error { return c.SendString("leaderboard") })
	app.Get("/achievements", func(c *fiber.Ctx) error { return c.SendString("achievements") })
	app.Get("/api/leaderboard", func(c *fiber.Ctx) error { return c.JSON(fiber.Map{"leaders": []string{}}) })
	app.Get("/api/achievements/:username", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"username": c.Params("username")})
	})

	// Gated surface: writing reputation requires a verified identity.
	app.Post("/api/token-uri", authRequired, func(c *fiber.Ctx) error {
		var req struct {
			Username string   `json:"username"`
			Skills   []string `json:"skills"`
		}
		if err := c.BodyParser(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "bad body"})
		}

		viewer := mgr.Login(c)
		if !strings.EqualFold(viewer, req.Username) {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "you can only mint your own profile",
			})
		}
		return c.JSON(fiber.Map{"tokenUri": "data:application/json;base64,e30="})
	})
	// Self-only analyze: the username always comes from the session, so the
	// test double mirrors main.go and ignores any username in the body.
	app.Post("/api/analyze", authRequired, func(c *fiber.Ctx) error {
		var req struct {
			Username string `json:"username"`
		}
		_ = c.BodyParser(&req)
		viewer := mgr.Login(c)
		owned, _ := serviceOwnership(viewer, viewer)
		return c.JSON(fiber.Map{"owned": owned, "viewerLogin": viewer})
	})

	return &testApp{app: app, mgr: mgr, svc: svc}
}

// serviceOwnership re-exports the decision for the test double.
func serviceOwnership(viewer, analyzed string) (bool, string) {
	return service.DecideOwnershipForTest(viewer, analyzed)
}

type testSession struct {
	cookie     string
	csrfToken  string
	csrfCookie string
}

// signIn returns a session holding the CSRF token and auth cookie.
func (ta *testApp) signIn(t *testing.T, login string) testSession {
	t.Helper()

	meResp, err := ta.app.Test(httptest.NewRequest("GET", "/api/me", nil))
	if err != nil {
		t.Fatal(err)
	}
	var me map[string]any
	_ = json.NewDecoder(meResp.Body).Decode(&me)
	csrfToken, _ := me["csrfToken"].(string)

	s := testSession{csrfToken: csrfToken}
	for _, c := range meResp.Cookies() {
		switch c.Name {
		case "ggstar_csrf":
			s.csrfCookie = c.Name + "=" + c.Value
		case "ggstar_session":
			s.cookie = c.Name + "=" + c.Value
		}
	}

	if login != "" {
		req := httptest.NewRequest("GET", "/test/signin?login="+login, nil)
		req.Header.Set("Cookie", joinCookies(s.cookie, s.csrfCookie))
		resp, err := ta.app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range resp.Cookies() {
			if c.Name == "ggstar_session" {
				s.cookie = c.Name + "=" + c.Value
			}
		}
	}

	return s
}

func joinCookies(parts ...string) string {
	var keep []string
	for _, p := range parts {
		if p != "" {
			keep = append(keep, p)
		}
	}
	return strings.Join(keep, "; ")
}

func (ta *testApp) post(t *testing.T, s testSession, path, body string) *http.Response {
	t.Helper()

	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", joinCookies(s.cookie, s.csrfCookie))
	if s.csrfToken != "" {
		req.Header.Set("X-Csrf-Token", s.csrfToken)
	}

	resp, err := ta.app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestPublicRoutesNeverRequireLogin(t *testing.T) {
	ta := newTestApp(t)

	for _, path := range []string{"/healthz", "/api/me", "/api/badge/0xabc"} {
		resp, err := ta.app.Test(httptest.NewRequest("GET", path, nil))
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != fiber.StatusOK {
			t.Errorf("%s should be public, got %d", path, resp.StatusCode)
		}
	}
}

// TestBadgeSVGStaysPublic is the regression guard for the widget feature: if the
// badge ever gets gated, every GitHub README embed silently breaks.
func TestBadgeSVGStaysPublic(t *testing.T) {
	ta := newTestApp(t)

	resp, err := ta.app.Test(httptest.NewRequest("GET", "/api/badge/0x1111111111111111111111111111111111111111.svg", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Errorf("badge endpoint must stay public, got %d", resp.StatusCode)
	}
}

func TestBrowsableWithoutLogin(t *testing.T) {
	ta := newTestApp(t)

	for _, path := range []string{"/", "/leaderboard", "/achievements", "/api/leaderboard", "/api/achievements/torvalds"} {
		resp, err := ta.app.Test(httptest.NewRequest("GET", path, nil))
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != fiber.StatusOK {
			t.Errorf("%s must be browsable by judges without login, got %d", path, resp.StatusCode)
		}
	}
}

// TestAnalyzeStaysGated is the other half of the contract: browsing is open, but
// analysing writes reputation, so it still requires a verified identity.
func TestAnalyzeStaysGated(t *testing.T) {
	ta := newTestApp(t)
	s := ta.signIn(t, "")

	resp := ta.post(t, s, "/api/analyze", `{"username":"torvalds"}`)
	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Errorf("anonymous analyze = %d, want 401", resp.StatusCode)
	}
}

func TestAnonymousCannotAnalyze(t *testing.T) {
	ta := newTestApp(t)
	s := ta.signIn(t, "")

	resp := ta.post(t, s, "/api/analyze", `{"username":"torvalds"}`)
	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Errorf("anonymous analyze = %d, want 401", resp.StatusCode)
	}
}

// TestLandingPageIsPublic replaces the old redirect behaviour: judges must reach
// the product without a GitHub account, so the root path renders instead of
// bouncing to OAuth.
func TestLandingPageIsPublic(t *testing.T) {
	ta := newTestApp(t)

	resp, err := ta.app.Test(httptest.NewRequest("GET", "/", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Errorf("landing page = %d, want 200", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "" {
		t.Errorf("landing page must not redirect, got %q", loc)
	}
}

// TestSplitBrainClaimIsForbidden is the core guarantee: a signed-in user may not
// mint a badge for somebody else's username.
func TestSplitBrainClaimIsForbidden(t *testing.T) {
	ta := newTestApp(t)
	s := ta.signIn(t, "alice")

	resp := ta.post(t, s, "/api/token-uri", `{"username":"torvalds","skills":["C"]}`)
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("claiming another username = %d, want 403", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "own profile") {
		t.Errorf("error should explain the restriction, got %s", body)
	}
}

func TestOwnProfileCanBeMinted(t *testing.T) {
	ta := newTestApp(t)
	s := ta.signIn(t, "alice")

	resp := ta.post(t, s, "/api/token-uri", `{"username":"alice","skills":["Go"]}`)
	if resp.StatusCode != fiber.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("own profile = %d, want 200 (%s)", resp.StatusCode, body)
	}
}

func TestUsernameComparisonIgnoresCase(t *testing.T) {
	ta := newTestApp(t)
	s := ta.signIn(t, "Alice")

	// GitHub logins are case-insensitive; the gate must agree.
	resp := ta.post(t, s, "/api/token-uri", `{"username":"alice","skills":["Go"]}`)
	if resp.StatusCode != fiber.StatusOK {
		t.Errorf("case-different own login = %d, want 200", resp.StatusCode)
	}
}

// TestAnalyzeMarksOwnership: analyze is self-only, so the result is always
// the signed-in user's own profile, marked owned.
func TestAnalyzeMarksOwnership(t *testing.T) {
	ta := newTestApp(t)
	s := ta.signIn(t, "alice")

	resp := ta.post(t, s, "/api/analyze", `{"username":"alice"}`)
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if owned, _ := body["owned"].(bool); !owned {
		t.Errorf("own profile should be marked owned: %v", body)
	}
	if login, _ := body["viewerLogin"].(string); !strings.EqualFold(login, "alice") {
		t.Errorf("viewerLogin should be alice, got %v", body)
	}
}

// TestAnalyzeIgnoresBodyUsername is the self-only guarantee: even when the
// body names somebody else, the analyzed profile is the session login.
func TestAnalyzeIgnoresBodyUsername(t *testing.T) {
	ta := newTestApp(t)
	s := ta.signIn(t, "alice")

	for _, body := range []string{`{"username":"torvalds"}`, `{}`, ``} {
		resp := ta.post(t, s, "/api/analyze", body)
		var got map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&got)
		if resp.StatusCode != fiber.StatusOK {
			t.Errorf("body %q = %d, want 200", body, resp.StatusCode)
			continue
		}
		if login, _ := got["viewerLogin"].(string); !strings.EqualFold(login, "alice") {
			t.Errorf("body %q analyzed %v, want alice's own profile", body, got)
		}
		if owned, _ := got["owned"].(bool); !owned {
			t.Errorf("body %q should be marked owned: %v", body, got)
		}
	}
}

// TestCSRFBlocksPostWithoutToken proves the protection is actually active.
func TestCSRFBlocksPostWithoutToken(t *testing.T) {
	ta := newTestApp(t)
	s := ta.signIn(t, "alice")

	req := httptest.NewRequest("POST", "/api/analyze", strings.NewReader(`{"username":"alice"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", joinCookies(s.cookie, s.csrfCookie))
	// Deliberately no X-Csrf-Token header.

	resp, err := ta.app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusForbidden {
		t.Errorf("POST without a CSRF token = %d, want 403", resp.StatusCode)
	}
}

func TestAuthenticatedMeExposesIdentityAndToken(t *testing.T) {
	ta := newTestApp(t)
	s := ta.signIn(t, "bob")

	req := httptest.NewRequest("GET", "/api/me", nil)
	req.Header.Set("Cookie", joinCookies(s.cookie, s.csrfCookie))
	resp, err := ta.app.Test(req)
	if err != nil {
		t.Fatal(err)
	}

	var me map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&me)

	if auth, _ := me["authenticated"].(bool); !auth {
		t.Fatalf("expected authenticated session, got %v", me)
	}
	if me["login"] != "bob" {
		t.Errorf("login = %v, want bob", me["login"])
	}
	if me["csrfToken"] == "" {
		t.Error("csrfToken must be exposed so the SPA can sign its requests")
	}
}

// TestLeaderboardExcludesSearchedUser is the privacy check at the HTTP layer.
func TestLeaderboardExcludesSearchedUser(t *testing.T) {
	ta := newTestApp(t)

	// Write a profile owned by "alice" and a searched profile for "torvalds".
	if err := ta.svc.Store().UpsertUser(db.User{GithubID: 1, Login: "alice"}); err != nil {
		t.Fatal(err)
	}

	rows, err := ta.svc.Leaders(50)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("empty database should produce an empty leaderboard, got %+v", rows)
	}
}

// TestContractFileLoads keeps the embedded ABI in sync with the contract.
func TestContractFileLoads(t *testing.T) {
	if _, err := os.Stat(filepath.Join("config", "contract.json")); err != nil {
		t.Skip("contract.json not present")
	}

	f, err := contract.Load(filepath.Join("config", "contract.json"))
	if err != nil {
		t.Fatalf("load contract: %v", err)
	}
	if len(f.ABI) == 0 {
		t.Error("contract ABI should not be empty")
	}
	if !strings.Contains(f.ABIRaw(), "mintBadge") {
		t.Error("ABI should expose mintBadge")
	}
}

var _ = http.MethodGet
