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
	t.Setenv("MUNICHBRIEF_AI_IMMEDIATE", "")
	t.Setenv("MUNICHBRIEF_AI_WINDOW", "")
	t.Setenv("MUNICHBRIEF_PRESENTATION_MODE", "")
	t.Setenv("MUNICHBRIEF_SECURE_COOKIES", "")
	t.Setenv("MUNICHBRIEF_ADMIN_ENABLED", "")
	t.Setenv("MUNICHBRIEF_PUBLIC_HOST", "")

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
	if cfg.AIImmediate || cfg.AIWindowStart != 3*time.Hour || cfg.AIWindowEnd != 8*time.Hour {
		t.Errorf("AI schedule defaults = immediate:%t window:%s-%s", cfg.AIImmediate, cfg.AIWindowStart, cfg.AIWindowEnd)
	}
	if cfg.PresentationMode != "review" || cfg.SecureCookies {
		t.Errorf("presentation defaults = %q/secure:%t", cfg.PresentationMode, cfg.SecureCookies)
	}
	if cfg.AdminEnabled || cfg.PublicHost != "" {
		t.Errorf("admin defaults = enabled:%t public-host:%q", cfg.AdminEnabled, cfg.PublicHost)
	}
}

func TestLoadAcceptsRemoteOllamaConfiguration(t *testing.T) {
	t.Setenv("MUNICHBRIEF_AI_ENABLED", "true")
	t.Setenv("MUNICHBRIEF_OLLAMA_BASE_URL", "http://192.168.178.102:11434")
	t.Setenv("MUNICHBRIEF_OLLAMA_MODEL", "qwen3.5:4b")
	t.Setenv("MUNICHBRIEF_AI_CONTEXT_SIZE", "8192")
	t.Setenv("MUNICHBRIEF_AI_IMMEDIATE", "true")
	t.Setenv("MUNICHBRIEF_AI_WINDOW", "22:30-06:15")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.AIEnabled || !cfg.AIImmediate || cfg.OllamaBaseURL != "http://192.168.178.102:11434" {
		t.Fatalf("AI configuration = %#v", cfg)
	}
	if cfg.AIWindowStart != 22*time.Hour+30*time.Minute || cfg.AIWindowEnd != 6*time.Hour+15*time.Minute {
		t.Fatalf("AI window = %s-%s", cfg.AIWindowStart, cfg.AIWindowEnd)
	}
}

func TestLoadRejectsInvalidAISchedule(t *testing.T) {
	for _, test := range []struct {
		name      string
		immediate string
		window    string
	}{
		{name: "immediate", immediate: "sometimes", window: defaultAIWindow},
		{name: "format", window: "3am-8am"},
		{name: "same time", window: "03:00-03:00"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("MUNICHBRIEF_AI_IMMEDIATE", test.immediate)
			t.Setenv("MUNICHBRIEF_AI_WINDOW", test.window)
			if _, err := Load(); err == nil {
				t.Fatal("Load() error = nil, want invalid AI schedule error")
			}
		})
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

func TestLoadAcceptsAdminAndPublicHost(t *testing.T) {
	t.Setenv("MUNICHBRIEF_ADMIN_ENABLED", "true")
	t.Setenv("MUNICHBRIEF_PUBLIC_HOST", "MUNICHBRIEF.EGEKOCABAS.COM")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AdminEnabled || cfg.PublicHost != "munichbrief.egekocabas.com" {
		t.Fatalf("admin config = enabled:%t public-host:%q", cfg.AdminEnabled, cfg.PublicHost)
	}
}

func TestLoadRejectsInvalidAdminConfiguration(t *testing.T) {
	for _, test := range []struct {
		name, enabled, host string
	}{
		{name: "enabled", enabled: "sometimes"},
		{name: "scheme", host: "https://munichbrief.egekocabas.com"},
		{name: "port", host: "munichbrief.egekocabas.com:443"},
		{name: "path", host: "munichbrief.egekocabas.com/admin"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("MUNICHBRIEF_ADMIN_ENABLED", test.enabled)
			t.Setenv("MUNICHBRIEF_PUBLIC_HOST", test.host)
			if _, err := Load(); err == nil {
				t.Fatal("Load() error = nil, want invalid admin configuration error")
			}
		})
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
