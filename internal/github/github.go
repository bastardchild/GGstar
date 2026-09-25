package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Profile struct {
	Login       string `json:"login"`
	Name        string `json:"name"`
	AvatarURL   string `json:"avatar_url"`
	Bio         string `json:"bio"`
	Company     string `json:"company"`
	Location    string `json:"location"`
	Blog        string `json:"blog"`
	PublicRepos int    `json:"public_repos"`
	Followers   int    `json:"followers"`
	Following   int    `json:"following"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
	HTMLURL     string `json:"html_url"`
}

type Repo struct {
	Name            string   `json:"name"`
	FullName        string   `json:"full_name"`
	Description     string   `json:"description"`
	Language        string   `json:"language"`
	Stars           int      `json:"stargazers_count"`
	Forks           int      `json:"forks_count"`
	Fork            bool     `json:"fork"`
	Archived        bool     `json:"archived"`
	Topics          []string `json:"topics"`
	License         *struct {
		SPDXID string `json:"spdx_id"`
	} `json:"license"`
	Size      int    `json:"size"`
	PushedAt  string `json:"pushed_at"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	HTMLURL   string `json:"html_url"`
}

type Org struct {
	Login string `json:"login"`
}

type Event struct {
	Type      string `json:"type"`
	CreatedAt string `json:"created_at"`
	Payload   struct {
		Size    int `json:"size"`
		Commits []struct {
			Message string `json:"message"`
		} `json:"commits"`
	} `json:"payload"`
	Repo struct {
		Name string `json:"name"`
	} `json:"repo"`
}

type Snapshot struct {
	Username  string         `json:"username"`
	Profile   Profile        `json:"profile"`
	Repos     []Repo         `json:"repos"`
	Orgs      []string       `json:"orgs"`
	Events    []Event        `json:"events"`
	Languages map[string]int `json:"languages"`

	FetchedAt time.Time `json:"fetchedAt"`
	Requests  int       `json:"requests"`

	// ReposPartial is true when the repo list could not be fetched completely
	// (e.g. a paged request failed after earlier pages succeeded). Totals
	// derived from such a snapshot must be shown but never persisted as fact.
	ReposPartial bool `json:"reposPartial"`
}

type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func New(baseURL, token string) *Client {
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		HTTP:    &http.Client{Timeout: 25 * time.Second},
	}
}

type httpError struct {
	Status     int
	Message    string
	RateLimit  string
	RetryAfter string
}

func (e *httpError) Error() string {
	if e.Status == http.StatusNotFound {
		return "github user not found"
	}
	if e.Status == http.StatusForbidden && e.RateLimit == "0" {
		return "github rate limit exceeded (set GITHUB_TOKEN to raise the limit)"
	}
	if e.Message != "" {
		return fmt.Sprintf("github api %d: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("github api returned %d", e.Status)
}

func IsNotFound(err error) bool {
	var he *httpError
	return err != nil && asHTTPError(err, &he) && he.Status == http.StatusNotFound
}

func IsRateLimited(err error) bool {
	var he *httpError
	return err != nil && asHTTPError(err, &he) && he.Status == http.StatusForbidden
}

func asHTTPError(err error, target **httpError) bool {
	if he, ok := err.(*httpError); ok {
		*target = he
		return true
	}
	return false
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}

	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "ggstar-hackathon")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		he := &httpError{
			Status:     resp.StatusCode,
			RateLimit:  resp.Header.Get("X-RateLimit-Remaining"),
			RetryAfter: resp.Header.Get("Retry-After"),
		}
		var payload struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(body, &payload)
		he.Message = payload.Message
		return he
	}

	if out == nil {
		return nil
	}
	return json.Unmarshal(body, out)
}

// GetProfile fetches GET /users/{username}.
func (c *Client) GetProfile(ctx context.Context, username string) (Profile, error) {
	var p Profile
	err := c.get(ctx, "/users/"+url.PathEscape(username), &p)
	return p, err
}

// GetRepos fetches up to max public repos sorted by stars (most relevant first).
func (c *Client) GetRepos(ctx context.Context, username string, max int) ([]Repo, error) {
	if max <= 0 {
		max = 100
	}

	var all []Repo
	for page := 1; page <= 3; page++ {
		var batch []Repo
		path := fmt.Sprintf("/users/%s/repos?per_page=100&page=%d&sort=updated", url.PathEscape(username), page)
		if err := c.get(ctx, path, &batch); err != nil {
			return all, err
		}
		all = append(all, batch...)
		if len(batch) < 100 || len(all) >= max {
			break
		}
	}

	sort.SliceStable(all, func(i, j int) bool { return all[i].Stars > all[j].Stars })
	if len(all) > max {
		all = all[:max]
	}
	return all, nil
}

// SumStars walks every page of /users/{u}/repos and sums stargazers_count.
func (c *Client) SumStars(ctx context.Context, username string) (int, error) {
	total := 0
	for page := 1; page <= 10; page++ {
		var batch []Repo
		path := fmt.Sprintf("/users/%s/repos?per_page=100&page=%d", url.PathEscape(username), page)
		if err := c.get(ctx, path, &batch); err != nil {
			return total, err
		}
		for _, r := range batch {
			total += r.Stars
		}
		if len(batch) < 100 {
			break
		}
	}
	return total, nil
}

func (c *Client) GetOrgs(ctx context.Context, username string) ([]string, error) {
	var orgs []Org
	if err := c.get(ctx, "/users/"+url.PathEscape(username)+"/orgs", &orgs); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(orgs))
	for _, o := range orgs {
		if o.Login != "" {
			out = append(out, o.Login)
		}
	}
	return out, nil
}

