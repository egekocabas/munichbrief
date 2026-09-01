package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultAddress               = "127.0.0.1:8080"
	defaultMetricsAddr           = "127.0.0.1:9090"
	defaultDatabasePath          = ".data/munichbrief.db"
	defaultGazetteerDatabasePath = ".data/munichbrief-gazetteer.db"
	defaultSourceMode            = "fixture"
	defaultPageSize              = 20
	defaultFeedURL               = "https://www.polizei.bayern.de/rss/polizeiprasidium-munchen.xml"
	defaultUserAgent             = "MunichBrief/dev (+https://github.com/egekocabas/munichbrief)"
	defaultHTTPTimeout           = 10 * time.Second
	defaultRefreshAfter          = 6 * time.Hour
	defaultAIEnabled             = false
	defaultOllamaURL             = "http://127.0.0.1:11434"
	defaultAIInterval            = 15 * time.Second
	defaultAITimeout             = 15 * time.Minute
	defaultAIContext             = 8192
	defaultAIImmediate           = false
	defaultAIWindow              = "03:00-08:00"
	defaultSecureCookie          = false
	defaultGazetteerRefresh      = 7 * 24 * time.Hour
	defaultGazetteerTimeout      = 2 * time.Minute
	repositoryURL                = "https://github.com/egekocabas/munichbrief"
)

// Config contains the application runtime settings.
type Config struct {
	Address                  string
	MetricsAddress           string
	DatabasePath             string
	GazetteerEnabled         bool
	GazetteerDatabasePath    string
	GazetteerRefreshInterval time.Duration
	GazetteerHTTPTimeout     time.Duration
	SourceMode               string
	PageSize                 int
	FeedURL                  string
	UserAgent                string
	HTTPTimeout              time.Duration
	RefreshAfter             time.Duration
	AIEnabled                bool
	OllamaBaseURL            string
	AIInterval               time.Duration
	AITimeout                time.Duration
	AIContextSize            int
	AIImmediate              bool
	AIWindowStart            time.Duration
	AIWindowEnd              time.Duration
	PresentationMode         string
	SecureCookies            bool
	AdminEnabled             bool
	PublicHosts              []string
	CanonicalOrigin          string
}

