package processing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	modelCatalogRefreshInterval = 30 * time.Second
	modelCatalogMaxBytes        = 1 << 20
)

var ErrModelUnavailable = errors.New("ollama model is unavailable")

// ModelCatalogSnapshot is a point-in-time, sorted view of available model names.
// An unsuccessful refresh fails closed by returning no models and a non-nil Err.
type ModelCatalogSnapshot struct {
	Digests   map[string]string
	Models    []string
	CheckedAt time.Time
	Err       error
}

func (s ModelCatalogSnapshot) Has(model string) bool {
	index := sort.SearchStrings(s.Models, model)
	return index < len(s.Models) && s.Models[index] == model
}

func (s ModelCatalogSnapshot) Available() bool { return s.Err == nil && !s.CheckedAt.IsZero() }

// ModelCatalog supplies the latest immutable availability snapshot.
type ModelCatalog interface {
	Snapshot() ModelCatalogSnapshot
}

// OllamaModelCatalog periodically reads the bounded Ollama tags endpoint.
type OllamaModelCatalog struct {
	endpoint string
	client   *http.Client
	clock    func() time.Time
	mu       sync.RWMutex
	snapshot ModelCatalogSnapshot
}

// NewOllamaModelCatalog validates baseURL and builds a catalog client with a
// hard request timeout.
func NewOllamaModelCatalog(baseURL string, timeout time.Duration, baseClient *http.Client) (*OllamaModelCatalog, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("ollama base URL must be an absolute HTTP(S) URL without credentials, query, or fragment")
	}
	if timeout <= 0 {
		return nil, errors.New("ollama model catalog timeout must be positive")
	}
	client := &http.Client{Timeout: timeout}
	if baseClient != nil {
		clone := *baseClient
		client = &clone
		client.Timeout = timeout
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/api/tags"
	return &OllamaModelCatalog{endpoint: parsed.String(), client: client, clock: time.Now}, nil
}

func (c *OllamaModelCatalog) Snapshot() ModelCatalogSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := c.snapshot
	result.Models = append([]string(nil), result.Models...)
	result.Digests = maps.Clone(result.Digests)
	return result
}

// Refresh replaces the complete snapshot, including failures, so stale model
// availability can never authorize new processing work.
func (c *OllamaModelCatalog) Refresh(ctx context.Context) ModelCatalogSnapshot {
	snapshot := ModelCatalogSnapshot{CheckedAt: c.clock(), Digests: make(map[string]string)}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint, nil)
	if err == nil {
		request.Header.Set("Accept", "application/json")
		var response *http.Response
		response, err = c.client.Do(request)
		if err == nil {
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				err = fmt.Errorf("ollama model catalog returned HTTP %d", response.StatusCode)
			} else {
				var body []byte
				body, err = readBounded(response.Body, modelCatalogMaxBytes)
				if err == nil {
					var payload struct {
						Models []struct {
							Name   string `json:"name"`
							Digest string `json:"digest"`
						} `json:"models"`
					}
					if err = json.Unmarshal(body, &payload); err == nil {
						seen := make(map[string]struct{}, len(payload.Models))
						for _, item := range payload.Models {
							name := strings.TrimSpace(item.Name)
							if name != "" {
								if _, duplicate := seen[name]; duplicate && snapshot.Digests[name] != item.Digest {
									snapshot.Digests[name] = ""
								} else if !duplicate {
									snapshot.Digests[name] = item.Digest
								}
								seen[name] = struct{}{}
							}
						}
						for name := range seen {
							snapshot.Models = append(snapshot.Models, name)
						}
						sort.Strings(snapshot.Models)
					}
				}
			}
		}
	}
	if err != nil {
		snapshot.Err = fmt.Errorf("list Ollama models: %w", err)
		snapshot.Models = nil
		snapshot.Digests = nil
	}
	c.mu.Lock()
	c.snapshot = snapshot
	c.mu.Unlock()
	return c.Snapshot()
}

func (c *OllamaModelCatalog) Run(ctx context.Context) {
	ticker := time.NewTicker(modelCatalogRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.Refresh(ctx)
		}
	}
}
