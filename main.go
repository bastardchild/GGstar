package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"ggstar/internal/auth"
	"ggstar/internal/badgesvg"
	"ggstar/internal/cache"
	"ggstar/internal/config"
	"ggstar/internal/contract"
	"ggstar/internal/db"
	"ggstar/internal/service"
	"ggstar/internal/sessionstore"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/compress"
	"github.com/gofiber/fiber/v2/middleware/csrf"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/fiber/v2/middleware/session"
	"github.com/gofiber/template/html/v2"
)

type analyzeRequest struct {
	// Username is accepted for backward compatibility but ignored: the
	// analyzed profile always comes from the signed-in session.
	Username     string `json:"username"`
	Refresh      bool   `json:"refresh"`
	Achievements *bool  `json:"achievements"`
}

type claimRequest struct {
	TxHash string `json:"txHash"`
}

// proxyHeader selects the header Fiber should read the client IP from. It is
// only honoured when trusted-proxy checking is on, so a client cannot spoof its
// own address to dodge the rate limiter.
func proxyHeader(trustProxy bool) string {
	if !trustProxy {
		return ""
	}
	return fiber.HeaderXForwardedFor
}

type tokenURIRequest struct {
	Username    string   `json:"username"`
	Title       string   `json:"title"`
	Summary     string   `json:"summary"`
	Dominant    string   `json:"dominantLanguage"`
	SkillScore  int      `json:"skillScore"`
	TotalStars  int      `json:"totalStars"`
	PublicRepos int      `json:"publicRepos"`
	Followers   int      `json:"followers"`
	AvatarID    int      `json:"avatarId"`
	Skills      []string `json:"skills"`
}

