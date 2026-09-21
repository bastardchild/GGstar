package db

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"ggstar/internal/model"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

// Open creates the SQLite database (and its parent directory) then runs
// idempotent migrations.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}

	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(1)

	if err := database.Ping(); err != nil {
		return nil, err
	}

	s := &Store{db: database}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS profiles (
	github_username  TEXT PRIMARY KEY,
	skill_score      INTEGER NOT NULL DEFAULT 0,
	top_skills       TEXT    NOT NULL DEFAULT '[]',
	top_skill        TEXT    NOT NULL DEFAULT '',
	suggested_titles TEXT    NOT NULL DEFAULT '[]',
	summary          TEXT    NOT NULL DEFAULT '',
	dominant_lang    TEXT    NOT NULL DEFAULT '',
	avatar_url       TEXT    NOT NULL DEFAULT '',
	public_repos     INTEGER NOT NULL DEFAULT 0,
	followers        INTEGER NOT NULL DEFAULT 0,
	total_stars      INTEGER NOT NULL DEFAULT 0,
	stars_updated_at DATETIME,
	updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_profiles_total_stars ON profiles (total_stars DESC);

CREATE TABLE IF NOT EXISTS achievements (
	id               INTEGER PRIMARY KEY AUTOINCREMENT,
	github_username  TEXT NOT NULL,
	achievement_key  TEXT NOT NULL,
	achievement_name TEXT NOT NULL,
	tier             TEXT NOT NULL,
	unlocked_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE (github_username, achievement_key)
);
CREATE INDEX IF NOT EXISTS idx_achievements_user ON achievements (github_username);

CREATE TABLE IF NOT EXISTS orgs (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	github_username TEXT NOT NULL,
	org_login       TEXT NOT NULL,
	UNIQUE (github_username, org_login)
);

CREATE TABLE IF NOT EXISTS schema_version (
	version    INTEGER PRIMARY KEY,
	applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT OR IGNORE INTO schema_version (version) VALUES (1);

CREATE TABLE IF NOT EXISTS users (
	github_id     INTEGER PRIMARY KEY,
	login         TEXT    NOT NULL UNIQUE,
	name          TEXT    NOT NULL DEFAULT '',
	avatar_url    TEXT    NOT NULL DEFAULT '',
	created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	last_login_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- One row per verified on-chain claim. A claim is only stored after the server
-- reads the BadgeMinted event back from the chain and sees that its
-- githubUsername matches the OAuth login of the session that submitted it.
CREATE TABLE IF NOT EXISTS claims (
	token_id         INTEGER PRIMARY KEY,
	wallet           TEXT    NOT NULL,
	github_username  TEXT    NOT NULL,
	tx_hash          TEXT    NOT NULL,
	verified_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE (tx_hash)
);
CREATE INDEX IF NOT EXISTS idx_claims_wallet ON claims (wallet);
CREATE INDEX IF NOT EXISTS idx_claims_username ON claims (github_username);
`
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}

	return s.migrateColumns()
}

// migrateColumns adds columns introduced after the first release. SQLite has no
// "ADD COLUMN IF NOT EXISTS", so the pragma is inspected first to keep the
// migration idempotent.
func (s *Store) migrateColumns() error {
	has, err := s.hasColumn("profiles", "owner_login")
	if err != nil {
		return err
	}
	if has {
		return nil
	}

	_, err = s.db.Exec(`ALTER TABLE profiles ADD COLUMN owner_login TEXT`)
	return err
}

func (s *Store) hasColumn(table, column string) (bool, error) {
	rows, err := s.db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func encodeList(v []string) string {
	if v == nil {
		v = []string{}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func decodeList(raw string) []string {
	out := []string{}
	if raw == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

// UpsertProfile stores an analysis result. Stars are only overwritten when the
// caller explicitly refreshes them (24h TTL handled by the service layer).
//
// ownerLogin is the OAuth login that was signed in when this analysis ran, or
// empty when the profile belongs to somebody else. Only owned profiles appear on
// the leaderboard.
func (s *Store) UpsertProfile(a model.Analysis, refreshStars bool, ownerLogin string) error {
	dominant := a.DominantLang

	_, err := s.db.Exec(`
INSERT INTO profiles (
	github_username, skill_score, top_skills, top_skill, suggested_titles, summary,
	dominant_lang, avatar_url, public_repos, followers, owner_login, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(github_username) DO UPDATE SET
	skill_score      = excluded.skill_score,
	top_skills       = excluded.top_skills,
	top_skill        = excluded.top_skill,
	suggested_titles = excluded.suggested_titles,
	summary          = excluded.summary,
	dominant_lang    = excluded.dominant_lang,
	avatar_url       = excluded.avatar_url,
	public_repos     = excluded.public_repos,
	followers        = excluded.followers,
	-- Never clear an ownership flag that was set earlier: a later anonymous
	-- lookup of the same username must not remove it from the leaderboard.
	owner_login      = COALESCE(NULLIF(excluded.owner_login, ''), profiles.owner_login),
	updated_at       = CURRENT_TIMESTAMP
`,
		a.Username, a.SkillScore, encodeList(a.TopSkills), dominant, encodeList(a.SuggestedTitles),
		a.Summary, dominant, a.AvatarURL, a.PublicRepos, a.Followers, ownerLogin,
	)
	if err != nil {
		return err
	}

	if refreshStars {
		_, err = s.db.Exec(`
UPDATE profiles SET total_stars = ?, stars_updated_at = CURRENT_TIMESTAMP
WHERE github_username = ?`, a.TotalStars, a.Username)
	}
	return err
}

func (s *Store) GetProfile(username string) (model.ProfileRow, bool, error) {
	var row model.ProfileRow
	var topSkills, titles string
	var starsUpdated sql.NullString

	err := s.db.QueryRow(`
SELECT github_username, skill_score, top_skills, suggested_titles, summary,
       dominant_lang, total_stars, COALESCE(stars_updated_at, ''), updated_at
FROM profiles WHERE github_username = ?`, username).
		Scan(&row.Username, &row.SkillScore, &topSkills, &titles, &row.Summary,
			&row.DominantLang, &row.TotalStars, &starsUpdated, &row.UpdatedAt)
	if err == sql.ErrNoRows {
		return model.ProfileRow{}, false, nil
	}
	if err != nil {
		return model.ProfileRow{}, false, err
	}

	row.TopSkills = decodeList(topSkills)
	row.SuggestedTitles = decodeList(titles)
	row.StarsUpdatedAt = starsUpdated.String
	return row, true, nil
}

func (s *Store) StarsFresh(username string, maxAge time.Duration) bool {
	var raw sql.NullString
	err := s.db.QueryRow(`SELECT stars_updated_at FROM profiles WHERE github_username = ?`, username).Scan(&raw)
	if err != nil || !raw.Valid || raw.String == "" {
		return false
	}
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339} {
		if t, err := time.Parse(layout, raw.String); err == nil {
			return time.Since(t) < maxAge
		}
	}
	return false
}

func (s *Store) CountProfiles() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM profiles`).Scan(&n)
	return n, err
}

type LeaderRow struct {
	Username    string `json:"username"`
	SkillScore  int    `json:"skillScore"`
	TotalStars  int    `json:"totalStars"`
	TopSkill    string `json:"topSkill"`
	AvatarURL   string `json:"avatarUrl"`
	GithubURL   string `json:"githubUrl"`
}

// Leaderboard lists only profiles whose owner signed in with GitHub, so a badge
// or ranking cannot be claimed for somebody else's account.
func (s *Store) Leaderboard(limit int) ([]LeaderRow, error) {
	rows, err := s.db.Query(`
SELECT p.github_username, p.skill_score, p.total_stars, p.top_skill, p.avatar_url
FROM profiles p
JOIN users u ON u.login = p.owner_login
WHERE p.owner_login IS NOT NULL AND p.owner_login <> ''
ORDER BY p.total_stars DESC, p.skill_score DESC
LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []LeaderRow{}
	for rows.Next() {
		var r LeaderRow
		if err := rows.Scan(&r.Username, &r.SkillScore, &r.TotalStars, &r.TopSkill, &r.AvatarURL); err != nil {
			return nil, err
		}
		r.GithubURL = "https://github.com/" + r.Username
		out = append(out, r)
	}
	return out, rows.Err()
}

// User is a GitHub account that has signed in at least once.
type User struct {
	GithubID    int64  `json:"githubId"`
	Login       string `json:"login"`
	Name        string `json:"name"`
	AvatarURL   string `json:"avatarUrl"`
	CreatedAt   string `json:"createdAt"`
	LastLoginAt string `json:"lastLoginAt"`
}

// UpsertUser records a successful OAuth sign-in.
func (s *Store) UpsertUser(u User) error {
	_, err := s.db.Exec(`
INSERT INTO users (github_id, login, name, avatar_url, last_login_at)
VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(github_id) DO UPDATE SET
	login         = excluded.login,
	name          = excluded.name,
	avatar_url    = excluded.avatar_url,
	last_login_at = CURRENT_TIMESTAMP`,
		u.GithubID, u.Login, u.Name, u.AvatarURL)
	return err
}

func (s *Store) GetUser(login string) (User, bool, error) {
	var u User
	err := s.db.QueryRow(`
SELECT github_id, login, name, avatar_url, created_at, last_login_at
FROM users WHERE login = ?`, login).
		Scan(&u.GithubID, &u.Login, &u.Name, &u.AvatarURL, &u.CreatedAt, &u.LastLoginAt)
	if err == sql.ErrNoRows {
		return User{}, false, nil
	}
	if err != nil {
		return User{}, false, err
	}
	return u, true, nil
}

func (s *Store) CountUsers() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// Claim is a verified link between an on-chain badge and an OAuth identity.
type Claim struct {
	TokenID        int64  `json:"tokenId"`
	Wallet         string `json:"wallet"`
	GithubUsername string `json:"githubUsername"`
	TxHash         string `json:"txHash"`
	VerifiedAt     string `json:"verifiedAt"`
}

func (s *Store) UpsertClaim(c Claim) error {
	_, err := s.db.Exec(`
INSERT INTO claims (token_id, wallet, github_username, tx_hash)
VALUES (?, ?, ?, ?)
ON CONFLICT(token_id) DO UPDATE SET
	wallet          = excluded.wallet,
	github_username = excluded.github_username,
	tx_hash         = excluded.tx_hash,
	verified_at     = CURRENT_TIMESTAMP`,
		c.TokenID, c.Wallet, c.GithubUsername, c.TxHash)
	return err
}

func (s *Store) GetClaimByWallet(wallet string) (Claim, bool, error) {
	var c Claim
	err := s.db.QueryRow(`
SELECT token_id, wallet, github_username, tx_hash, verified_at
FROM claims WHERE wallet = ? ORDER BY verified_at DESC LIMIT 1`, wallet).
		Scan(&c.TokenID, &c.Wallet, &c.GithubUsername, &c.TxHash, &c.VerifiedAt)
	if err == sql.ErrNoRows {
		return Claim{}, false, nil
	}
	if err != nil {
		return Claim{}, false, err
	}
	return c, true, nil
}

type AchievementRow struct {
	Key        string `json:"key"`
	Name       string `json:"name"`
	Tier       string `json:"tier"`
	UnlockedAt string `json:"unlockedAt"`
}

func (s *Store) ReplaceAchievements(username string, list []AchievementRow) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM achievements WHERE github_username = ?`, username); err != nil {
		return err
	}
	for _, a := range list {
		if _, err := tx.Exec(`
INSERT INTO achievements (github_username, achievement_key, achievement_name, tier)
VALUES (?, ?, ?, ?)
ON CONFLICT(github_username, achievement_key) DO NOTHING`,
			username, a.Key, a.Name, a.Tier); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Achievements(username string) ([]AchievementRow, error) {
	rows, err := s.db.Query(`
SELECT achievement_key, achievement_name, tier, unlocked_at
FROM achievements WHERE github_username = ? ORDER BY tier, achievement_key`, username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []AchievementRow{}
	for rows.Next() {
		var a AchievementRow
		if err := rows.Scan(&a.Key, &a.Name, &a.Tier, &a.UnlockedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) ReplaceOrgs(username string, orgs []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM orgs WHERE github_username = ?`, username); err != nil {
		return err
	}
	for _, o := range orgs {
		if _, err := tx.Exec(`INSERT INTO orgs (github_username, org_login) VALUES (?, ?)
ON CONFLICT(github_username, org_login) DO NOTHING`, username, o); err != nil {
			return err
		}
	}
	return tx.Commit()
}
