package gazetteer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/text/unicode/norm"
)

type Observer interface {
	RecordGazetteerAttempt()
	RecordGazetteerFailure()
	RecordGazetteerSuccess(time.Time, int)
	RecordGazetteerDuration(time.Duration)
	SetNextGazetteerRefresh(time.Time)
}

type Manager struct {
	store    *Store
	fetcher  *Fetcher
	sources  []SourceDefinition
	interval time.Duration
	clock    func() time.Time
	logger   *slog.Logger
	observer Observer
	matcher  atomic.Pointer[Matcher]
	ready    atomic.Bool
}

func NewManager(ctx context.Context, store *Store, fetcher *Fetcher, sources []SourceDefinition, interval time.Duration, observer Observer, logger *slog.Logger) (*Manager, error) {
	if store == nil || fetcher == nil || len(sources) == 0 || interval < time.Hour {
		return nil, errors.New("gazetteer manager requires store, fetcher, sources, and interval of at least one hour")
	}
	if logger == nil {
		logger = slog.Default()
	}
	manager := &Manager{store: store, fetcher: fetcher, sources: append([]SourceDefinition(nil), sources...), interval: interval, clock: time.Now, observer: observer, logger: logger}
	entries, err := store.ActiveEntries(ctx)
	if err != nil {
		return nil, fmt.Errorf("load active gazetteer: %w", err)
	}
	if len(entries) > 0 {
		matcher, err := NewMatcher(entries)
		if err != nil {
			return nil, fmt.Errorf("build active gazetteer matcher: %w", err)
		}
		manager.matcher.Store(matcher)
		manager.ready.Store(true)
		if observer != nil {
			status, statusErr := store.Status(ctx)
			if statusErr != nil {
				return nil, fmt.Errorf("load active gazetteer status: %w", statusErr)
			}
			observer.RecordGazetteerSuccess(status.LastSuccess, len(entries))
			observer.SetNextGazetteerRefresh(status.NextRefresh)
		}
	}
	return manager, nil
}

func (m *Manager) Ready() bool { return m != nil && m.ready.Load() }

func (m *Manager) Protect(title, summary string) (Protected, error) {
	if m == nil || !m.ready.Load() {
		return Protected{}, errors.New("gazetteer is not ready")
	}
	return m.matcher.Load().Protect(title, summary)
}

func (m *Manager) Refresh(ctx context.Context) (RefreshResult, error) {
	started := m.clock()
	succeeded := false
	defer func() {
		if m.observer == nil {
			return
		}
		m.observer.RecordGazetteerDuration(m.clock().Sub(started))
		if !succeeded {
			m.observer.RecordGazetteerFailure()
		}
	}()
	next := started.Add(m.interval)
	if m.observer != nil {
		m.observer.RecordGazetteerAttempt()
		m.observer.SetNextGazetteerRefresh(next)
	}
	_ = m.store.RecordAttempt(ctx, started, next)
	var snapshots []SourceSnapshot
	allNotModified := true
	for _, source := range m.sources {
		snapshot, notModified, err := m.fetcher.Fetch(ctx, source)
		if err != nil {
			_ = m.store.RecordSourceFailure(ctx, source, m.clock())
			return RefreshResult{}, err
		}
		allNotModified = allNotModified && notModified
		snapshots = append(snapshots, snapshot)
	}
	overrides, err := m.store.Overrides(ctx)
	if err != nil {
		return RefreshResult{}, err
	}
	entries := mergeEntries(snapshots, overrides)
	matcher, err := NewMatcher(entries)
	if err != nil {
		return RefreshResult{}, err
	}
	aggregateHash := aggregateSourceHash(snapshots, overrides)
	generationID, changed, err := m.store.Activate(ctx, snapshots, entries, aggregateHash, m.clock(), next)
	if err != nil {
		return RefreshResult{}, err
	}
	if changed || !m.ready.Load() {
		m.matcher.Store(matcher)
		m.ready.Store(true)
	}
	finished := m.clock()
	if m.observer != nil {
		m.observer.RecordGazetteerSuccess(finished, len(entries))
	}
	succeeded = true
	m.logger.Info("gazetteer refresh completed", "generation_id", generationID, "entries", len(entries), "changed", changed, "all_not_modified", allNotModified, "duration", finished.Sub(started).Round(time.Millisecond))
	return RefreshResult{GenerationID: generationID, EntryCount: len(entries), NotModified: !changed, Duration: finished.Sub(started)}, nil
}

func (m *Manager) Run(ctx context.Context) {
	delay := time.Duration(0)
	failures := 0
	for {
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
		_, err := m.Refresh(ctx)
		if err != nil {
			m.logger.Error("gazetteer refresh failed; keeping last successful generation", "error", err)
			failures++
			delay = 5 * time.Minute * time.Duration(1<<min(failures-1, 6))
			if delay > 6*time.Hour {
				delay = 6 * time.Hour
			}
			next := m.clock().Add(delay)
			_ = m.store.SetNextRefresh(ctx, next)
			if m.observer != nil {
				m.observer.SetNextGazetteerRefresh(next)
			}
			continue
		}
		failures = 0
		jitter := time.Duration(rand.Int64N(int64(m.interval/10)*2+1)) - m.interval/10
		delay = m.interval + jitter
		next := m.clock().Add(delay)
		_ = m.store.SetNextRefresh(ctx, next)
		if m.observer != nil {
			m.observer.SetNextGazetteerRefresh(next)
		}
	}
}

func mergeEntries(snapshots []SourceSnapshot, overrides map[string]string) []Entry {
	merged := make(map[string]Entry)
	for _, snapshot := range snapshots {
		for _, entry := range snapshot.Entries {
			entry.Name = norm.NFC.String(strings.TrimSpace(entry.Name))
			if !validName(entry.Name) || overrides[entry.Name] == "exclude" {
				continue
			}
			if letterCount(entry.Name) < 3 {
				entry.RequiresContext = true
			}
			if overrides[entry.Name] == "context" {
				entry.RequiresContext = true
			}
			current, found := merged[entry.Name]
			if !found {
				merged[entry.Name] = entry
				continue
			}
			current.Sources = append(current.Sources, entry.Sources...)
			if entry.Priority < current.Priority {
				current.Kind, current.Priority = entry.Kind, entry.Priority
			}
			current.RequiresContext = current.RequiresContext || entry.RequiresContext
			merged[entry.Name] = current
		}
	}
	entries := make([]Entry, 0, len(merged))
	for _, entry := range merged {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries
}

func aggregateSourceHash(snapshots []SourceSnapshot, overrides map[string]string) string {
	parts := make([]string, 0, len(snapshots)+len(overrides))
	for _, snapshot := range snapshots {
		parts = append(parts, snapshot.Definition.Key+":"+snapshot.ContentHash)
	}
	for name, action := range overrides {
		parts = append(parts, "override:"+name+":"+action)
	}
	sort.Strings(parts)
	hash := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(hash[:])
}
