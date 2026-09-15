package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("MUNICHBRIEF_ADDR", "")
	t.Setenv("MUNICHBRIEF_METRICS_ADDR", "")
	t.Setenv("MUNICHBRIEF_DATABASE_PATH", "")
	t.Setenv("MUNICHBRIEF_GAZETTEER_DATABASE_PATH", "")
	t.Setenv("MUNICHBRIEF_GAZETTEER_ENABLED", "")
	t.Setenv("MUNICHBRIEF_GAZETTEER_REFRESH_INTERVAL", "")
	t.Setenv("MUNICHBRIEF_GAZETTEER_HTTP_TIMEOUT", "")
	t.Setenv("MUNICHBRIEF_SOURCE_MODE", "")
	t.Setenv("MUNICHBRIEF_PAGE_SIZE", "")
	t.Setenv("MUNICHBRIEF_FEED_URL", "")
	t.Setenv("MUNICHBRIEF_USER_AGENT", "")
	t.Setenv("MUNICHBRIEF_HTTP_TIMEOUT", "")
	t.Setenv("MUNICHBRIEF_ARTICLE_REFRESH_INTERVAL", "")
	t.Setenv("MUNICHBRIEF_AI_ENABLED", "")
	t.Setenv("MUNICHBRIEF_OLLAMA_BASE_URL", "")
	t.Setenv("MUNICHBRIEF_AI_INTERVAL", "")
	t.Setenv("MUNICHBRIEF_AI_TIMEOUT", "")
	t.Setenv("MUNICHBRIEF_AI_CONTEXT_SIZE", "")
	t.Setenv("MUNICHBRIEF_AI_IMMEDIATE", "")
	t.Setenv("MUNICHBRIEF_AI_WINDOW", "")
	t.Setenv("MUNICHBRIEF_PRESENTATION_MODE", "")
	t.Setenv("MUNICHBRIEF_SECURE_COOKIES", "")
	t.Setenv("MUNICHBRIEF_ADMIN_ENABLED", "")
	t.Setenv("MUNICHBRIEF_PUBLIC_HOSTS", "")
	t.Setenv("MUNICHBRIEF_CANONICAL_ORIGIN", "")

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
	if cfg.GazetteerEnabled || cfg.GazetteerDatabasePath != defaultGazetteerDatabasePath || cfg.GazetteerRefreshInterval != 7*24*time.Hour || cfg.GazetteerHTTPTimeout != 2*time.Minute {
		t.Errorf("gazetteer defaults = enabled:%t path:%q interval:%s timeout:%s", cfg.GazetteerEnabled, cfg.GazetteerDatabasePath, cfg.GazetteerRefreshInterval, cfg.GazetteerHTTPTimeout)
	}
	if cfg.SourceMode != "fixture" {
		t.Errorf("SourceMode = %q, want fixture", cfg.SourceMode)
	}
	if cfg.PageSize != defaultPageSize {
		t.Errorf("PageSize = %d, want %d", cfg.PageSize, defaultPageSize)
	}
	if cfg.AIEnabled || cfg.AIContextSize != defaultAIContext {
		t.Errorf("AI defaults = enabled:%t context:%d", cfg.AIEnabled, cfg.AIContextSize)
	}
	if cfg.AIInterval != 15*time.Second {
		t.Errorf("AIInterval = %s, want 15s", cfg.AIInterval)
	}
	if cfg.AITimeout != 15*time.Minute {
		t.Errorf("AITimeout = %s, want 15m", cfg.AITimeout)
	}
	if cfg.AIImmediate || cfg.AIWindowStart != 3*time.Hour || cfg.AIWindowEnd != 8*time.Hour {
		t.Errorf("AI schedule defaults = immediate:%t window:%s-%s", cfg.AIImmediate, cfg.AIWindowStart, cfg.AIWindowEnd)
	}
	if cfg.PresentationMode != "review" || cfg.SecureCookies {
		t.Errorf("presentation defaults = %q/secure:%t", cfg.PresentationMode, cfg.SecureCookies)
	}
	if cfg.AdminEnabled || len(cfg.PublicHosts) != 0 {
		t.Errorf("admin defaults = enabled:%t public-hosts:%q", cfg.AdminEnabled, cfg.PublicHosts)
	}
}

