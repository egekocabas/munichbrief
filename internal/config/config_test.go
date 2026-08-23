package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("MUNICHBRIEF_ADDR", "")
	t.Setenv("MUNICHBRIEF_METRICS_ADDR", "")
	t.Setenv("MUNICHBRIEF_DATABASE_PATH", "")
	t.Setenv("MUNICHBRIEF_SOURCE_MODE", "")
	t.Setenv("MUNICHBRIEF_PAGE_SIZE", "")
	t.Setenv("MUNICHBRIEF_FEED_URL", "")
	t.Setenv("MUNICHBRIEF_USER_AGENT", "")
	t.Setenv("MUNICHBRIEF_HTTP_TIMEOUT", "")
	t.Setenv("MUNICHBRIEF_ARTICLE_REFRESH_INTERVAL", "")
	t.Setenv("MUNICHBRIEF_AI_ENABLED", "")
	t.Setenv("MUNICHBRIEF_OLLAMA_BASE_URL", "")
	t.Setenv("MUNICHBRIEF_OLLAMA_MODEL", "")
	t.Setenv("MUNICHBRIEF_AI_INTERVAL", "")
	t.Setenv("MUNICHBRIEF_AI_TIMEOUT", "")
	t.Setenv("MUNICHBRIEF_AI_CONTEXT_SIZE", "")
	t.Setenv("MUNICHBRIEF_PRESENTATION_MODE", "")
	t.Setenv("MUNICHBRIEF_SECURE_COOKIES", "")

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
	if cfg.AIEnabled || cfg.OllamaModel != defaultOllamaModel || cfg.AIContextSize != defaultAIContext {
		t.Errorf("AI defaults = enabled:%t model:%q context:%d", cfg.AIEnabled, cfg.OllamaModel, cfg.AIContextSize)
	}
	if cfg.AITimeout != 10*time.Minute {
		t.Errorf("AITimeout = %s, want 10m", cfg.AITimeout)
	}
	if cfg.PresentationMode != "review" || cfg.SecureCookies {
		t.Errorf("presentation defaults = %q/secure:%t", cfg.PresentationMode, cfg.SecureCookies)
	}
}

func TestLoadAcceptsRemoteOllamaConfiguration(t *testing.T) {
	t.Setenv("MUNICHBRIEF_AI_ENABLED", "true")
	t.Setenv("MUNICHBRIEF_OLLAMA_BASE_URL", "http://192.168.178.102:11434")
	t.Setenv("MUNICHBRIEF_OLLAMA_MODEL", "qwen3.5:4b")
	t.Setenv("MUNICHBRIEF_AI_CONTEXT_SIZE", "8192")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.AIEnabled || cfg.OllamaBaseURL != "http://192.168.178.102:11434" {
		t.Fatalf("AI configuration = %#v", cfg)
	}
}

func TestLoadRejectsInvalidOllamaConfiguration(t *testing.T) {
	for _, baseURL := range []string{"192.168.178.102:11434", "ftp://pi8/model", "http://user:password@pi8:11434"} {
		t.Run(baseURL, func(t *testing.T) {
			t.Setenv("MUNICHBRIEF_OLLAMA_BASE_URL", baseURL)
			if _, err := Load(); err == nil {
				t.Fatal("Load() error = nil, want invalid Ollama URL error")
			}
		})
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
	t.Setenv("MUNICHBRIEF_PRESENTATION_MODE", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.SourceMode != "live" {
		t.Fatalf("SourceMode = %q, want live", cfg.SourceMode)
	}
	if cfg.PresentationMode != "public" {
		t.Fatalf("live presentation mode = %q, want public", cfg.PresentationMode)
	}
}

func TestLoadAcceptsExplicitReviewAndSecureCookies(t *testing.T) {
	t.Setenv("MUNICHBRIEF_SOURCE_MODE", "live")
	t.Setenv("MUNICHBRIEF_PRESENTATION_MODE", "review")
	t.Setenv("MUNICHBRIEF_SECURE_COOKIES", "true")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PresentationMode != "review" || !cfg.SecureCookies {
		t.Fatalf("presentation config = %q/secure:%t", cfg.PresentationMode, cfg.SecureCookies)
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
