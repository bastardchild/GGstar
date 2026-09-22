package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"ggstar/internal/achievements"
	"ggstar/internal/ai"
	"ggstar/internal/avatar"
	"ggstar/internal/cache"
	"ggstar/internal/chain"
	"ggstar/internal/config"
	"ggstar/internal/db"
	"ggstar/internal/github"
	"ggstar/internal/model"
)

const (
	snapshotTTL = 10 * time.Minute
	starsTTL    = 24 * time.Hour
	langRepos   = 12
)

type Service struct {
	cfg        config.Config
	store      *db.Store
	gh         *github.Client
	ai         *ai.Client
	chain      *chain.Client
	chainMain  *chain.Client
	cache      cache.Store
	bg         *sync.WaitGroup
}

func New(cfg config.Config, store *db.Store, cacheStore cache.Store) *Service {
	return &Service{
		cfg:       cfg,
		store:     store,
		gh:        github.New(cfg.GitHubAPIURL, cfg.GitHubToken),
		ai:        ai.New(cfg.AIBaseURL, cfg.AIAPIKey, cfg.AIModel, cfg.AITimeout, cfg.AIMaxTokens),
		chain:     chain.New(cfg.RPCURL, cfg.ContractAddr, cfg.ExplorerURL),
		chainMain: chain.New(cfg.MainnetRPCURL, cfg.MainnetContractAddr, cfg.MainnetExplorerURL),
		cache:     cacheStore,
		bg:        &sync.WaitGroup{},
	}
}

func (s *Service) Config() config.Config { return s.cfg }

// Store exposes the database for auth wiring and tests.
func (s *Service) Store() *db.Store { return s.store }

// DecideOwnershipForTest exposes the ownership rule to the integration test.
func DecideOwnershipForTest(viewerLogin, analyzed string) (bool, string) {
	return decideOwnership(viewerLogin, analyzed)
}

func (s *Service) Chain() *chain.Client { return s.chain }

func (s *Service) AIEnabled() bool { return s.ai.Enabled() }

// StartBackground begins the periodic cache sweeper.
func (s *Service) StartBackground(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if mem, ok := s.cache.(interface{ Cleanup() }); ok {
					mem.Cleanup()
				}
			}
		}
	}()
}

type AnalyzeOptions struct {
	ForceRefresh        bool
	IncludeAchievements bool

	// ViewerLogin is the OAuth login of the signed-in user, or empty for an
	// anonymous request. /api/analyze always passes the session login as both
	// the analysed username and the viewer, so the profile is recorded as
	// owned and becomes eligible for the leaderboard.
	ViewerLogin string
}

type AnalyzeResult struct {
	Analysis     model.Analysis                `json:"analysis"`
	Profile      model.Analysis                `json:"profile"`
	Achievements []achievements.CatalogueEntry `json:"achievements"`
	Stats        AnalyzeStats                  `json:"stats"`
	TokenURI     string                        `json:"tokenUri"`

	// Owned is true when the viewer owns the analysed profile. The frontend uses
	// it to decide whether minting is allowed.
	Owned bool `json:"owned"`
	// ViewerLogin echoes the signed-in login (empty when anonymous).
	ViewerLogin string `json:"viewerLogin"`
}

type AnalyzeStats struct {
	DominantLanguage string         `json:"dominantLanguage"`
	Languages        []string       `json:"languages"`
	TotalStars       int            `json:"totalStars"`
	MaxRepoStars     int            `json:"maxRepoStars"`
	OwnRepos         int            `json:"ownRepos"`
	Followers        int            `json:"followers"`
	AccountYears     float64        `json:"accountYears"`
	OrgCount         int            `json:"orgCount"`
	TopRepos         []ai.RepoBrief `json:"topRepos"`
	GitHubRequests   int            `json:"githubRequests"`
	Source           string         `json:"source"`
	Cached           bool           `json:"cached"`
	UnlockedCount    int            `json:"unlockedCount"`
}

var usernameOK = func(u string) bool {
	if len(u) == 0 || len(u) > 39 {
		return false
	}
	for i, r := range u {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-' && i != 0 && i != len(u)-1:
		default:
			return false
		}
	}
	return true
}

func ValidUsername(u string) bool { return usernameOK(u) }

