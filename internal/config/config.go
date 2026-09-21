package config

import (
	"os"
	"strconv"
	"strings"
	"time"

	"ggstar/internal/auth"
	"ggstar/internal/cache"

	"github.com/joho/godotenv"
)

type Config struct {
	Port         string
	DBPath       string
	NetworkName  string
	ChainID      string
	ChainIDHex   string
	RPCURL       string
	ExplorerURL  string
	FaucetURL    string
	NativeSymbol string
	ContractAddr string

	// Mainnet mirror. The hackathon requires a testnet *and* a mainnet contract
	// address, so badge lookups try mainnet first and fall back to testnet.
	MainnetName         string
	MainnetChainID      string
	MainnetChainIDHex   string
	MainnetRPCURL       string
	MainnetExplorerURL  string
	MainnetContractAddr string

	AIBaseURL   string
	AIAPIKey    string
	AIModel     string
	AITimeout   time.Duration
	AIMaxTokens int

	GitHubAPIURL string
	GitHubToken  string

	RateLimitPerMin int

	// Cache
	CacheDriver   string
	CacheKeyPrefix string
	RedisURL      string
	RedisPassword string
	RedisDB       int

	// Auth
	SessionTTL              time.Duration
	PublicBaseURL           string
	GitHubOAuthClientID     string
	GitHubOAuthClientSecret string
	CookieSecure            bool

	// Proxy
	//
	// Only enable behind a reverse proxy you control. Fiber trusts the
	// X-Forwarded-For header unconditionally unless a trusted-proxy list is
	// configured, and a spoofed client IP would defeat the rate limiter.
	TrustProxy     bool
	TrustedProxies []string
}

// CacheTTL is the lifetime of a cached GitHub snapshot.
const CacheTTL = 10 * time.Minute

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil && v > 0 {
		return v
	}
	return fallback
}

// envIntAllowZero is used for values where 0 is meaningful.
func envIntAllowZero(key string, fallback int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil && v >= 0 {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch raw {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func Load() Config {
	_ = godotenv.Load()

	return Config{
		Port:         env("PORT", "3000"),
		DBPath:       env("DB_PATH", "/data/ggstar.db"),
		NetworkName:  env("NETWORK_NAME", "BOT Chain Testnet"),
		ChainID:      env("NETWORK_CHAIN_ID", "968"),
		ChainIDHex:   env("NETWORK_CHAIN_ID_HEX", "0x3c8"),
		RPCURL:       env("RPC_URL", "https://rpc.bohr.life"),
		ExplorerURL:  env("EXPLORER_URL", "https://scan.bohr.life"),
		FaucetURL:    env("FAUCET_URL", "https://faucet.botchain.ai"),
		NativeSymbol: env("NATIVE_SYMBOL", "BOT"),
		ContractAddr: env("CONTRACT_ADDRESS", ""),

		MainnetName:         env("NETWORK_MAINNET_NAME", "BOT Chain Mainnet"),
		MainnetChainID:      env("NETWORK_MAINNET_CHAIN_ID", "677"),
		MainnetChainIDHex:   env("NETWORK_MAINNET_CHAIN_ID_HEX", "0x2a5"),
		MainnetRPCURL:       env("RPC_URL_MAINNET", "https://rpc.botchain.ai"),
		MainnetExplorerURL:  env("EXPLORER_URL_MAINNET", "https://scan.botchain.ai"),
		MainnetContractAddr: env("CONTRACT_ADDRESS_MAINNET", ""),

		AIBaseURL:   env("AI_BASE_URL", "https://api.openai.com/v1"),
		AIAPIKey:    env("AI_API_KEY", ""),
		AIModel:     env("AI_MODEL", "gpt-4o-mini"),
		AITimeout:   time.Duration(envInt("AI_TIMEOUT_SECONDS", 30)) * time.Second,
		AIMaxTokens: envInt("AI_MAX_TOKENS", 900),

		GitHubAPIURL: env("GITHUB_API_URL", "https://api.github.com"),
		GitHubToken:  env("GITHUB_TOKEN", ""),

		RateLimitPerMin: envInt("RATE_LIMIT_PER_MIN", 20),

		CacheDriver:    strings.ToLower(env("CACHE_DRIVER", "memory")),
		CacheKeyPrefix: env("CACHE_KEY_PREFIX", "ggstar:"),
		RedisURL:       env("REDIS_URL", ""),
		RedisPassword:  env("REDIS_PASSWORD", ""),
		RedisDB:        envIntAllowZero("REDIS_DB", 0),

		SessionTTL:              time.Duration(envInt("SESSION_TTL_HOURS", 72)) * time.Hour,
		PublicBaseURL:           strings.TrimRight(env("PUBLIC_BASE_URL", "http://localhost:3000"), "/"),
		GitHubOAuthClientID:     env("GITHUB_OAUTH_CLIENT_ID", ""),
		GitHubOAuthClientSecret: env("GITHUB_OAUTH_CLIENT_SECRET", ""),
		CookieSecure:            envBool("COOKIE_SECURE", false),

		TrustProxy:     envBool("TRUST_PROXY", false),
		TrustedProxies: envList("TRUSTED_PROXIES"),
	}
}

// envList parses a comma-separated environment variable into a slice.
func envList(key string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ProxyConfig returns the Fiber trusted-proxy settings.
//
// When TRUST_PROXY is false both values are zero, so c.IP() always reports the
// real TCP peer and the rate limiter cannot be spoofed via headers.
func (c Config) ProxyConfig() (enable bool, proxies []string) {
	if !c.TrustProxy {
		return false, nil
	}
	return true, c.TrustedProxies
}

// MainnetConfigured reports whether a mainnet contract address is set.
func (c Config) MainnetConfigured() bool {
	return len(c.MainnetContractAddr) == 42 && c.MainnetContractAddr[:2] == "0x"
}

// OAuthConfig builds the auth client configuration.
func (c Config) OAuthConfig() auth.Config {
	return auth.Config{
		ClientID:     c.GitHubOAuthClientID,
		ClientSecret: c.GitHubOAuthClientSecret,
		RedirectURL:  c.PublicBaseURL + "/auth/github/callback",
	}
}

// OAuthEnabled reports whether GitHub sign-in is usable.
func (c Config) OAuthEnabled() bool {
	return c.GitHubOAuthClientID != "" && c.GitHubOAuthClientSecret != ""
}

// CacheStoreConfig translates the app config into a cache driver config.
func (c Config) CacheStoreConfig() cache.Config {
	return cache.Config{
		Driver:        c.CacheDriver,
		TTL:           CacheTTL,
		RedisURL:      c.RedisURL,
		RedisPassword: c.RedisPassword,
		RedisDB:       c.RedisDB,
		KeyPrefix:     c.CacheKeyPrefix,
		DialTimeout:   3 * time.Second,
		OpTimeout:     2 * time.Second,
	}
}

func (c Config) AIEnabled() bool { return c.AIAPIKey != "" }

func (c Config) GitHubAuthenticated() bool { return c.GitHubToken != "" }
