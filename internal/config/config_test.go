package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	t.Setenv("MUNICHBRIEF_ADDR", "")
	t.Setenv("MUNICHBRIEF_METRICS_ADDR", "")
	t.Setenv("MUNICHBRIEF_DATABASE_PATH", "")
	t.Setenv("MUNICHBRIEF_SOURCE_MODE", "")
	t.Setenv("MUNICHBRIEF_PAGE_SIZE", "")
	t.Setenv("MUNICHBRIEF_FEED_URL", "")
	t.Setenv("MUNICHBRIEF_USER_AGENT", "")
	t.Setenv("MUNICHBRIEF_SYNC_INTERVAL", "")
	t.Setenv("MUNICHBRIEF_HTTP_TIMEOUT", "")
	t.Setenv("MUNICHBRIEF_ARTICLE_REFRESH_INTERVAL", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Address != defaultAddress {
		t.Errorf("Address = %q, want %q", cfg.Address, defaultAddress)
	}
	if cfg.MetricsAddress != defaultMetricsAddr {
		t.Errorf("MetricsAddress = %q, want %q", cfg.MetricsAddress, defaultMetricsAddr)
	}
	if cfg.DatabasePath != defaultDatabasePath {
		t.Errorf("DatabasePath = %q, want %q", cfg.DatabasePath, defaultDatabasePath)
	}
	if cfg.SourceMode != "fixture" {
		t.Errorf("SourceMode = %q, want fixture", cfg.SourceMode)
	}
	if cfg.PageSize != defaultPageSize {
		t.Errorf("PageSize = %d, want %d", cfg.PageSize, defaultPageSize)
	}
}

func TestLoadRejectsUnsupportedSourceMode(t *testing.T) {
	t.Setenv("MUNICHBRIEF_SOURCE_MODE", "unknown")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want unsupported source mode error")
	}
}

func TestLoadAcceptsLiveMode(t *testing.T) {
	t.Setenv("MUNICHBRIEF_SOURCE_MODE", "live")
	t.Setenv("MUNICHBRIEF_SYNC_INTERVAL", "2m")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.SourceMode != "live" {
		t.Fatalf("SourceMode = %q, want live", cfg.SourceMode)
	}
}

func TestLoadRejectsInsecureFeedURL(t *testing.T) {
	t.Setenv("MUNICHBRIEF_FEED_URL", "http://www.polizei.bayern.de/feed.xml")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want invalid feed URL error")
	}
}

func TestLoadRejectsUnidentifiableUserAgent(t *testing.T) {
	t.Setenv("MUNICHBRIEF_USER_AGENT", "anonymous-client")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want repository-identifying user-agent error")
	}
}