func main() {
	cfg := config.Load()

	store, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("open database %q: %v", cfg.DBPath, err)
	}
	defer store.Close()

	cache.SetFallback(func(err error) {
		log.Printf("cache: %v", err)
	})

	cacheStore, err := cache.New(context.Background(), cfg.CacheStoreConfig())
	if err != nil {
		log.Fatalf("init cache: %v", err)
	}
	defer cacheStore.Close()

	svc := service.New(cfg, store, cacheStore)

	// Sessions are stored in SQLite (not Redis) so an LRU eviction policy can
	// never silently drop a login. The store is the pure-Go driver, which keeps
	// CGO_ENABLED=0 builds working.
	sessionStore, err := sessionstore.New(cfg.DBPath)
	if err != nil {
		log.Fatalf("init session store: %v", err)
	}
	defer sessionStore.Close()

	sessions := session.New(session.Config{
		Storage:        sessionStore,
		Expiration:     cfg.SessionTTL,
		KeyLookup:      "cookie:ggstar_session",
		CookieHTTPOnly: true,
		CookieSecure:   cfg.CookieSecure,
		CookieSameSite: "Lax",
		CookiePath:     "/",
	})

	authClient := auth.New(cfg.OAuthConfig())
	authMgr := auth.NewManager(sessions)

	if !cfg.OAuthEnabled() {
		log.Printf("warning: GitHub OAuth is not configured; sign-in is disabled")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.StartBackground(ctx)

	engine := html.New("./views", ".html")
	engine.AddFunc("add", func(a, b int) int { return a + b })

	trustProxy, trustedProxies := cfg.ProxyConfig()
	app := fiber.New(fiber.Config{
		AppName:                 "ggstar",
		Views:                   engine,
		DisableStartupMessage:   true,
		ReadTimeout:             30 * time.Second,
		WriteTimeout:            60 * time.Second,
		EnableTrustedProxyCheck: trustProxy,
		TrustedProxies:          trustedProxies,
		ProxyHeader:             proxyHeader(trustProxy),
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			if e, ok := err.(*fiber.Error); ok {
				code = e.Code
			}
			if strings.HasPrefix(c.Path(), "/api/") {
				return c.Status(code).JSON(fiber.Map{"error": err.Error()})
			}
			return c.Status(code).SendString(err.Error())
		},
	})

	app.Use(recover.New())
	app.Use(compress.New(compress.Config{Level: compress.LevelBestSpeed}))
	app.Use(logger.New(logger.Config{Format: "${time} ${status} ${latency} ${method} ${path}\n"}))

	// CSRF protection is required now that state-changing requests carry an auth
	// cookie. The token is exposed to the frontend through /api/me and sent back
	// in the X-Csrf-Token header by the fetch helpers in views/index.html.
	app.Use(csrf.New(csrf.Config{
		KeyLookup:      "header:X-Csrf-Token",
		CookieName:     "ggstar_csrf",
		CookieHTTPOnly: false, // must be readable so the SPA can echo it back
		CookieSecure:   cfg.CookieSecure,
		CookieSameSite: "Lax",
		CookiePath:     "/",
		Expiration:     cfg.SessionTTL,
		ContextKey:     "csrf_token",
	}))

	app.Static("/public", "./public", fiber.Static{MaxAge: 86400})

	// Rate limiting uses the same cache backend, so limits are shared across
	// instances when Redis is enabled.
	rateLimit := func(c *fiber.Ctx) error {
		count, err := cacheStore.Incr(c.Context(), "rl:"+c.IP(), time.Minute)
		if err != nil {
			// Never block traffic because the limiter backend misbehaved.
			return c.Next()
		}
		if count > int64(cfg.RateLimitPerMin) {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error": "too many requests, please slow down",
			})
		}
		return c.Next()
	}

	contractFile, contractErr := contract.Load("config/contract.json")
	if contractErr != nil {
		log.Printf("warning: config/contract.json not loaded: %v (read-only mode)", contractErr)
	}

	pageData := func(c *fiber.Ctx) fiber.Map {
		return fiber.Map{
			"NetworkName":   cfg.NetworkName,
			"ChainID":       cfg.ChainID,
			"ChainIDHex":    cfg.ChainIDHex,
			"RPCURL":        cfg.RPCURL,
			"ExplorerURL":   cfg.ExplorerURL,
			"FaucetURL":     cfg.FaucetURL,
			"NativeSymbol":  cfg.NativeSymbol,
			"ContractAddr":  cfg.ContractAddr,
			"MainnetName":   cfg.MainnetName,
			"MainnetChainID": cfg.MainnetChainID,
			"MainnetExplorerURL": cfg.MainnetExplorerURL,
			"MainnetContractAddr": cfg.MainnetContractAddr,
			"AIEnabled":     svc.AIEnabled(),
			"GitHubAuth":    cfg.GitHubAuthenticated(),
			"ProfileCount":  svc.CountProfiles(),
			"UserCount":     svc.CountUsers(),
			"OAuthEnabled":  cfg.OAuthEnabled(),
			"ViewerLogin":   authMgr.Login(c),
		}
	}

	// Runtime config for the frontend: keeps the ABI out of template escaping.
	app.Get("/api/config", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"network":         cfg.NetworkName,
			"chainId":         cfg.ChainID,
			"chainIdHex":      cfg.ChainIDHex,
			"rpcUrl":          cfg.RPCURL,
			"explorerUrl":     cfg.ExplorerURL,
			"faucetUrl":       cfg.FaucetURL,
			"nativeSymbol":    cfg.NativeSymbol,
			"contractAddress": cfg.ContractAddr,
			"abi":             json.RawMessage(contractFile.ABIRaw()),
			"aiEnabled":       svc.AIEnabled(),
			"githubAuth":      cfg.GitHubAuthenticated(),
			"oauthEnabled":    cfg.OAuthEnabled(),
			"viewerLogin":     authMgr.Login(c),
			"signInUrl":       "/auth/github",
			"signOutUrl":      "/auth/logout",
			"mainnet": fiber.Map{
				"name":            cfg.MainnetName,
				"chainId":         cfg.MainnetChainID,
				"chainIdHex":      cfg.MainnetChainIDHex,
				"rpcUrl":          cfg.MainnetRPCURL,
				"explorerUrl":     cfg.MainnetExplorerURL,
				"contractAddress": cfg.MainnetContractAddr,
			},
		})
	})

	// ------------------------------------------------------------------
	// Public authentication routes
	// ------------------------------------------------------------------

	app.Get("/auth/github", func(c *fiber.Ctx) error {
		if !authClient.Enabled() {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
				"error": "GitHub sign-in is not configured on this server",
			})
		}

		state, err := auth.NewState()
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		if err := authMgr.SetState(c, state); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}

		target, err := authClient.AuthorizeURL(state)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		return c.Redirect(target, fiber.StatusFound)
	})

	app.Get("/auth/github/callback", func(c *fiber.Ctx) error {
		if oauthErr := c.Query("error"); oauthErr != "" {
			return c.Redirect("/?auth=denied", fiber.StatusFound)
		}
		if state := c.Query("state"); !authMgr.ConsumeState(c, state) {
			return c.Redirect("/?auth=state_mismatch", fiber.StatusFound)
		}

		identity, token, err := authClient.Exchange(c.Context(), c.Query("code"))
		if err != nil {
			log.Printf("oauth exchange failed: %v", err)
			return c.Redirect("/?auth=failed", fiber.StatusFound)
		}

		if err := authMgr.SignIn(c, identity, token); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		if err := authMgr.RecordUser(c.Context(), store, identity); err != nil {
			log.Printf("record user %s: %v", identity.Login, err)
		}

		return c.Redirect("/?auth=ok", fiber.StatusFound)
	})

	app.Post("/auth/logout", func(c *fiber.Ctx) error {
		if err := authMgr.SignOut(c); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		return c.JSON(fiber.Map{"ok": true})
	})

	// /api/me is deliberately public: the frontend needs it to render the right
	// navigation state before any protected call is attempted.
	app.Get("/api/me", func(c *fiber.Ctx) error {
		login := authMgr.Login(c)

		var (
			name      string
			avatarURL string
		)
		if login != "" {
			if u, found, err := store.GetUser(login); err == nil && found {
				name = u.Name
				avatarURL = u.AvatarURL
			}
		}

		token, _ := c.Locals("csrf_token").(string)

		return c.JSON(fiber.Map{
			"authenticated": login != "",
			"login":         login,
			"name":          name,
			"avatarUrl":     avatarURL,
			"oauthEnabled":  cfg.OAuthEnabled(),
			"csrfToken":     token,
			"signInUrl":     "/auth/github",
			"signOutUrl":    "/auth/logout",
		})
	})

	app.Get("/healthz", func(c *fiber.Ctx) error {
		chainID, err := svc.Chain().HexChainID(c.Context())
		rpcOK := err == nil

		cacheOK := cacheStore.Ping(c.Context()) == nil

		status := fiber.StatusOK
		if !rpcOK {
			status = fiber.StatusServiceUnavailable
		}

		return c.Status(status).JSON(fiber.Map{
			"status":            map[bool]string{true: "ok", false: "degraded"}[rpcOK],
			"service":           "ggstar",
			"network":           cfg.NetworkName,
			"chainId":           cfg.ChainID,
			"rpcChainId":        chainID,
			"rpcOk":             rpcOK,
			"cacheDriver":       cfg.CacheDriver,
			"cacheOk":           cacheOK,
			"aiEnabled":         svc.AIEnabled(),
			"githubAuthenticated": cfg.GitHubAuthenticated(),
			"oauthEnabled":      cfg.OAuthEnabled(),
			"contractConfigured": svc.Chain().Configured(),
			"mainnetConfigured":  cfg.MainnetConfigured(),
		})
	})

	// ------------------------------------------------------------------
	// Public pages and read APIs
	//
	// Judges must be able to open the site and use the product without a GitHub
	// account. What stays gated is the identity claim: /api/analyze (self-only,
	// writes the leaderboard), /api/token-uri and /api/claim.
	// ------------------------------------------------------------------

	app.Get("/", func(c *fiber.Ctx) error {
		data := pageData(c)
		data["Title"] = "GGstar - Verified GitHub Skill Badge on BOT Chain"
		data["Active"] = "index"
		data["WithEthers"] = true
		data["WithAlpine"] = true
		return c.Render("index", data)
	})

	app.Get("/leaderboard", func(c *fiber.Ctx) error {
		rows, err := svc.Leaders(50)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		data := pageData(c)
		data["Title"] = "Leaderboard - GGstar"
		data["Active"] = "leaderboard"
		data["Leaders"] = rows
		return c.Render("leaderboard", data)
	})

	app.Get("/achievements", func(c *fiber.Ctx) error {
		username := c.Query("username")
		if username == "" {
			username = authMgr.Login(c)
		}
		data := pageData(c)
		data["Title"] = "Achievements - GGstar"
		data["Active"] = "achievements"
		data["WithAlpine"] = true
		data["Username"] = username
		return c.Render("achievements", data)
	})

	// ------------------------------------------------------------------
	// Gated APIs: these write reputation, so they require a verified identity.
	// ------------------------------------------------------------------

	authRequired := authMgr.RequireAuth()

	// Self-only analyze: the username always comes from the signed-in session.
	// Any username sent in the body is ignored, so a user can only ever
	// analyze their own GitHub profile with one click.
	app.Post("/api/analyze", authRequired, rateLimit, func(c *fiber.Ctx) error {
		var req analyzeRequest
		if err := c.BodyParser(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid JSON body"})
		}

		username := authMgr.Login(c)

		includeAchievements := true
		if req.Achievements != nil {
			includeAchievements = *req.Achievements
		}

		result, err := svc.Analyze(c.Context(), username, service.AnalyzeOptions{
			ForceRefresh:        req.Refresh,
			IncludeAchievements: includeAchievements,
			ViewerLogin:         username,
		})
		if err != nil {
			return c.Status(service.HTTPStatusFor(err)).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(result)
	})

	app.Post("/api/token-uri", authRequired, rateLimit, func(c *fiber.Ctx) error {
		var req tokenURIRequest
		if err := c.BodyParser(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid JSON body"})
		}
		if !service.ValidUsername(req.Username) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid github username"})
		}

		// Defense in depth: /api/analyze already forces the session login, but
		// metadata is still never generated for a username the caller does not
		// own.
		viewer := authMgr.Login(c)
		if !strings.EqualFold(viewer, req.Username) {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": fmt.Sprintf("you are signed in as @%s and can only mint @%s", viewer, viewer),
			})
		}

		skills := make([]string, 0, len(req.Skills))
		for _, s := range req.Skills {
			s = strings.TrimSpace(s)
			if s != "" {
				skills = append(skills, s)
			}
		}
		if len(skills) == 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "at least one skill is required"})
		}
		if len(skills) > 12 {
			skills = skills[:12]
		}

		uri := service.BuildTokenURIWithBadge(
			req.Username, req.Title, req.Summary, req.Dominant,
			req.SkillScore, req.TotalStars, req.PublicRepos, req.Followers,
			req.AvatarID, skills,
		)
		return c.JSON(fiber.Map{"tokenUri": uri, "skills": skills, "avatarId": req.AvatarID})
	})

	// Verifies a mint transaction against the signed-in identity. The chain is
	// the source of truth for what was minted; the session is the source of
	// truth for who is asking.
	app.Post("/api/claim", authRequired, rateLimit, func(c *fiber.Ctx) error {
		var req claimRequest
		if err := c.BodyParser(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid JSON body"})
		}

		result, err := svc.VerifyClaim(c.Context(), req.TxHash, authMgr.Login(c))
		if err != nil {
			return c.Status(service.HTTPStatusFor(err)).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(result)
	})

	app.Get("/api/leaderboard", func(c *fiber.Ctx) error {
		rows, err := svc.Leaders(50)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(fiber.Map{"leaders": rows, "count": len(rows)})
	})

	app.Get("/api/achievements/:username", func(c *fiber.Ctx) error {
		username := c.Params("username")
		if !service.ValidUsername(username) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid github username"})
		}
		list, err := svc.Achievements(username)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(fiber.Map{"username": username, "achievements": list, "count": len(list)})
	})

	// One handler serves both representations: /api/badge/{address} returns JSON,
	// while /api/badge/{address}.svg returns the README-embeddable SVG. Keeping it
	// in a single route also guarantees embeds never break with an HTTP error.
	//
	// This route is intentionally PUBLIC: the badge is meant to be embedded in a
	// GitHub README, so gating it would break the whole widget feature.
	app.Get("/api/badge/:address", func(c *fiber.Ctx) error {
		raw := c.Params("address")
		wantsSVG := strings.HasSuffix(strings.ToLower(raw), ".svg")
		address := strings.TrimSuffix(raw, ".svg")
		address = strings.TrimSuffix(address, ".SVG")

		badge, err := svc.GetBadge(c.Context(), address)

		// The verified flag is not stored on chain; it comes from the claims
		// table, which is only written after a successful OAuth-backed check.
		if err == nil && badge.Found {
			if claim, found, claimErr := svc.ClaimForWallet(badge.Address); claimErr == nil && found {
				badge.Verified = true
				badge.VerifiedLogin = claim.GithubUsername
			}
		}

		if wantsSVG {
			c.Set("Content-Type", "image/svg+xml; charset=utf-8")
			if err != nil || !badge.Found {
				c.Set("Cache-Control", "public, max-age=60")
				return c.Status(fiber.StatusOK).SendString(badgesvg.Empty())
			}
			c.Set("Cache-Control", "public, max-age=300")
			return c.Status(fiber.StatusOK).SendString(badgesvg.Render(badge, cfg.ExplorerURL, cfg.ContractAddr))
		}

		if err != nil {
			return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": err.Error()})
		}
		if !badge.Found {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "no badge for this address", "found": false})
		}

		return c.JSON(fiber.Map{
			"badge":   badge,
			"snippet": badgesvg.Snippet(badge, c.BaseURL(), cfg.ExplorerURL, cfg.ContractAddr),
		})
	})

	app.Get("/api/snippet", func(c *fiber.Ctx) error {
		address := strings.TrimSpace(c.Query("address"))
		if address == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "address query param is required"})
		}

		badge, err := svc.GetBadge(c.Context(), address)
		if err != nil {
			return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(badgesvg.Snippet(badge, c.BaseURL(), cfg.ExplorerURL, cfg.ContractAddr))
	})

	go func() {
		if err := app.Listen("0.0.0.0:" + cfg.Port); err != nil {
			log.Fatalf("listen: %v", err)
		}
	}()

	log.Printf("ggstar ready on :%s | network=%s chain=%s ai=%v githubToken=%v contract=%v",
		cfg.Port, cfg.NetworkName, cfg.ChainID, svc.AIEnabled(), cfg.GitHubAuthenticated(), svc.Chain().Configured())

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("shutting down...")
	_ = app.ShutdownWithTimeout(10 * time.Second)
}
