package auth

import (
	"context"
	"strings"

	"ggstar/internal/db"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/session"
)

// Session keys.
const (
	sessionKeyLogin      = "login"
	sessionKeyGithubID   = "github_id"
	sessionKeyOAuthState = "oauth_state"
	sessionKeyToken      = "token"
)

// Manager wraps Fiber's session store with ggstar-specific helpers.
//
// Sessions live in SQLite rather than Redis on purpose: a Redis LRU eviction
// policy could silently drop sessions and log users out at random.
type Manager struct {
	store *session.Store
}

func NewManager(store *session.Store) *Manager {
	// The Session type uses gob, so every stored type must be registered.
	store.RegisterType("")
	store.RegisterType(int64(0))
	return &Manager{store: store}
}

// Session returns the current session, creating one when needed.
func (m *Manager) Session(c *fiber.Ctx) (*session.Session, error) {
	return m.store.Get(c)
}

// Login returns the signed-in GitHub login, or empty when anonymous.
func (m *Manager) Login(c *fiber.Ctx) string {
	sess, err := m.store.Get(c)
	if err != nil {
		return ""
	}
	raw, ok := sess.Get(sessionKeyLogin).(string)
	if !ok {
		return ""
	}
	return raw
}

// SignIn stores the authenticated identity and regenerates the session id so a
// pre-login session cannot be reused (session fixation).
func (m *Manager) SignIn(c *fiber.Ctx, id Identity, accessToken string) error {
	sess, err := m.store.Get(c)
	if err != nil {
		return err
	}
	if err := sess.Regenerate(); err != nil {
		return err
	}

	sess.Set(sessionKeyLogin, id.Login)
	sess.Set(sessionKeyGithubID, id.ID)
	if accessToken != "" {
		sess.Set(sessionKeyToken, accessToken)
	}
	return sess.Save()
}

// AccessToken returns the stored OAuth token, letting GitHub API calls spend the
// signed-in user's quota instead of one shared server token.
func (m *Manager) AccessToken(c *fiber.Ctx) string {
	sess, err := m.store.Get(c)
	if err != nil {
		return ""
	}
	raw, _ := sess.Get(sessionKeyToken).(string)
	return raw
}

// SignOut destroys the session entirely.
func (m *Manager) SignOut(c *fiber.Ctx) error {
	sess, err := m.store.Get(c)
	if err != nil {
		return err
	}
	return sess.Destroy()
}

// State helpers for the OAuth CSRF check.

func (m *Manager) SetState(c *fiber.Ctx, state string) error {
	sess, err := m.store.Get(c)
	if err != nil {
		return err
	}
	sess.Set(sessionKeyOAuthState, state)
	return sess.Save()
}

// ConsumeState compares and clears the stored state so it can only be used once.
func (m *Manager) ConsumeState(c *fiber.Ctx, state string) bool {
	sess, err := m.store.Get(c)
	if err != nil {
		return false
	}

	stored, _ := sess.Get(sessionKeyOAuthState).(string)
	sess.Delete(sessionKeyOAuthState)
	_ = sess.Save()

	return stored != "" && state != "" && stored == state
}

// RecordUser persists the signed-in account so the leaderboard can be limited to
// registered users.
func (m *Manager) RecordUser(ctx context.Context, store *db.Store, id Identity) error {
	return store.UpsertUser(db.User{
		GithubID:  id.ID,
		Login:     id.Login,
		Name:      id.Name,
		AvatarURL: id.AvatarURL,
	})
}

// RequireAuth blocks anonymous requests. API paths receive 401 as JSON so the
// frontend can redirect; page paths are redirected straight to GitHub.
func (m *Manager) RequireAuth() fiber.Handler {
	return func(c *fiber.Ctx) error {
		if m.Login(c) != "" {
			return c.Next()
		}

		if strings.HasPrefix(c.Path(), "/api/") {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error":   "sign in with GitHub to continue",
				"signIn":  "/auth/github",
				"needsAuth": true,
			})
		}
		return c.Redirect("/auth/github", fiber.StatusFound)
	}
}
