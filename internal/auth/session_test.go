package auth

import (
	"io"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"ggstar/internal/sessionstore"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/session"
)

// newTestManager wires a real session store so the OAuth state machine and sign
// in/out paths are exercised end to end, not mocked away.
func newTestManager(t *testing.T) (*Manager, *fiber.App) {
	t.Helper()

	store, err := sessionstore.New(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("session store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	sessions := session.New(session.Config{
		Storage:        store,
		Expiration:     0, // replaced below
		KeyLookup:      "cookie:ggstar_session",
		CookieHTTPOnly: true,
		CookieSameSite: "Lax",
	})

	mgr := NewManager(sessions)
	app := fiber.New()

	return mgr, app
}

func TestAnonymousLoginIsEmpty(t *testing.T) {
	mgr, app := newTestManager(t)

	app.Get("/who", func(c *fiber.Ctx) error {
		return c.SendString(mgr.Login(c))
	})

	resp, err := app.Test(httptest.NewRequest("GET", "/who", nil))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)

	if string(body) != "" {
		t.Errorf("anonymous request should have empty login, got %q", body)
	}
}

func TestSignInThenLogin(t *testing.T) {
	mgr, app := newTestManager(t)

	app.Get("/in", func(c *fiber.Ctx) error {
		if err := mgr.SignIn(c, Identity{ID: 7, Login: "alice", Name: "Alice"}, "tok"); err != nil {
			return err
		}
		return c.SendString("ok")
	})
	app.Get("/who", func(c *fiber.Ctx) error {
		return c.SendString(mgr.Login(c))
	})

	// Sign in and capture the session cookie.
	inReq := httptest.NewRequest("GET", "/in", nil)
	inResp, err := app.Test(inReq)
	if err != nil {
		t.Fatal(err)
	}

	var cookie string
	for _, c := range inResp.Cookies() {
		if c.Name == "ggstar_session" {
			cookie = c.Name + "=" + c.Value
		}
	}
	if cookie == "" {
		t.Fatal("sign-in should set a session cookie")
	}

	whoReq := httptest.NewRequest("GET", "/who", nil)
	whoReq.Header.Set("Cookie", cookie)

	whoResp, err := app.Test(whoReq)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(whoResp.Body)

	if string(body) != "alice" {
		t.Errorf("login = %q, want alice", body)
	}
}

func TestSignOutClearsLogin(t *testing.T) {
	mgr, app := newTestManager(t)

	app.Get("/in", func(c *fiber.Ctx) error {
		_ = mgr.SignIn(c, Identity{ID: 1, Login: "bob"}, "")
		return c.SendString("ok")
	})
	app.Post("/out", func(c *fiber.Ctx) error {
		_ = mgr.SignOut(c)
		return c.SendString("ok")
	})
	app.Get("/who", func(c *fiber.Ctx) error {
		return c.SendString(mgr.Login(c))
	})

	inResp, _ := app.Test(httptest.NewRequest("GET", "/in", nil))
	var cookie string
	for _, c := range inResp.Cookies() {
		if c.Name == "ggstar_session" {
			cookie = c.Name + "=" + c.Value
		}
	}

	outReq := httptest.NewRequest("POST", "/out", nil)
	outReq.Header.Set("Cookie", cookie)
	_, _ = app.Test(outReq)

	whoReq := httptest.NewRequest("GET", "/who", nil)
	whoReq.Header.Set("Cookie", cookie)
	whoResp, _ := app.Test(whoReq)
	body, _ := io.ReadAll(whoResp.Body)

	if string(body) != "" {
		t.Errorf("after sign-out login should be empty, got %q", body)
	}
}

