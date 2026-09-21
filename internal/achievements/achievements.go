package achievements

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"ggstar/internal/db"
	"ggstar/internal/github"
)

const (
	TierCommon    = "common"
	TierRare      = "rare"
	TierEpic      = "epic"
	TierLegendary = "legendary"
)

type Definition struct {
	Key      string
	Name     string
	Tier     string
	Category string
	Icon     string
	Hint     string
	Test     func(s *github.Snapshot) bool
}

func tierOf(n int) string {
	switch n {
	case 0:
		return TierCommon
	case 1:
		return TierRare
	case 2:
		return TierEpic
	default:
		return TierLegendary
	}
}

// ladder builds an ordered Common→Legendary ladder from parallel slices.
func ladder(category, icon, hint string, metric func(*github.Snapshot) float64, keys, names []string, thresholds []float64) []Definition {
	out := make([]Definition, 0, len(thresholds))
	for i := range thresholds {
		if i >= len(keys) || i >= len(names) {
			break
		}
		threshold := thresholds[i]
		out = append(out, Definition{
			Key:      keys[i],
			Name:     names[i],
			Tier:     tierOf(i),
			Category: category,
			Icon:     icon,
			Hint:     hint,
			Test:     func(s *github.Snapshot) bool { return metric(s) >= threshold },
		})
	}
	return out
}

var Definitions = buildDefinitions()

func buildDefinitions() []Definition {
	defs := []Definition{}

	defs = append(defs, ladder("Star Path", "⭐", "Total stars earned across public repos",
		func(s *github.Snapshot) float64 { return float64(s.TotalStars()) },
		[]string{"stardust", "nova", "supernova", "blackhole"},
		[]string{"Stardust", "Nova", "Supernova", "Black Hole"},
		[]float64{10, 100, 1000, 10000})...)

	defs = append(defs, ladder("World Builder", "📦", "Number of public repositories you own",
		func(s *github.Snapshot) float64 { return float64(s.OwnRepoCount()) },
		[]string{"seedling", "architect", "kingdom", "worldbuilder"},
		[]string{"Seedling", "Architect", "Kingdom", "World Builder"},
		[]float64{5, 20, 50, 100})...)

	defs = append(defs, ladder("Champion Board", "🏆", "Stars on your single most popular repo",
		func(s *github.Snapshot) float64 { return float64(s.MaxRepoStars()) },
		[]string{"headshot", "grandprize", "halloffame", "legend"},
		[]string{"Headshot", "Grand Prize", "Hall of Fame", "Legend"},
		[]float64{50, 100, 500, 1000})...)

	defs = append(defs, ladder("Polyglot Rank", "🧬", "Distinct programming languages used",
		func(s *github.Snapshot) float64 { return float64(len(s.LanguageNames(0))) },
		[]string{"solocaster", "shapeshifter", "hybrid", "prism"},
		[]string{"Solo Caster", "Shapeshifter", "Hybrid", "Prism"},
		[]float64{1, 3, 5, 8})...)

	defs = append(defs, ladder("Signal Strength", "📡", "GitHub followers",
		func(s *github.Snapshot) float64 { return float64(s.Profile.Followers) },
		[]string{"ping", "signal", "broadcast", "tower"},
		[]string{"Ping", "Signal", "Broadcast", "Tower"},
		[]float64{10, 100, 500, 1000})...)

	defs = append(defs, timeWarpDefs()...)
	defs = append(defs, partyDefs()...)
	defs = append(defs, techDefs()...)
	defs = append(defs, licenseDefs()...)
	defs = append(defs, pulseDefs()...)
	defs = append(defs, statDefs()...)

	sort.SliceStable(defs, func(i, j int) bool { return defs[i].Key < defs[j].Key })
	return defs
}

func timeWarpDefs() []Definition {
	ladder := []struct {
		key    string
		name   string
		tier   string
		years  float64
	}{
		{"freshspawn", "Fresh Spawn", TierCommon, 0},
		{"veteran", "Veteran", TierRare, 5},
		{"ancient", "Ancient", TierEpic, 10},
		{"ogplayer", "OG Player", TierLegendary, 15},
	}

	out := make([]Definition, 0, len(ladder))
	for _, l := range ladder {
		threshold := l.years
		out = append(out, Definition{
			Key:      l.key,
			Name:     l.name,
			Tier:     l.tier,
			Category: "Time Warp",
			Icon:     "⏳",
			Hint:     "Age of your GitHub account",
			Test: func(s *github.Snapshot) bool {
				years, ok := s.AccountAge()
				if !ok {
					return false
				}
				return years >= threshold
			},
		})
	}
	return out
}

