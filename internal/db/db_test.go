package db

import (
	"path/filepath"
	"testing"
	"time"

	"ggstar/internal/model"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()

	path := filepath.Join(t.TempDir(), "test.db")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func sampleAnalysis() model.Analysis {
	return model.Analysis{
		Username:        "torvalds",
		SkillScore:      88,
		TopSkills:       []string{"C", "Assembly", "Rust"},
		SuggestedTitles: []string{"Kernel Whisperer", "Boba Addict"},
		Summary:         "Ships kernels.",
		DominantLang:    "C",
		AvatarURL:       "https://avatars.githubusercontent.com/u/1024025",
		PublicRepos:     9,
		Followers:       324427,
		TotalStars:      262867,
		Source:          "mock",
	}
}

func TestUpsertAndReadProfile(t *testing.T) {
	store := newTestStore(t)

	if err := store.UpsertProfile(sampleAnalysis(), true, ""); err != nil {
		t.Fatalf("UpsertProfile: %v", err)
	}

	row, found, err := store.GetProfile("torvalds")
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if !found {
		t.Fatal("expected profile to be found")
	}
	if row.SkillScore != 88 || row.TotalStars != 262867 || row.DominantLang != "C" {
		t.Errorf("unexpected row: %+v", row)
	}
	if len(row.TopSkills) != 3 || row.TopSkills[0] != "C" {
		t.Errorf("top skills not round-tripped: %v", row.TopSkills)
	}
	if len(row.SuggestedTitles) != 2 {
		t.Errorf("titles not round-tripped: %v", row.SuggestedTitles)
	}

	n, err := store.CountProfiles()
	if err != nil || n != 1 {
		t.Fatalf("CountProfiles = %d err=%v, want 1", n, err)
	}
}

func TestUpsertDoesNotResetStars(t *testing.T) {
	store := newTestStore(t)

	a := sampleAnalysis()
	if err := store.UpsertProfile(a, true, ""); err != nil {
		t.Fatal(err)
	}

	// Second analysis run: no star refresh, different score.
	a2 := a
	a2.SkillScore = 91
	a2.TotalStars = 0
	if err := store.UpsertProfile(a2, false, ""); err != nil {
		t.Fatal(err)
	}

	row, _, err := store.GetProfile("torvalds")
	if err != nil {
		t.Fatal(err)
	}
	if row.SkillScore != 91 {
		t.Errorf("score should update, got %d", row.SkillScore)
	}
	if row.TotalStars != 262867 {
		t.Errorf("stars must be preserved when not refreshing, got %d", row.TotalStars)
	}
}

func TestStarsFreshTTL(t *testing.T) {
	store := newTestStore(t)

	if store.StarsFresh("torvalds", 24*time.Hour) {
		t.Error("unknown profile must not be fresh")
	}

	if err := store.UpsertProfile(sampleAnalysis(), true, ""); err != nil {
		t.Fatal(err)
	}
	if !store.StarsFresh("torvalds", 24*time.Hour) {
		t.Error("expected stars to be fresh right after refresh")
	}
	if store.StarsFresh("torvalds", time.Nanosecond) {
		t.Error("expected stars to be stale with a nanosecond TTL")
	}
}

// registerUser creates the users row that the leaderboard joins against.
func registerUser(t *testing.T, store *Store, login string) {
	t.Helper()

	if err := store.UpsertUser(User{GithubID: int64(len(login)) + 1000, Login: login, Name: login}); err != nil {
		t.Fatalf("UpsertUser(%s): %v", login, err)
	}
}

func TestLeaderboardOrdering(t *testing.T) {
	store := newTestStore(t)

	registerUser(t, store, "lowstar")
	registerUser(t, store, "highstar")

	low := sampleAnalysis()
	low.Username = "lowstar"
	low.TotalStars = 10

	high := sampleAnalysis()
	high.Username = "highstar"
	high.TotalStars = 9999

	if err := store.UpsertProfile(low, true, "lowstar"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertProfile(high, true, "highstar"); err != nil {
		t.Fatal(err)
	}

	rows, err := store.Leaderboard(50)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0].Username != "highstar" {
		t.Errorf("leaderboard must be ordered by stars desc, got %s first", rows[0].Username)
	}
	if rows[0].GithubURL != "https://github.com/highstar" {
		t.Errorf("unexpected github url %q", rows[0].GithubURL)
	}
}

// TestLeaderboardExcludesSearchedProfiles is the core privacy guarantee: looking
// up somebody else's profile must never place them on the leaderboard.
func TestLeaderboardExcludesSearchedProfiles(t *testing.T) {
	store := newTestStore(t)

	registerUser(t, store, "viewer")

	// The viewer looks at someone else's profile: ownerLogin stays empty.
	other := sampleAnalysis()
	other.Username = "torvalds"
	other.TotalStars = 500000
	if err := store.UpsertProfile(other, true, ""); err != nil {
		t.Fatal(err)
	}

	rows, err := store.Leaderboard(50)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("a searched profile must not appear on the leaderboard, got %+v", rows)
	}

	// Ownership is recorded once the owner signs in and analyses themselves.
	if err := store.UpsertProfile(other, true, "torvalds"); err != nil {
		t.Fatal(err)
	}
	registerUser(t, store, "torvalds")

	rows, err = store.Leaderboard(50)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Username != "torvalds" {
		t.Errorf("expected the owned profile on the leaderboard, got %+v", rows)
	}
}

