package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultAddress      = "127.0.0.1:8080"
	defaultMetricsAddr  = "127.0.0.1:9090"
	defaultDatabasePath = ".data/munichbrief.db"
	defaultSourceMode   = "fixture"
	defaultPageSize     = 20
	defaultFeedURL      = "https://www.polizei.bayern.de/rss/polizeiprasidium-munchen.xml"
	defaultUserAgent    = "MunichBrief/dev (+https://github.com/egekocabas/munichbrief)"
	defaultSyncInterval = 15 * time.Minute
	defaultHTTPTimeout  = 10 * time.Second
	defaultRefreshAfter = 6 * time.Hour
	repositoryURL       = "https://github.com/egekocabas/munichbrief"
)

// Config contains the runtime settings for the walking skeleton.
type Config struct {
	Address        string
	MetricsAddress string
	DatabasePath   string
	SourceMode     string
	PageSize       int
	FeedURL        string
	UserAgent      string
	SyncInterval   time.Duration
	HTTPTimeout    time.Duration
	RefreshAfter   time.Duration
}

// Load reads configuration from the environment and applies local-safe defaults.
func Load() (Config, error) {
	cfg := Config{
		Address:        envOrDefault("MUNICHBRIEF_ADDR", defaultAddress),
		MetricsAddress: envOrDefault("MUNICHBRIEF_METRICS_ADDR", defaultMetricsAddr),
		DatabasePath:   envOrDefault("MUNICHBRIEF_DATABASE_PATH", defaultDatabasePath),
		SourceMode:     envOrDefault("MUNICHBRIEF_SOURCE_MODE", defaultSourceMode),
		PageSize:       defaultPageSize,
		FeedURL:        envOrDefault("MUNICHBRIEF_FEED_URL", defaultFeedURL),
		UserAgent:      envOrDefault("MUNICHBRIEF_USER_AGENT", defaultUserAgent),
		SyncInterval:   defaultSyncInterval,
		HTTPTimeout:    defaultHTTPTimeout,
		RefreshAfter:   defaultRefreshAfter,
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

	var err error
	if cfg.SyncInterval, err = durationFromEnv("MUNICHBRIEF_SYNC_INTERVAL", cfg.SyncInterval, time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.HTTPTimeout, err = durationFromEnv("MUNICHBRIEF_HTTP_TIMEOUT", cfg.HTTPTimeout, time.Second); err != nil {
		return Config{}, err
	}
	if cfg.RefreshAfter, err = durationFromEnv("MUNICHBRIEF_ARTICLE_REFRESH_INTERVAL", cfg.RefreshAfter, 15*time.Minute); err != nil {
		return Config{}, err
	}

	feedURL, err := url.Parse(cfg.FeedURL)
	if err != nil || feedURL.Scheme != "https" || feedURL.Host == "" || feedURL.User != nil {
		return Config{}, fmt.Errorf("MUNICHBRIEF_FEED_URL must be an absolute HTTPS URL without credentials")
	}
	if !strings.Contains(cfg.UserAgent, repositoryURL) {
		return Config{}, fmt.Errorf("MUNICHBRIEF_USER_AGENT must contain %s", repositoryURL)
	}

	return cfg, nil
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