// Load reads configuration from the environment and applies local-safe defaults.
func Load() (Config, error) {
	cfg := Config{
		Address:                  envOrDefault("MUNICHBRIEF_ADDR", defaultAddress),
		MetricsAddress:           envOrDefault("MUNICHBRIEF_METRICS_ADDR", defaultMetricsAddr),
		DatabasePath:             envOrDefault("MUNICHBRIEF_DATABASE_PATH", defaultDatabasePath),
		GazetteerDatabasePath:    envOrDefault("MUNICHBRIEF_GAZETTEER_DATABASE_PATH", defaultGazetteerDatabasePath),
		GazetteerRefreshInterval: defaultGazetteerRefresh,
		GazetteerHTTPTimeout:     defaultGazetteerTimeout,
		SourceMode:               envOrDefault("MUNICHBRIEF_SOURCE_MODE", defaultSourceMode),
		PageSize:                 defaultPageSize,
		FeedURL:                  envOrDefault("MUNICHBRIEF_FEED_URL", defaultFeedURL),
		UserAgent:                envOrDefault("MUNICHBRIEF_USER_AGENT", defaultUserAgent),
		HTTPTimeout:              defaultHTTPTimeout,
		RefreshAfter:             defaultRefreshAfter,
		AIEnabled:                defaultAIEnabled,
		OllamaBaseURL:            envOrDefault("MUNICHBRIEF_OLLAMA_BASE_URL", defaultOllamaURL),
		AIInterval:               defaultAIInterval,
		AITimeout:                defaultAITimeout,
		AIContextSize:            defaultAIContext,
		AIImmediate:              defaultAIImmediate,
		SecureCookies:            defaultSecureCookie,
	}
	cfg.GazetteerEnabled = cfg.SourceMode == "live"
	var err error
	if cfg.PublicHosts, err = publicHostsFromEnv("MUNICHBRIEF_PUBLIC_HOSTS"); err != nil {
		return Config{}, err
	}
	if cfg.CanonicalOrigin, err = canonicalOriginFromEnv("MUNICHBRIEF_CANONICAL_ORIGIN", cfg.PublicHosts); err != nil {
		return Config{}, err
	}
	defaultPresentationMode := "review"
	if cfg.SourceMode == "live" {
		defaultPresentationMode = "public"
	}
	cfg.PresentationMode = envOrDefault("MUNICHBRIEF_PRESENTATION_MODE", defaultPresentationMode)
	if raw := os.Getenv("MUNICHBRIEF_AI_ENABLED"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return Config{}, fmt.Errorf("MUNICHBRIEF_AI_ENABLED must be true or false")
		}
		cfg.AIEnabled = value
	}
	if raw := os.Getenv("MUNICHBRIEF_GAZETTEER_ENABLED"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return Config{}, fmt.Errorf("MUNICHBRIEF_GAZETTEER_ENABLED must be true or false")
		}
		cfg.GazetteerEnabled = value
	}
	if raw := os.Getenv("MUNICHBRIEF_AI_IMMEDIATE"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return Config{}, fmt.Errorf("MUNICHBRIEF_AI_IMMEDIATE must be true or false")
		}
		cfg.AIImmediate = value
	}
	if raw := os.Getenv("MUNICHBRIEF_SECURE_COOKIES"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return Config{}, fmt.Errorf("MUNICHBRIEF_SECURE_COOKIES must be true or false")
		}
		cfg.SecureCookies = value
	}
	if raw := os.Getenv("MUNICHBRIEF_ADMIN_ENABLED"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return Config{}, fmt.Errorf("MUNICHBRIEF_ADMIN_ENABLED must be true or false")
		}
		cfg.AdminEnabled = value
	}

	if raw := os.Getenv("MUNICHBRIEF_PAGE_SIZE"); raw != "" {
		pageSize, err := strconv.Atoi(raw)
		if err != nil || pageSize < 1 || pageSize > 100 {
			return Config{}, fmt.Errorf("MUNICHBRIEF_PAGE_SIZE must be between 1 and 100")
		}
		cfg.PageSize = pageSize
	}

	if cfg.SourceMode != "fixture" && cfg.SourceMode != "live" {
		return Config{}, fmt.Errorf("unsupported MUNICHBRIEF_SOURCE_MODE %q: use fixture or live", cfg.SourceMode)
	}
	if filepath.Clean(cfg.DatabasePath) == filepath.Clean(cfg.GazetteerDatabasePath) {
		return Config{}, errors.New("MUNICHBRIEF_GAZETTEER_DATABASE_PATH must differ from MUNICHBRIEF_DATABASE_PATH")
	}
	if cfg.PresentationMode != "review" && cfg.PresentationMode != "public" {
		return Config{}, fmt.Errorf("unsupported MUNICHBRIEF_PRESENTATION_MODE %q: use review or public", cfg.PresentationMode)
	}

	if cfg.HTTPTimeout, err = durationFromEnv("MUNICHBRIEF_HTTP_TIMEOUT", cfg.HTTPTimeout, time.Second); err != nil {
		return Config{}, err
	}
	if cfg.RefreshAfter, err = durationFromEnv("MUNICHBRIEF_ARTICLE_REFRESH_INTERVAL", cfg.RefreshAfter, 15*time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.GazetteerRefreshInterval, err = durationFromEnv("MUNICHBRIEF_GAZETTEER_REFRESH_INTERVAL", cfg.GazetteerRefreshInterval, time.Hour); err != nil {
		return Config{}, err
	}
	if cfg.GazetteerHTTPTimeout, err = durationFromEnv("MUNICHBRIEF_GAZETTEER_HTTP_TIMEOUT", cfg.GazetteerHTTPTimeout, 10*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.AIInterval, err = durationFromEnv("MUNICHBRIEF_AI_INTERVAL", cfg.AIInterval, time.Second); err != nil {
		return Config{}, err
	}
	if cfg.AITimeout, err = durationFromEnv("MUNICHBRIEF_AI_TIMEOUT", cfg.AITimeout, time.Second); err != nil {
		return Config{}, err
	}
	if raw := os.Getenv("MUNICHBRIEF_AI_CONTEXT_SIZE"); raw != "" {
		contextSize, parseErr := strconv.Atoi(raw)
		if parseErr != nil || contextSize < 2048 || contextSize > 32768 {
			return Config{}, fmt.Errorf("MUNICHBRIEF_AI_CONTEXT_SIZE must be between 2048 and 32768")
		}
		cfg.AIContextSize = contextSize
	}
	cfg.AIWindowStart, cfg.AIWindowEnd, err = dailyWindowFromEnv("MUNICHBRIEF_AI_WINDOW", defaultAIWindow)
	if err != nil {
		return Config{}, err
	}

	feedURL, err := url.Parse(cfg.FeedURL)
	if err != nil || feedURL.Scheme != "https" || feedURL.Host == "" || feedURL.User != nil {
		return Config{}, fmt.Errorf("MUNICHBRIEF_FEED_URL must be an absolute HTTPS URL without credentials")
	}
	if !strings.Contains(cfg.UserAgent, repositoryURL) {
		return Config{}, fmt.Errorf("MUNICHBRIEF_USER_AGENT must contain %s", repositoryURL)
	}
	ollamaURL, err := url.Parse(cfg.OllamaBaseURL)
	if err != nil || (ollamaURL.Scheme != "http" && ollamaURL.Scheme != "https") || ollamaURL.Host == "" || ollamaURL.User != nil || ollamaURL.RawQuery != "" || ollamaURL.Fragment != "" {
		return Config{}, fmt.Errorf("MUNICHBRIEF_OLLAMA_BASE_URL must be an absolute HTTP(S) URL without credentials, query, or fragment")
	}
	return cfg, nil
}

func canonicalOriginFromEnv(key string, publicHosts []string) (string, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		if len(publicHosts) > 0 {
			return "", fmt.Errorf("%s is required when MUNICHBRIEF_PUBLIC_HOSTS is set", key)
		}
		return "", nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Port() != "" || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("%s must be an HTTPS origin without credentials, port, path, query, or fragment", key)
	}
	host := strings.ToLower(parsed.Hostname())
	for _, publicHost := range publicHosts {
		if host == publicHost {
			return "https://" + host, nil
		}
	}
	return "", fmt.Errorf("%s host must appear in MUNICHBRIEF_PUBLIC_HOSTS", key)
}