func (s *Service) snapshot(ctx context.Context, username string) (*github.Snapshot, bool, error) {
	key := snapshotKey(username)

	var snap *github.Snapshot
	if err := s.cache.Get(ctx, key, &snap); err == nil && snap != nil {
		return snap, true, nil
	}

	fresh, err := s.gh.BuildSnapshot(ctx, username, langRepos)
	if err != nil {
		return nil, false, err
	}

	// A cache write failure must not fail the request: the data is already good.
	_ = s.cache.Set(ctx, key, fresh, snapshotTTL)
	return fresh, false, nil
}

func snapshotKey(username string) string {
	return "snap:" + strings.ToLower(username)
}

// decideOwnership reports whether the viewer owns the analysed profile, and the
// login to persist as owner (empty when not owned).
//
// This is the single rule that makes "mint only your own profile" enforceable:
// the comparison is against the OAuth login, never against anything the browser
// sent about itself.
func decideOwnership(viewerLogin, analyzed string) (bool, string) {
	viewer := strings.TrimSpace(viewerLogin)
	if viewer == "" {
		return false, ""
	}
	if !strings.EqualFold(viewer, strings.TrimSpace(analyzed)) {
		return false, ""
	}
	return true, viewer
}

// claimMismatchReason explains an unverified claim in user-facing terms.
func claimMismatchReason(badgeUsername, viewerLogin string) string {
	return fmt.Sprintf("badge claims @%s but you are signed in as @%s", badgeUsername, viewerLogin)
}

func (s *Service) Analyze(ctx context.Context, username string, opts AnalyzeOptions) (AnalyzeResult, error) {
	username = strings.TrimSpace(username)
	if !usernameOK(username) {
		return AnalyzeResult{}, fmt.Errorf("invalid github username")
	}

	var (
		snap   *github.Snapshot
		cached bool
		err    error
	)

	if opts.ForceRefresh {
		_ = s.cache.Delete(ctx, snapshotKey(username))
	}
	snap, cached, err = s.snapshot(ctx, username)
	if err != nil {
		if github.IsNotFound(err) {
			return AnalyzeResult{}, fmt.Errorf("github user %q not found", username)
		}
		return AnalyzeResult{}, err
	}

	dominant := snap.DominantLanguage()
	languages := snap.LanguageNames(8)

	topRepos := make([]ai.RepoBrief, 0, 6)
	for i, r := range snap.Repos {
		if i >= 6 {
			break
		}
		topRepos = append(topRepos, ai.RepoBrief{
			Name:        r.Name,
			Description: truncate(r.Description, 140),
			Language:    r.Language,
			Stars:       r.Stars,
			Topics:      r.Topics,
		})
	}

	in := ai.Input{
		Username:     snap.Profile.Login,
		Name:         snap.Profile.Name,
		Bio:          truncate(snap.Profile.Bio, 300),
		Company:      snap.Profile.Company,
		Location:     snap.Profile.Location,
		PublicRepos:  snap.Profile.PublicRepos,
		Followers:    snap.Profile.Followers,
		AccountYears: snap.AccountAgeYears(),
		TotalStars:   snap.TotalStars(),
		MaxRepoStars: snap.MaxRepoStars(),
		DominantLang: dominant,
		Languages:    languages,
		TopRepos:     topRepos,
	}
	if in.Username == "" {
		in.Username = username
	}

	aiResult, source := s.ai.Analyze(ctx, in)
	analysis := ai.ToAnalysis(in, aiResult, source)
	analysis.AvatarURL = snap.Profile.AvatarURL

	if cached {
		analysis.Source = "cache"
	}

	refreshStars := !s.store.StarsFresh(username, starsTTL)

	// Ownership is only recorded when the analysed username is the signed-in
	// user. A viewer looking at somebody else's profile must never claim it.
	owned, ownerLogin := decideOwnership(opts.ViewerLogin, username)

	if err := s.store.UpsertProfile(analysis, refreshStars, ownerLogin); err != nil {
		return AnalyzeResult{}, err
	}

	unlocked := []db.AchievementRow{}
	if opts.IncludeAchievements {
		unlocked = achievements.Evaluate(snap)
		if err := s.store.ReplaceAchievements(username, unlocked); err != nil {
			return AnalyzeResult{}, err
		}
		_ = s.store.ReplaceOrgs(username, snap.Orgs)
	}

	tokenURI := BuildTokenURI(analysis, nil)

	return AnalyzeResult{
		Analysis:     analysis,
		Profile:      analysis,
		Achievements: achievements.Catalogue(unlocked),
		Stats: AnalyzeStats{
			DominantLanguage: dominant,
			Languages:        languages,
			TotalStars:       snap.TotalStars(),
			MaxRepoStars:     snap.MaxRepoStars(),
			OwnRepos:         snap.OwnRepoCount(),
			Followers:        snap.Profile.Followers,
			AccountYears:     snap.AccountAgeYears(),
			OrgCount:         len(snap.Orgs),
			TopRepos:         topRepos,
			GitHubRequests:   snap.Requests,
			Source:           analysis.Source,
			Cached:           cached,
			UnlockedCount:    len(unlocked),
		},
		TokenURI:    tokenURI,
		Owned:       owned,
		ViewerLogin: opts.ViewerLogin,
	}, nil
}