func partyDefs() []Definition {
	return []Definition{
		{
			Key: "partymember", Name: "Party Member", Tier: TierCommon, Category: "Party System", Icon: "👥",
			Hint: "Member of 1+ GitHub organizations",
			Test: func(s *github.Snapshot) bool { return len(s.Orgs) >= 1 },
		},
		{
			Key: "guildleader", Name: "Guild Leader", Tier: TierRare, Category: "Party System", Icon: "👥",
			Hint: "Member of 3+ GitHub organizations",
			Test: func(s *github.Snapshot) bool { return len(s.Orgs) >= 3 },
		},
		{
			Key: "council", Name: "Council", Tier: TierEpic, Category: "Party System", Icon: "👥",
			Hint: "Member of 5+ GitHub organizations",
			Test: func(s *github.Snapshot) bool { return len(s.Orgs) >= 5 },
		},
	}
}

func containsAny(corpus string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(corpus, n) {
			return true
		}
	}
	return false
}

func techDefs() []Definition {
	chain := []string{"solidity", "hardhat", "foundry", "ethereum", "web3", "contract"}
	bot := []string{"tensorflow", "pytorch", "python", "openai", "llm", "langchain", "ml "}
	pixel := []string{"react", "html", "css", "tailwind", "vue", "svelte", "figma"}
	infra := []string{"docker", "kubernetes", "terraform", "ansible", "helm", "ansible"}

	return []Definition{
		{
			Key: "chaincrafter", Name: "Chain Crafter", Tier: TierCommon, Category: "Tech Stack", Icon: "⚡",
			Hint: "Repos touching solidity / hardhat / web3",
			Test: func(s *github.Snapshot) bool { return containsAny(s.TechCorpus(), chain) },
		},
		{
			Key: "botwhisperer", Name: "Bot Whisperer", Tier: TierCommon, Category: "Tech Stack", Icon: "⚡",
			Hint: "Repos touching python / tensorflow / pytorch",
			Test: func(s *github.Snapshot) bool { return containsAny(s.TechCorpus(), bot) },
		},
		{
			Key: "pixelmage", Name: "Pixel Mage", Tier: TierCommon, Category: "Tech Stack", Icon: "⚡",
			Hint: "Repos touching html / css / react",
			Test: func(s *github.Snapshot) bool { return containsAny(s.TechCorpus(), pixel) },
		},
		{
			Key: "infradruid", Name: "Infra Druid", Tier: TierCommon, Category: "Tech Stack", Icon: "⚡",
			Hint: "Repos touching docker / kubernetes",
			Test: func(s *github.Snapshot) bool { return containsAny(s.TechCorpus(), infra) },
		},
		{
			Key: "swissarmy", Name: "Swiss Army", Tier: TierRare, Category: "Tech Stack", Icon: "⚡",
			Hint: "3+ tech-stack classes active at once",
			Test: func(s *github.Snapshot) bool {
				corpus := s.TechCorpus()
				n := 0
				for _, set := range [][]string{chain, bot, pixel, infra} {
					if containsAny(corpus, set) {
						n++
					}
				}
				return n >= 3
			},
		},
	}
}

func licenseDefs() []Definition {
	return []Definition{
		{
			Key: "friendlyfork", Name: "Friendly Fork", Tier: TierCommon, Category: "Open Hand", Icon: "📜",
			Hint: "20%+ of your repos include a LICENSE",
			Test: func(s *github.Snapshot) bool { return s.OwnRepoCount() > 0 && s.LicenseRatio() >= 0.20 },
		},
		{
			Key: "openspirit", Name: "Open Spirit", Tier: TierRare, Category: "Open Hand", Icon: "📜",
			Hint: "50%+ of your repos include a LICENSE",
			Test: func(s *github.Snapshot) bool { return s.OwnRepoCount() > 0 && s.LicenseRatio() >= 0.50 },
		},
		{
			Key: "freecodehero", Name: "Free Code Hero", Tier: TierEpic, Category: "Open Hand", Icon: "📜",
			Hint: "80%+ of your repos include a LICENSE",
			Test: func(s *github.Snapshot) bool { return s.OwnRepoCount() > 0 && s.LicenseRatio() >= 0.80 },
		},
	}
}

func dayKey(t time.Time) string { return t.UTC().Format("2006-01-02") }

func pushDays(s *github.Snapshot) map[string]int {
	days := map[string]int{}
	for _, e := range s.Events {
		if e.Type != "PushEvent" {
			continue
		}
		t, ok := github.ParseTime(e.CreatedAt)
		if !ok {
			continue
		}
		days[dayKey(t)]++
	}
	return days
}

func maxStreak(days map[string]int) int {
	best, run := 0, 0
	now := time.Now().UTC()
	for i := 0; i < 120; i++ {
		d := dayKey(now.AddDate(0, 0, -i))
		if days[d] > 0 {
			run++
			if run > best {
				best = run
			}
		} else {
			run = 0
		}
	}
	return best
}