// TestOwnershipSurvivesAnonymousLookup makes sure a later anonymous analysis of
// the same username cannot silently remove it from the leaderboard.
func TestOwnershipSurvivesAnonymousLookup(t *testing.T) {
	store := newTestStore(t)
	registerUser(t, store, "torvalds")

	a := sampleAnalysis()
	a.Username = "torvalds"

	if err := store.UpsertProfile(a, true, "torvalds"); err != nil {
		t.Fatal(err)
	}
	// Someone else looks them up afterwards.
	if err := store.UpsertProfile(a, false, ""); err != nil {
		t.Fatal(err)
	}

	rows, err := store.Leaderboard(50)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Errorf("ownership must not be cleared by a later anonymous lookup, got %+v", rows)
	}
}

func TestLeaderboardRequiresRegisteredUser(t *testing.T) {
	store := newTestStore(t)

	// owner_login set, but no matching users row: must not be listed.
	a := sampleAnalysis()
	a.Username = "ghost"
	a.TotalStars = 12345
	if err := store.UpsertProfile(a, true, "ghost"); err != nil {
		t.Fatal(err)
	}

	rows, err := store.Leaderboard(50)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("profiles without a registered user must be excluded, got %+v", rows)
	}
}

func TestUpsertAndFindUser(t *testing.T) {
	store := newTestStore(t)

	u := User{GithubID: 42, Login: "octocat", Name: "Mona", AvatarURL: "https://example.com/a.png"}
	if err := store.UpsertUser(u); err != nil {
		t.Fatal(err)
	}

	got, found, err := store.GetUser("octocat")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected the user to be found")
	}
	if got.GithubID != 42 || got.Name != "Mona" {
		t.Errorf("unexpected user: %+v", got)
	}

	// Same github_id with a renamed login must update, not duplicate.
	if err := store.UpsertUser(User{GithubID: 42, Login: "octocat-renamed", Name: "Mona"}); err != nil {
		t.Fatal(err)
	}
	if n, _ := store.CountUsers(); n != 1 {
		t.Errorf("rename must not create a second user, CountUsers = %d", n)
	}
	if _, found, _ := store.GetUser("octocat-renamed"); !found {
		t.Error("expected the renamed login to be present")
	}

	if _, found, _ := store.GetUser("nobody"); found {
		t.Error("unknown user should not be found")
	}
}