// BuildTokenURI generates metadata server-side so the frontend can never forge it.
func BuildTokenURI(a model.Analysis, extraSkills []string) string {
	return BuildTokenURIWithBadge(
		a.Username, "", a.Summary, a.DominantLang,
		a.SkillScore, a.TotalStars, a.PublicRepos, a.Followers,
		1, extraSkillsOrDefault(a.TopSkills, extraSkills),
	)
}

func extraSkillsOrDefault(base, override []string) []string {
	if len(override) > 0 {
		return override
	}
	return base
}

// BuildTokenURIWithBadge is used after the user customizes skills/avatar/title.
// The avatar is referenced by its generated id so the metadata matches the
// numeric avatarId that the smart contract stores.
func BuildTokenURIWithBadge(username, title, summary, dominant string, score, totalStars, publicRepos, followers int, avatarID int, skills []string) string {
	avatarID = avatar.Clamp(avatarID)

	attributes := []map[string]any{
		{"trait_type": "GitHub", "value": username},
		{"trait_type": "Skill Score", "value": score},
		{"trait_type": "Avatar", "value": avatarID},
		{"trait_type": "Dominant Language", "value": dominant},
		{"trait_type": "Total Stars", "value": totalStars},
		{"trait_type": "Public Repos", "value": publicRepos},
		{"trait_type": "Followers", "value": followers},
	}
	for i, s := range skills {
		if i >= 12 {
			break
		}
		attributes = append(attributes, map[string]any{"trait_type": fmt.Sprintf("Skill %d", i+1), "value": s})
	}

	payload := map[string]any{
		"name":        fmt.Sprintf("GGstar Skill Badge - %s", username),
		"description": summary,
		"image":       avatar.Path(avatarID),
		"title":       title,
		"attributes":  attributes,
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return "data:application/json;base64," + base64.StdEncoding.EncodeToString(raw)
}

func (s *Service) Leaders(limit int) ([]db.LeaderRow, error) {
	return s.store.Leaderboard(limit)
}

func (s *Service) Achievements(username string) ([]achievements.CatalogueEntry, error) {
	rows, err := s.store.Achievements(username)
	if err != nil {
		return nil, err
	}
	return achievements.Catalogue(rows), nil
}

// GetBadge reads a wallet's badge. Mainnet is checked first so the launched
// contract is what visitors see, with testnet as a fallback for legacy badges.
//
// The error is only surfaced when neither configured network could be read, so a
// missing mainnet deployment (or a mainnet RPC hiccup) never hides a badge that
// exists on testnet.
func (s *Service) GetBadge(ctx context.Context, address string) (model.Badge, error) {
	var firstErr error
	tried := false

	for _, client := range []*chain.Client{s.chainMain, s.chain} {
		if client == nil || !client.Configured() {
			continue
		}
		tried = true

		badge, err := client.GetBadge(ctx, address)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if badge.Found {
			return badge, nil
		}
	}

	if !tried && firstErr == nil {
		return model.Badge{}, fmt.Errorf("contract address is not configured")
	}
	if firstErr != nil {
		return model.Badge{}, firstErr
	}
	return model.Badge{Found: false}, nil
}

func (s *Service) Profile(username string) (model.ProfileRow, bool, error) {
	return s.store.GetProfile(username)
}

func (s *Service) CountProfiles() int {
	n, _ := s.store.CountProfiles()
	return n
}

func (s *Service) CountUsers() int {
	n, _ := s.store.CountUsers()
	return n
}

// UserStore exposes user records to the auth layer.
func (s *Service) UpsertUser(u db.User) error { return s.store.UpsertUser(u) }

// VerifiedClaim is the result of checking an on-chain badge against an OAuth
// identity.
type VerifiedClaim struct {
	Verified       bool   `json:"verified"`
	TokenID        uint64 `json:"tokenId"`
	Wallet         string `json:"wallet"`
	GithubUsername string `json:"githubUsername"`
	ViewerLogin    string `json:"viewerLogin"`
	Reason         string `json:"reason,omitempty"`
	ExplorerURL    string `json:"explorerUrl,omitempty"`
}

// VerifyClaim reads a transaction back from the chain and decides whether it
// proves ownership of a GitHub identity.
//
// The trust model is deliberate: the chain is the only source of truth for what
// was minted, and the OAuth session is the only source of truth for who is
// asking. The frontend is never trusted for either. A transaction that mints
// somebody else's username is therefore recorded as unverified rather than
// rejected, so the badge stays on chain but is publicly marked as an unproven
// claim.
func (s *Service) VerifyClaim(ctx context.Context, txHash, viewerLogin string) (VerifiedClaim, error) {
	viewerLogin = strings.TrimSpace(viewerLogin)
	if viewerLogin == "" {
		return VerifiedClaim{}, fmt.Errorf("not signed in")
	}

	// A mint happened on exactly one network. Try each configured contract and
	// use whichever emitted the BadgeMinted event for this transaction.
	type attempt struct {
		client   *chain.Client
		contract string
	}

	attempts := []attempt{}
	if s.chainMain != nil && s.chainMain.Configured() {
		attempts = append(attempts, attempt{s.chainMain, s.cfg.MainnetContractAddr})
	}
	if s.chain.Configured() {
		attempts = append(attempts, attempt{s.chain, s.cfg.ContractAddr})
	}
	if len(attempts) == 0 {
		return VerifiedClaim{}, fmt.Errorf("contract address is not configured")
	}

	var lastErr error
	for _, att := range attempts {
		out, ok, err := s.verifyClaimOn(ctx, att.client, att.contract, txHash, viewerLogin)
		if err != nil {
			lastErr = err
			continue
		}
		if ok {
			return out, nil
		}
	}

	if lastErr != nil {
		return VerifiedClaim{}, lastErr
	}
	return VerifiedClaim{ViewerLogin: viewerLogin, Reason: "no BadgeMinted event in this transaction"}, nil
}

// verifyClaimOn checks one network. ok is false when the event is not present
// there, which tells the caller to try the other network.
func (s *Service) verifyClaimOn(
	ctx context.Context,
	client *chain.Client,
	contractAddr, txHash, viewerLogin string,
) (VerifiedClaim, bool, error) {
	receipt, err := client.GetTransactionReceipt(ctx, txHash)
	if err != nil {
		return VerifiedClaim{}, false, err
	}
	if receipt == nil {
		return VerifiedClaim{ViewerLogin: viewerLogin, Reason: "transaction is still pending"}, false, nil
	}
	if receipt.Failed() {
		return VerifiedClaim{ViewerLogin: viewerLogin, Reason: "transaction reverted"}, false, nil
	}

	minted, found := receipt.FindBadgeMinted(contractAddr)
	if !found {
		return VerifiedClaim{}, false, nil
	}

	out := VerifiedClaim{
		TokenID:        minted.TokenID,
		Wallet:         minted.Owner,
		GithubUsername: minted.GithubUsername,
		ViewerLogin:    viewerLogin,
	}

	if !strings.EqualFold(minted.GithubUsername, viewerLogin) {
		out.Reason = claimMismatchReason(minted.GithubUsername, viewerLogin)
		return out, true, nil
	}

	out.Verified = true
	if err := s.store.UpsertClaim(db.Claim{
		TokenID:        int64(minted.TokenID),
		Wallet:         strings.ToLower(minted.Owner),
		GithubUsername: minted.GithubUsername,
		TxHash:         txHash,
	}); err != nil {
		return out, true, err
	}

	return out, true, nil
}

// ClaimForWallet returns the stored verified claim for a wallet, if any.
func (s *Service) ClaimForWallet(wallet string) (db.Claim, bool, error) {
	return s.store.GetClaimByWallet(strings.ToLower(wallet))
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return strings.TrimSpace(s[:max]) + "..."
}

// Renderer helpers

func HTTPStatusFor(err error) int {
	if err == nil {
		return http.StatusOK
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "not found"):
		return http.StatusNotFound
	case strings.Contains(msg, "invalid"):
		return http.StatusBadRequest
	case github.IsRateLimited(err):
		return http.StatusTooManyRequests
	default:
		return http.StatusBadGateway
	}
}
