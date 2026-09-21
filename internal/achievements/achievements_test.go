package achievements

import (
	"testing"
	"time"

	"ggstar/internal/github"
)

func snapshot() *github.Snapshot {
	now := time.Now().UTC()

	return &github.Snapshot{
		Username: "tester",
		Profile: github.Profile{
			Login:     "tester",
			PublicRepos: 25,
			Followers: 150,
			CreatedAt: now.AddDate(-6, 0, 0).Format(time.RFC3339),
		},
		Repos: []github.Repo{
			{Name: "big", FullName: "tester/big", Language: "C", Stars: 600, License: &struct {
				SPDXID string `json:"spdx_id"`
			}{SPDXID: "MIT"}},
			{Name: "sol", FullName: "tester/sol", Language: "Solidity", Stars: 20, Topics: []string{"web3"}},
			{Name: "docker-infra", FullName: "tester/docker-infra", Language: "Dockerfile", Stars: 5, License: &struct {
				SPDXID string `json:"spdx_id"`
			}{SPDXID: "Apache-2.0"}},
			{Name: "ai", FullName: "tester/ai", Language: "Python", Stars: 1},
		},
		Orgs:      []string{"linux", "git", "cncf"},
		Languages: map[string]int{"C": 9000, "Solidity": 4000, "Go": 3000, "Python": 1000},
		Events: []github.Event{
			{Type: "PushEvent", CreatedAt: now.Add(-2 * time.Hour).Format(time.RFC3339)},
			{Type: "PushEvent", CreatedAt: now.Add(-30 * time.Hour).Format(time.RFC3339)},
			{Type: "WatchEvent", CreatedAt: now.Add(-1 * time.Hour).Format(time.RFC3339)},
		},
	}
}

func keys(rows []struct{ Key string }) map[string]bool { return nil }

func unlockSet() map[string]bool {
	out := map[string]bool{}
	for _, a := range Evaluate(snapshot()) {
		out[a.Key] = true
	}
	return out
}

func TestEvaluateUnlocksExpectedLadder(t *testing.T) {
	got := unlockSet()

	// 625 stars -> Stardust(10) and Nova(100), but not Supernova(1000).
	if !got["stardust"] || !got["nova"] {
		t.Error("expected stardust + nova for 625 stars")
	}
	if got["supernova"] || got["blackhole"] {
		t.Error("must not unlock 1000+ star tiers")
	}

	// 4 owned repos -> below the 5-repo Seedling threshold.
	if got["seedling"] {
		t.Error("4 repos must not unlock Seedling (needs 5)")
	}

	// Max repo 600 stars -> Headshot(50), Grand Prize(100), Hall of Fame(500).
	if !got["headshot"] || !got["grandprize"] || !got["halloffame"] {
		t.Error("expected champion board tiers up to Hall of Fame")
	}
	if got["legend"] {
		t.Error("600 stars must not unlock Legend (needs 1000)")
	}

	// 4 languages -> Solo Caster(1) + Shapeshifter(3).
	if !got["solocaster"] || !got["shapeshifter"] {
		t.Error("expected polyglot tiers for 4 languages")
	}
	if got["hybrid"] {
		t.Error("4 languages must not unlock Hybrid (needs 5)")
	}

	// Followers 150 -> Ping(10) + Signal(100).
	if !got["ping"] || !got["signal"] || got["broadcast"] {
		t.Error("expected follower tiers up to Signal only")
	}

	// 6 years old -> Veteran(5) but not Ancient(10).
	if !got["veteran"] || got["ancient"] {
		t.Error("expected Veteran for a 6-year-old account")
	}

	// Orgs: 3 -> Party Member + Guild Leader, not Council(5).
	if !got["partymember"] || !got["guildleader"] || got["council"] {
		t.Error("expected org tiers up to Guild Leader")
	}

	// Tech stack: solidity + python + docker -> 3 classes -> Swiss Army.
	for _, k := range []string{"chaincrafter", "botwhisperer", "infradruid", "swissarmy"} {
		if !got[k] {
			t.Errorf("expected %s to unlock", k)
		}
	}

	// Push 2h ago -> Just Shipped + On Fire.
	if !got["justshipped"] || !got["onfire"] {
		t.Error("expected pulse achievements for a recent push")
	}
}