func pulseDefs() []Definition {
	return []Definition{
		{
			Key: "justshipped", Name: "Just Shipped", Tier: TierCommon, Category: "Pulse Check", Icon: "🔨",
			Hint: "Pushed code in the last 7 days",
			Test: func(s *github.Snapshot) bool {
				for _, e := range s.Events {
					if e.Type != "PushEvent" {
						continue
					}
					if t, ok := github.ParseTime(e.CreatedAt); ok && time.Since(t) < 7*24*time.Hour {
						return true
					}
				}
				return false
			},
		},
		{
			Key: "onfire", Name: "On Fire", Tier: TierRare, Category: "Pulse Check", Icon: "🔨",
			Hint: "Pushed code in the last 24 hours",
			Test: func(s *github.Snapshot) bool {
				for _, e := range s.Events {
					if e.Type != "PushEvent" {
						continue
					}
					if t, ok := github.ParseTime(e.CreatedAt); ok && time.Since(t) < 24*time.Hour {
						return true
					}
				}
				return false
			},
		},
		{
			Key: "machine", Name: "Machine", Tier: TierEpic, Category: "Pulse Check", Icon: "🔨",
			Hint: "Pushed code 3 days in a row",
			Test: func(s *github.Snapshot) bool { return maxStreak(pushDays(s)) >= 3 },
		},
	}
}

func statDefs() []Definition {
	return []Definition{
		{
			Key: "speedster", Name: "Speedster", Tier: TierRare, Category: "Stat Card", Icon: "📊",
			Hint: "10+ commits pushed in a single day",
			Test: func(s *github.Snapshot) bool {
				perDay := map[string]int{}
				for _, e := range s.Events {
					if e.Type != "PushEvent" {
						continue
					}
					t, ok := github.ParseTime(e.CreatedAt)
					if !ok {
						continue
					}
					n := e.Payload.Size
					if n == 0 {
						n = len(e.Payload.Commits)
					}
					perDay[dayKey(t)] += n
				}
				for _, n := range perDay {
					if n >= 10 {
						return true
					}
				}
				return false
			},
		},
		{
			Key: "nightowl", Name: "Night Owl", Tier: TierRare, Category: "Stat Card", Icon: "📊",
			Hint: "Pushed code between 00:00-04:00 UTC",
			Test: func(s *github.Snapshot) bool {
				for _, e := range s.Events {
					if e.Type != "PushEvent" {
						continue
					}
					if t, ok := github.ParseTime(e.CreatedAt); ok && t.UTC().Hour() < 4 {
						return true
					}
				}
				return false
			},
		},
		{
			Key: "weekendwarrior", Name: "Weekend Warrior", Tier: TierEpic, Category: "Stat Card", Icon: "📊",
			Hint: "Pushed code on a Saturday or Sunday",
			Test: func(s *github.Snapshot) bool {
				for _, e := range s.Events {
					if e.Type != "PushEvent" {
						continue
					}
					if t, ok := github.ParseTime(e.CreatedAt); ok {
						if d := t.UTC().Weekday(); d == time.Saturday || d == time.Sunday {
							return true
						}
					}
				}
				return false
			},
		},
	}
}

// Evaluate returns every unlocked achievement for a snapshot.
func Evaluate(s *github.Snapshot) []db.AchievementRow {
	now := time.Now().UTC().Format(time.RFC3339)
	out := []db.AchievementRow{}
	for _, d := range Definitions {
		if d.Test == nil || !d.Test(s) {
			continue
		}
		out = append(out, db.AchievementRow{
			Key:        d.Key,
			Name:       d.Name,
			Tier:       d.Tier,
			UnlockedAt: now,
		})
	}
	return out
}

// Catalogue returns every achievement with its unlock state, for the UI grid.
type CatalogueEntry struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Tier     string `json:"tier"`
	Category string `json:"category"`
	Icon     string `json:"icon"`
	Hint     string `json:"hint"`
	Unlocked bool   `json:"unlocked"`
}

func Catalogue(unlocked []db.AchievementRow) []CatalogueEntry {
	byKey := map[string]string{}
	for _, a := range unlocked {
		byKey[a.Key] = a.Tier
	}

	out := make([]CatalogueEntry, 0, len(Definitions))
	for _, d := range Definitions {
		tier := d.Tier
		if t, ok := byKey[d.Key]; ok {
			tier = t
		}
		out = append(out, CatalogueEntry{
			Key:      d.Key,
			Name:     d.Name,
			Tier:     tier,
			Category: d.Category,
			Icon:     d.Icon,
			Hint:     d.Hint,
			Unlocked: byKey[d.Key] != "",
		})
	}
	return out
}

func TierLabel(tier string) string {
	switch tier {
	case TierRare:
		return "Rare"
	case TierEpic:
		return "Epic"
	case TierLegendary:
		return "Legendary"
	default:
		return "Common"
	}
}

func Summary(unlocked []db.AchievementRow) string {
	counts := map[string]int{}
	for _, a := range unlocked {
		counts[a.Tier]++
	}
	return fmt.Sprintf("%d unlocked (%d common, %d rare, %d epic, %d legendary)",
		len(unlocked), counts[TierCommon], counts[TierRare], counts[TierEpic], counts[TierLegendary])
}