func (c *Client) GetEvents(ctx context.Context, username string) ([]Event, error) {
	var events []Event
	path := "/users/" + url.PathEscape(username) + "/events/public?per_page=100"
	if err := c.get(ctx, path, &events); err != nil {
		return nil, err
	}
	return events, nil
}

// GetLanguages returns byte counts per language for a repo (one request per repo).
func (c *Client) GetLanguages(ctx context.Context, fullName string) (map[string]int, error) {
	parts := strings.SplitN(fullName, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid repo full name %q", fullName)
	}

	var langs map[string]int
	path := "/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]) + "/languages"
	if err := c.get(ctx, path, &langs); err != nil {
		return nil, err
	}
	return langs, nil
}

// BuildSnapshot fetches profile, repos, orgs, events and repo languages with
// bounded concurrency. Partial failures degrade gracefully.
func (c *Client) BuildSnapshot(ctx context.Context, username string, langRepos int) (*Snapshot, error) {
	snap := &Snapshot{Username: username, Languages: map[string]int{}, FetchedAt: time.Now()}

	profile, err := c.GetProfile(ctx, username)
	if err != nil {
		return nil, err
	}
	snap.Profile = profile
	snap.Requests++

	repos, err := c.GetRepos(ctx, username, 100)
	if err != nil && len(repos) == 0 {
		return nil, err
	}
	snap.Repos = repos
	snap.ReposPartial = err != nil
	snap.Requests += 3

	if orgs, err := c.GetOrgs(ctx, username); err == nil {
		snap.Orgs = orgs
		snap.Requests++
	}
	if events, err := c.GetEvents(ctx, username); err == nil {
		snap.Events = events
		snap.Requests++
	}

	if langRepos > len(repos) {
		langRepos = len(repos)
	}
	if langRepos > 0 {
		targets := repos[:langRepos]
		var mu sync.Mutex
		var wg sync.WaitGroup
		sem := make(chan struct{}, 4)

		for _, r := range targets {
			if r.Fork {
				continue
			}
			wg.Add(1)
			go func(full string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				langs, err := c.GetLanguages(ctx, full)
				if err != nil {
					return
				}
				mu.Lock()
				for k, v := range langs {
					snap.Languages[k] += v
				}
				mu.Unlock()
			}(r.FullName)
		}
		wg.Wait()
	}

	return snap, nil
}

func (s *Snapshot) TotalStars() int {
	total := 0
	for _, r := range s.Repos {
		total += r.Stars
	}
	return total
}

func (s *Snapshot) MaxRepoStars() int {
	best := 0
	for _, r := range s.Repos {
		if r.Stars > best {
			best = r.Stars
		}
	}
	return best
}

func (s *Snapshot) OwnRepoCount() int {
	n := 0
	for _, r := range s.Repos {
		if !r.Fork {
			n++
		}
	}
	return n
}

func (s *Snapshot) LicenseRatio() float64 {
	owned := s.OwnRepoCount()
	if owned == 0 {
		return 0
	}
	with := 0
	for _, r := range s.Repos {
		if r.Fork {
			continue
		}
		if r.License != nil && r.License.SPDXID != "" && r.License.SPDXID != "NOASSERTION" {
			with++
		}
	}
	return float64(with) / float64(owned)
}

// DominantLanguage prefers real byte counts, falling back to repo language tags.
func (s *Snapshot) DominantLanguage() string {
	if len(s.Languages) > 0 {
		best, bestBytes := "", -1
		for lang, bytes := range s.Languages {
			if bytes > bestBytes {
				best, bestBytes = lang, bytes
			}
		}
		return best
	}

	counts := map[string]int{}
	for _, r := range s.Repos {
		if r.Language != "" {
			counts[r.Language]++
		}
	}
	best, bestN := "", -1
	for lang, n := range counts {
		if n > bestN {
			best, bestN = lang, n
		}
	}
	return best
}

// LanguageNames returns every language seen, strongest first.
func (s *Snapshot) LanguageNames(limit int) []string {
	type kv struct {
		name string
		size int
	}

	var list []kv
	if len(s.Languages) > 0 {
		for k, v := range s.Languages {
			list = append(list, kv{k, v})
		}
	} else {
		counts := map[string]int{}
		for _, r := range s.Repos {
			if r.Language != "" {
				counts[r.Language]++
			}
		}
		for k, v := range counts {
			list = append(list, kv{k, v})
		}
	}

	sort.Slice(list, func(i, j int) bool { return list[i].size > list[j].size })
	out := make([]string, 0, len(list))
	for _, e := range list {
		out = append(out, e.name)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

// TechCorpus is a lowercase blob of repo names, topics and languages used for
// tech-stack achievement detection.
func (s *Snapshot) TechCorpus() string {
	var b strings.Builder
	for _, r := range s.Repos {
		b.WriteString(strings.ToLower(r.Name))
		b.WriteString(" ")
		b.WriteString(strings.ToLower(r.Description))
		b.WriteString(" ")
		b.WriteString(strings.ToLower(r.Language))
		b.WriteString(" ")
		for _, t := range r.Topics {
			b.WriteString(strings.ToLower(t))
			b.WriteString(" ")
		}
	}
	for lang := range s.Languages {
		b.WriteString(strings.ToLower(lang))
		b.WriteString(" ")
	}
	return b.String()
}

func (s *Snapshot) AccountAgeYears() float64 {
	years, _ := s.AccountAge()
	return years
}

// AccountAge reports the age of the account in years. ok is false when GitHub
// did not return a usable created_at value, so callers can avoid awarding
// age-based achievements on incomplete data.
func (s *Snapshot) AccountAge() (float64, bool) {
	t, err := time.Parse(time.RFC3339, s.Profile.CreatedAt)
	if err != nil {
		return 0, false
	}
	return time.Since(t).Hours() / (24 * 365.25), true
}

func ParseTime(raw string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func AtoiSafe(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