func TestEvaluateEmptySnapshotUnlocksNothing(t *testing.T) {
	empty := &github.Snapshot{Languages: map[string]int{}}

	if n := len(Evaluate(empty)); n != 0 {
		t.Errorf("empty snapshot should unlock nothing, got %d", n)
	}
}

func TestFreshSpawnUnlocksForNewAccount(t *testing.T) {
	snap := snapshot()
	snap.Profile.CreatedAt = time.Now().UTC().AddDate(0, -2, 0).Format(time.RFC3339)

	if !unlockSetFrom(snap)["freshspawn"] {
		t.Error("2-month-old account should unlock Fresh Spawn")
	}
	if unlockSetFrom(snap)["veteran"] {
		t.Error("2-month-old account must not be a Veteran")
	}
}

func unlockSetFrom(s *github.Snapshot) map[string]bool {
	out := map[string]bool{}
	for _, a := range Evaluate(s) {
		out[a.Key] = true
	}
	return out
}

func TestScheduleAchievementsUseUTC(t *testing.T) {
	// Sunday 01:00 UTC -> Night Owl (hour < 4) and Weekend Warrior.
	sunday := time.Date(2026, 9, 20, 1, 0, 0, 0, time.UTC)
	if sunday.Weekday() != time.Sunday {
		t.Fatalf("test fixture wrong weekday: %s", sunday.Weekday())
	}

	snap := snapshot()
	snap.Events = []github.Event{{Type: "PushEvent", CreatedAt: sunday.Format(time.RFC3339)}}

	got := unlockSetFrom(snap)
	if !got["nightowl"] {
		t.Error("01:00 UTC push should unlock Night Owl")
	}
	if !got["weekendwarrior"] {
		t.Error("Sunday push should unlock Weekend Warrior")
	}
}

func TestSpeedsterCountsCommitsPerDay(t *testing.T) {
	day := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)

	snap := snapshot()
	snap.Events = []github.Event{
		{Type: "PushEvent", CreatedAt: day.Format(time.RFC3339), Payload: struct {
			Size    int `json:"size"`
			Commits []struct {
				Message string `json:"message"`
			} `json:"commits"`
		}{Size: 7}},
		{Type: "PushEvent", CreatedAt: day.Add(2 * time.Hour).Format(time.RFC3339), Payload: struct {
			Size    int `json:"size"`
			Commits []struct {
				Message string `json:"message"`
			} `json:"commits"`
		}{Size: 4}},
	}

	if !unlockSetFrom(snap)["speedster"] {
		t.Error("7+4 commits in one day should unlock Speedster")
	}
}

func TestCatalogueIncludesLockedEntries(t *testing.T) {
	empty := &github.Snapshot{Languages: map[string]int{}}
	catalogue := Catalogue(Evaluate(empty))

	if len(catalogue) != len(Definitions) {
		t.Errorf("catalogue size = %d, want %d", len(catalogue), len(Definitions))
	}
	for _, entry := range catalogue {
		if entry.Unlocked {
			t.Errorf("%s should be locked", entry.Key)
		}
		if entry.Hint == "" || entry.Icon == "" || entry.Category == "" {
			t.Errorf("%s missing presentation metadata", entry.Key)
		}
	}
}

func TestCatalogueMarksUnlocked(t *testing.T) {
	catalogue := Catalogue(Evaluate(snapshot()))

	unlocked := 0
	for _, e := range catalogue {
		if e.Unlocked {
			unlocked++
		}
	}
	if unlocked == 0 {
		t.Fatal("expected some unlocked entries")
	}
	if unlocked == len(catalogue) {
		t.Error("a mid-tier profile should not unlock everything")
	}
}

func TestLadderTierProgression(t *testing.T) {
	byKey := map[string]string{}
	for _, d := range Definitions {
		byKey[d.Key] = d.Tier
	}

	expect := map[string]string{
		"stardust": TierCommon, "nova": TierRare,
		"supernova": TierEpic, "blackhole": TierLegendary,
	}
	for key, tier := range expect {
		if byKey[key] != tier {
			t.Errorf("%s tier = %s, want %s", key, byKey[key], tier)
		}
	}
}

func TestNoDuplicateKeys(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range Definitions {
		if seen[d.Key] {
			t.Errorf("duplicate achievement key %q", d.Key)
		}
		seen[d.Key] = true
	}
}