func TestLoadAcceptsRemoteOllamaConfiguration(t *testing.T) {
	t.Setenv("MUNICHBRIEF_AI_ENABLED", "true")
	t.Setenv("MUNICHBRIEF_OLLAMA_BASE_URL", "http://192.0.2.10:11434")
	t.Setenv("MUNICHBRIEF_AI_CONTEXT_SIZE", "8192")
	t.Setenv("MUNICHBRIEF_AI_IMMEDIATE", "true")
	t.Setenv("MUNICHBRIEF_AI_WINDOW", "22:30-06:15")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.AIEnabled || !cfg.AIImmediate || cfg.OllamaBaseURL != "http://192.0.2.10:11434" {
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
	for _, baseURL := range []string{"192.0.2.10:11434", "ftp://ollama.internal.example/model", "http://user:password@ollama.internal.example:11434"} {
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
	if !cfg.GazetteerEnabled {
		t.Fatal("live mode did not enable the gazetteer")
	}
}

func TestLoadAcceptsGazetteerOverrides(t *testing.T) {
	t.Setenv("MUNICHBRIEF_GAZETTEER_ENABLED", "true")
	t.Setenv("MUNICHBRIEF_GAZETTEER_DATABASE_PATH", "/tmp/test-gazetteer.db")
	t.Setenv("MUNICHBRIEF_GAZETTEER_REFRESH_INTERVAL", "48h")
	t.Setenv("MUNICHBRIEF_GAZETTEER_HTTP_TIMEOUT", "3m")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.GazetteerEnabled || cfg.GazetteerDatabasePath != "/tmp/test-gazetteer.db" || cfg.GazetteerRefreshInterval != 48*time.Hour || cfg.GazetteerHTTPTimeout != 3*time.Minute {
		t.Fatalf("gazetteer config = %#v", cfg)
	}
}

func TestLoadRejectsSharedGazetteerDatabasePath(t *testing.T) {
	t.Setenv("MUNICHBRIEF_DATABASE_PATH", ".data/shared.db")
	t.Setenv("MUNICHBRIEF_GAZETTEER_DATABASE_PATH", ".data/./shared.db")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted one file for incident and gazetteer databases")
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

func TestLoadAcceptsAdminAndPublicHosts(t *testing.T) {
	t.Setenv("MUNICHBRIEF_ADMIN_ENABLED", "true")
	t.Setenv("MUNICHBRIEF_PUBLIC_HOSTS", " BRIEF.EXAMPLE.COM,munichbrief.de,munichbrief.de ")
	t.Setenv("MUNICHBRIEF_CANONICAL_ORIGIN", "https://MUNICHBRIEF.DE/")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	wantHosts := []string{"brief.example.com", "munichbrief.de"}
	if !cfg.AdminEnabled || len(cfg.PublicHosts) != len(wantHosts) || cfg.PublicHosts[0] != wantHosts[0] || cfg.PublicHosts[1] != wantHosts[1] {
		t.Fatalf("admin config = enabled:%t public-hosts:%q", cfg.AdminEnabled, cfg.PublicHosts)
	}
	if cfg.CanonicalOrigin != "https://munichbrief.de" {
		t.Fatalf("canonical origin = %q", cfg.CanonicalOrigin)
	}
}

func TestLoadRejectsInvalidCanonicalOrigin(t *testing.T) {
	for _, test := range []struct {
		name, publicHosts, origin string
	}{
		{name: "missing", publicHosts: "munichbrief.de"},
		{name: "insecure", publicHosts: "munichbrief.de", origin: "http://munichbrief.de"},
		{name: "unknown host", publicHosts: "munichbrief.de", origin: "https://example.com"},
		{name: "path", publicHosts: "munichbrief.de", origin: "https://munichbrief.de/de"},
		{name: "port", publicHosts: "munichbrief.de", origin: "https://munichbrief.de:443"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("MUNICHBRIEF_PUBLIC_HOSTS", test.publicHosts)
			t.Setenv("MUNICHBRIEF_CANONICAL_ORIGIN", test.origin)
			if _, err := Load(); err == nil {
				t.Fatal("Load() error = nil, want invalid canonical origin error")
			}
		})
	}
}

func TestLoadRejectsInvalidAdminConfiguration(t *testing.T) {
	for _, test := range []struct {
		name, enabled, host string
	}{
		{name: "enabled", enabled: "sometimes"},
		{name: "scheme", host: "https://brief.example.com"},
		{name: "port", host: "brief.example.com:443"},
		{name: "path", host: "brief.example.com/admin"},
		{name: "empty entry", host: "brief.example.com,"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("MUNICHBRIEF_ADMIN_ENABLED", test.enabled)
			t.Setenv("MUNICHBRIEF_PUBLIC_HOSTS", test.host)
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

func TestContactConfiguration(t *testing.T) {
	t.Setenv("MUNICHBRIEF_CONTACT_ENABLED", "true")
	t.Setenv("MUNICHBRIEF_ADMIN_ENABLED", "true")
	t.Setenv("MUNICHBRIEF_CONTACT_SECRET", "01234567890123456789012345678901")
	t.Setenv("MUNICHBRIEF_CONTACT_TRUSTED_PROXIES", "127.0.0.1/32,::1/128")
	cfg, err := Load()
	if err != nil || !cfg.ContactEnabled || len(cfg.ContactTrustedProxies) != 2 || cfg.ContactDailyLimit != 20 || cfg.ContactMonthlyLimit != 300 {
		t.Fatalf("contact defaults or proxies: %v", err)
	}
	for _, tc := range []struct{ name, value string }{
		{"MUNICHBRIEF_CONTACT_SECRET", "short"}, {"MUNICHBRIEF_ADMIN_ENABLED", "false"},
		{"MUNICHBRIEF_CONTACT_DAILY_LIMIT", "0"}, {"MUNICHBRIEF_CONTACT_MONTHLY_LIMIT", "x"},
		{"MUNICHBRIEF_CONTACT_TRUSTED_PROXIES", "trust-everything"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.name, tc.value)
			if _, err := Load(); err == nil {
				t.Fatal("accepted invalid contact configuration")
			}
		})
	}
}