func publicHostsFromEnv(key string) ([]string, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil, nil
	}
	hosts := make([]string, 0, strings.Count(raw, ",")+1)
	seen := make(map[string]struct{})
	for _, value := range strings.Split(raw, ",") {
		host := strings.ToLower(strings.TrimSpace(value))
		parsed, err := url.Parse("//" + host)
		if host == "" || err != nil || parsed.Hostname() != host || parsed.Port() != "" || strings.ContainsAny(host, "/@") {
			return nil, fmt.Errorf("%s must be a comma-separated list of hostnames without schemes, credentials, paths, or ports", key)
		}
		if _, exists := seen[host]; exists {
			continue
		}
		seen[host] = struct{}{}
		hosts = append(hosts, host)
	}
	return hosts, nil
}

func dailyWindowFromEnv(key, fallback string) (time.Duration, time.Duration, error) {
	raw := envOrDefault(key, fallback)
	parts := strings.Split(raw, "-")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("%s must use HH:MM-HH:MM format", key)
	}
	start, err := wallClockDuration(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("%s must use HH:MM-HH:MM format", key)
	}
	end, err := wallClockDuration(parts[1])
	if err != nil || start == end {
		return 0, 0, fmt.Errorf("%s must use distinct times in HH:MM-HH:MM format", key)
	}
	return start, end, nil
}

func wallClockDuration(value string) (time.Duration, error) {
	parsed, err := time.Parse("15:04", value)
	if err != nil {
		return 0, err
	}
	return time.Duration(parsed.Hour())*time.Hour + time.Duration(parsed.Minute())*time.Minute, nil
}

func durationFromEnv(key string, fallback, minimum time.Duration) (time.Duration, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value < minimum {
		return 0, fmt.Errorf("%s must be a duration of at least %s", key, minimum)
	}
	return value, nil
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