func TestStateIsSingleUse(t *testing.T) {
	mgr, app := newTestManager(t)

	app.Get("/set", func(c *fiber.Ctx) error {
		_ = mgr.SetState(c, "state-1")
		return c.SendString("ok")
	})
	app.Get("/consume", func(c *fiber.Ctx) error {
		if mgr.ConsumeState(c, c.Query("state")) {
			return c.SendString("accepted")
		}
		return c.SendString("rejected")
	})

	setResp, _ := app.Test(httptest.NewRequest("GET", "/set", nil))
	var cookie string
	for _, c := range setResp.Cookies() {
		if c.Name == "ggstar_session" {
			cookie = c.Name + "=" + c.Value
		}
	}

	// First use with the correct value succeeds.
	first := httptest.NewRequest("GET", "/consume?state=state-1", nil)
	first.Header.Set("Cookie", cookie)
	r1, _ := app.Test(first)
	b1, _ := io.ReadAll(r1.Body)
	if string(b1) != "accepted" {
		t.Fatalf("first consume = %q, want accepted", b1)
	}

	// Replay must fail: the state was cleared.
	replay := httptest.NewRequest("GET", "/consume?state=state-1", nil)
	replay.Header.Set("Cookie", cookie)
	r2, _ := app.Test(replay)
	b2, _ := io.ReadAll(r2.Body)
	if string(b2) != "rejected" {
		t.Errorf("replayed state = %q, want rejected", b2)
	}
}

func TestConsumeStateRejectsWrongValue(t *testing.T) {
	mgr, app := newTestManager(t)

	app.Get("/set", func(c *fiber.Ctx) error {
		_ = mgr.SetState(c, "correct")
		return c.SendString("ok")
	})
	app.Get("/consume", func(c *fiber.Ctx) error {
		if mgr.ConsumeState(c, c.Query("state")) {
			return c.SendString("accepted")
		}
		return c.SendString("rejected")
	})

	setResp, _ := app.Test(httptest.NewRequest("GET", "/set", nil))
	var cookie string
	for _, c := range setResp.Cookies() {
		if c.Name == "ggstar_session" {
			cookie = c.Name + "=" + c.Value
		}
	}

	req := httptest.NewRequest("GET", "/consume?state=attacker-supplied", nil)
	req.Header.Set("Cookie", cookie)
	resp, _ := app.Test(req)
	body, _ := io.ReadAll(resp.Body)

	if string(body) != "rejected" {
		t.Errorf("mismatched state = %q, want rejected", body)
	}
}

func TestRequireAuthBlocksAnonymous(t *testing.T) {
	mgr, app := newTestManager(t)

	app.Get("/api/secret", mgr.RequireAuth(), func(c *fiber.Ctx) error {
		return c.SendString("secret")
	})
	app.Get("/page", mgr.RequireAuth(), func(c *fiber.Ctx) error {
		return c.SendString("page")
	})

	apiResp, _ := app.Test(httptest.NewRequest("GET", "/api/secret", nil))
	if apiResp.StatusCode != fiber.StatusUnauthorized {
		t.Errorf("API should answer 401, got %d", apiResp.StatusCode)
	}

	pageResp, _ := app.Test(httptest.NewRequest("GET", "/page", nil))
	if pageResp.StatusCode != fiber.StatusFound {
		t.Errorf("page should redirect, got %d", pageResp.StatusCode)
	}
	if loc := pageResp.Header.Get("Location"); loc != "/auth/github" {
		t.Errorf("redirect target = %q, want /auth/github", loc)
	}
}

func TestRequireAuthAllowsSignedIn(t *testing.T) {
	mgr, app := newTestManager(t)

	app.Get("/in", func(c *fiber.Ctx) error {
		_ = mgr.SignIn(c, Identity{ID: 3, Login: "carol"}, "")
		return c.SendString("ok")
	})
	app.Get("/api/secret", mgr.RequireAuth(), func(c *fiber.Ctx) error {
		return c.SendString("secret")
	})

	inResp, _ := app.Test(httptest.NewRequest("GET", "/in", nil))
	var cookie string
	for _, c := range inResp.Cookies() {
		if c.Name == "ggstar_session" {
			cookie = c.Name + "=" + c.Value
		}
	}

	req := httptest.NewRequest("GET", "/api/secret", nil)
	req.Header.Set("Cookie", cookie)
	resp, _ := app.Test(req)
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != fiber.StatusOK || string(body) != "secret" {
		t.Errorf("signed-in request should pass, got %d %q", resp.StatusCode, body)
	}
}