func TestClaimRoundTrip(t *testing.T) {
	store := newTestStore(t)

	c := Claim{
		TokenID:        7,
		Wallet:         "0xabc",
		GithubUsername: "torvalds",
		TxHash:         "0xdeadbeef",
	}
	if err := store.UpsertClaim(c); err != nil {
		t.Fatal(err)
	}

	got, found, err := store.GetClaimByWallet("0xabc")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected the claim to be found")
	}
	if got.TokenID != 7 || got.GithubUsername != "torvalds" {
		t.Errorf("unexpected claim: %+v", got)
	}

	if _, found, _ := store.GetClaimByWallet("0xnone"); found {
		t.Error("unknown wallet should have no claim")
	}
}

func TestAchievementsReplaceIsIdempotent(t *testing.T) {
	store := newTestStore(t)

	first := []AchievementRow{
		{Key: "stardust", Name: "Stardust", Tier: "common"},
		{Key: "nova", Name: "Nova", Tier: "rare"},
	}
	if err := store.ReplaceAchievements("torvalds", first); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceAchievements("torvalds", first); err != nil {
		t.Fatal(err)
	}

	rows, err := store.Achievements("torvalds")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2 unique achievements, got %d", len(rows))
	}

	if err := store.ReplaceAchievements("torvalds", first[:1]); err != nil {
		t.Fatal(err)
	}
	rows, _ = store.Achievements("torvalds")
	if len(rows) != 1 {
		t.Errorf("expected replacement to shrink set to 1, got %d", len(rows))
	}
}

func TestMigrationIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "twice.db")

	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.UpsertProfile(sampleAnalysis(), true, ""); err != nil {
		t.Fatal(err)
	}
	_ = first.Close()

	second, err := Open(path)
	if err != nil {
		t.Fatalf("re-open must reuse existing schema: %v", err)
	}
	defer second.Close()

	row, found, err := second.GetProfile("torvalds")
	if err != nil || !found || row.SkillScore != 88 {
		t.Fatalf("data lost across reopen: found=%v row=%+v err=%v", found, row, err)
	}
}

func TestReplaceOrgs(t *testing.T) {
	store := newTestStore(t)

	if err := store.ReplaceOrgs("torvalds", []string{"linux", "git"}); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceOrgs("torvalds", []string{"linux"}); err != nil {
		t.Fatal(err)
	}
	// No public getter needed; ensure the second replace did not error.
}

func TestMigrateAddsTopReposColumn(t *testing.T) {
	store := newTestStore(t)

	for _, col := range []string{"owner_login", "top_repos"} {
		has, err := store.hasColumn("profiles", col)
		if err != nil {
			t.Fatal(err)
		}
		if !has {
			t.Errorf("expected column profiles.%s after migrate", col)
		}
	}
}

func TestTopReposAndProfileFieldsRoundTrip(t *testing.T) {
	store := newTestStore(t)

	if err := store.UpsertProfile(sampleAnalysis(), true, ""); err != nil {
		t.Fatal(err)
	}
	const repos = `[{"name":"linux","description":"kernel","language":"C","stars":100,"topics":[]}]`
	if err := store.SaveTopRepos("torvalds", repos); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceOrgs("torvalds", []string{"linux", "git"}); err != nil {
		t.Fatal(err)
	}

	row, found, err := store.GetProfile("torvalds")
	if err != nil || !found {
		t.Fatalf("GetProfile: found=%v err=%v", found, err)
	}
	if row.AvatarURL != "https://avatars.githubusercontent.com/u/1024025" {
		t.Errorf("avatar not read back: %q", row.AvatarURL)
	}
	if row.PublicRepos != 9 || row.Followers != 324427 {
		t.Errorf("repos/followers not read back: %+v", row)
	}
	if row.TopReposJSON != repos {
		t.Errorf("top repos not round-tripped: %q", row.TopReposJSON)
	}
	if n := store.OrgCount("torvalds"); n != 2 {
		t.Errorf("OrgCount = %d, want 2", n)
	}
	if n := store.OrgCount("ghost"); n != 0 {
		t.Errorf("OrgCount unknown user = %d, want 0", n)
	}
}
