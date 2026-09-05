package ingest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/egekocabas/munichbrief/internal/domain"
	"github.com/egekocabas/munichbrief/internal/parser"
	"github.com/egekocabas/munichbrief/internal/source"
	"github.com/egekocabas/munichbrief/internal/store"
)

// Repository captures the atomic persistence operations needed by a source sync.
type Repository interface {
	GetSyncState(context.Context) (store.SyncState, error)
	RecordSyncAttempt(context.Context, time.Time, time.Time, time.Time) (int64, error)
	RecordSyncSuccess(context.Context, int64, string, string, time.Time, store.SyncRunResult) error
	RecordSyncFailure(context.Context, int64, time.Time, store.SyncRunResult, error) error
	UpsertSourceMetadata(context.Context, []domain.SourceDocument, time.Time) error
	ListDocumentsForFetch(context.Context, time.Time, time.Time, time.Time, bool) ([]store.SourceDocumentRecord, error)
	ReplaceDocumentIncidents(context.Context, int64, string, []domain.Incident, time.Time) error
	MarkDocumentFetchFailed(context.Context, int64, time.Time, error) error
}

// Syncer serializes conditional feed refreshes and article parsing.
type Syncer struct {
	mu           sync.Mutex
	repository   Repository
	client       source.LiveClient
	clock        func() time.Time
	location     *time.Location
	refreshAfter time.Duration
	logger       *slog.Logger
}

// Result summarizes one synchronization attempt for logs and metrics.
type Result struct {
	NotModified     bool
	FeedDocuments   int
	Discovered      int
	Fetched         int
	ArticleFailures int
	FetchFailures   int
	ParserFailures  int
	Skipped         int
	WindowStart     time.Time
	WindowEnd       time.Time
}

// NewSyncer constructs a live-source synchronizer. A nil clock uses time.Now.
func NewSyncer(repository Repository, client source.LiveClient, refreshAfter time.Duration, clock func() time.Time, logger *slog.Logger) (*Syncer, error) {
	if repository == nil || client == nil || logger == nil {
		return nil, errors.New("repository, live source client, and logger are required")
	}
	if refreshAfter <= 0 {
		return nil, errors.New("article refresh interval must be positive")
	}
	if clock == nil {
		clock = time.Now
	}
	location, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		return nil, fmt.Errorf("load Europe/Berlin timezone: %w", err)
	}
	return &Syncer{
		repository:   repository,
		client:       client,
		clock:        clock,
		location:     location,
		refreshAfter: refreshAfter,
		logger:       logger,
	}, nil
}

// Sync fetches the rolling seven-day source window and persists each document
// independently, allowing one malformed or unavailable article to be retried.
func (s *Syncer) Sync(ctx context.Context) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.clock()
	syncStartedAt := time.Now()
	start, end := s.sevenDayWindow(now)
	result := Result{WindowStart: start, WindowEnd: end}

	attemptID, err := s.repository.RecordSyncAttempt(ctx, now, start, end)
	if err != nil {
		return result, err
	}
	logger := s.logger.With("rss_attempt_id", attemptID)
	logger.Info("RSS synchronization started", "window_start", start, "window_end", end)
	state, err := s.repository.GetSyncState(ctx)
	if err != nil {
		return result, s.fail(ctx, attemptID, now, syncStartedAt, result, err)
	}

	feedStartedAt := time.Now()
	logger.Info("RSS feed request started", "conditional", state.ETag != "" || state.LastModified != "")
	feed, err := s.client.FetchFeed(ctx, state.ETag, state.LastModified)
	if err != nil {
		logger.Warn("RSS feed request failed", durationAttributes(feedStartedAt, time.Now())...)
		return result, s.fail(ctx, attemptID, now, syncStartedAt, result, err)
	}
	feedAttributes := []any{
		"not_modified", feed.NotModified,
		"documents", len(feed.Documents),
		"skipped", feed.Skipped,
	}
	feedAttributes = append(feedAttributes, durationAttributes(feedStartedAt, time.Now())...)
	logger.Info("RSS feed response received", feedAttributes...)
	result.NotModified = feed.NotModified
	result.FeedDocuments = len(feed.Documents)
	result.Skipped = feed.Skipped

	if !feed.NotModified {
		inWindow := make([]domain.SourceDocument, 0, len(feed.Documents))
		for _, document := range feed.Documents {
			if !document.PublishedAt.Before(start) && document.PublishedAt.Before(end) {
				inWindow = append(inWindow, document)
			}
		}
		result.Discovered = len(inWindow)
		if err := s.repository.UpsertSourceMetadata(ctx, inWindow, now); err != nil {
			return result, s.fail(ctx, attemptID, now, syncStartedAt, result, err)
		}
	}

	forceRefresh := feedChanged(state, feed)
	documents, err := s.repository.ListDocumentsForFetch(ctx, start, end, now.Add(-s.refreshAfter), forceRefresh)
	if err != nil {
		return result, s.fail(ctx, attemptID, now, syncStartedAt, result, err)
	}
	for _, document := range documents {
		articleStartedAt := time.Now()
		logger.Info("press release request started",
			"document_id", document.ID,
			"external_id", document.ExternalID,
			"source_url", document.SourceURL,
			"published_at", document.PublishedAt,
		)
		contents, fetchErr := s.client.FetchArticle(ctx, document.SourceURL)
		if fetchErr != nil {
			attributes := []any{"document_id", document.ID, "external_id", document.ExternalID, "failure_kind", "fetch"}
			attributes = append(attributes, durationAttributes(articleStartedAt, time.Now())...)
			logger.Warn("press release request failed", attributes...)
			result.FetchFailures++
			result.ArticleFailures++
			if markErr := s.repository.MarkDocumentFetchFailed(ctx, document.ID, now, fetchErr); markErr != nil {
				return result, s.fail(ctx, attemptID, now, syncStartedAt, result, errors.Join(fetchErr, markErr))
			}
			continue
		}
		responseAt := time.Now()
		responseAttributes := []any{
			"document_id", document.ID,
			"external_id", document.ExternalID,
			"response_bytes", len(contents),
		}
		responseAttributes = append(responseAttributes, durationAttributes(articleStartedAt, responseAt)...)
		logger.Info("press release response received", responseAttributes...)
		parsed, err := parser.ParsePoliceRelease(contents)
		if err != nil {
			attributes := []any{"document_id", document.ID, "external_id", document.ExternalID, "failure_kind", "parse"}
			attributes = append(attributes, durationAttributes(articleStartedAt, time.Now())...)
			logger.Warn("press release processing failed", attributes...)
			result.ParserFailures++
			result.ArticleFailures++
			if markErr := s.repository.MarkDocumentFetchFailed(ctx, document.ID, now, err); markErr != nil {
				return result, s.fail(ctx, attemptID, now, syncStartedAt, result, errors.Join(err, markErr))
			}
			continue
		}
		if err := s.repository.ReplaceDocumentIncidents(ctx, document.ID, parsed.SourceHash, parsed.Incidents, now); err != nil {
			return result, s.fail(ctx, attemptID, now, syncStartedAt, result, err)
		}
		attributes := []any{
			"document_id", document.ID,
			"external_id", document.ExternalID,
			"incidents", len(parsed.Incidents),
		}
		attributes = append(attributes, durationAttributes(articleStartedAt, time.Now())...)
		logger.Info("press release processed and stored", attributes...)
		result.Fetched++
	}

	etag := feed.ETag
	if etag == "" {
		etag = state.ETag
	}
	lastModified := feed.LastModified
	if lastModified == "" {
		lastModified = state.LastModified
	}
	elapsed := time.Since(syncStartedAt)
	completedAt := now.Add(elapsed)
	outcome := syncRunResult(result, elapsed)
	if err := s.repository.RecordSyncSuccess(ctx, attemptID, etag, lastModified, completedAt, outcome); err != nil {
		return result, err
	}
	syncAttributes := []any{
		"not_modified", result.NotModified,
		"discovered", result.Discovered,
		"fetched", result.Fetched,
		"fetch_failures", result.FetchFailures,
		"parser_failures", result.ParserFailures,
		"skipped", result.Skipped,
	}
	syncAttributes = append(syncAttributes, durationAttributes(syncStartedAt, time.Now())...)
	logger.Info("RSS synchronization completed", syncAttributes...)
	return result, nil
}

func durationAttributes(startedAt, completedAt time.Time) []any {
	duration := completedAt.Sub(startedAt)
	return []any{
		"duration", duration.Round(time.Millisecond).String(),
		"duration_seconds", duration.Seconds(),
	}
}

func (s *Syncer) sevenDayWindow(value time.Time) (time.Time, time.Time) {
	local := value.In(s.location)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, s.location)
	start := today.AddDate(0, 0, -6)
	end := today.AddDate(0, 0, 1)
	return start, end
}

func (s *Syncer) fail(ctx context.Context, attemptID int64, attemptedAt, startedAt time.Time, result Result, syncError error) error {
	elapsed := time.Since(startedAt)
	failedAt := attemptedAt.Add(elapsed)
	if recordError := s.repository.RecordSyncFailure(ctx, attemptID, failedAt, syncRunResult(result, elapsed), syncError); recordError != nil {
		return errors.Join(syncError, recordError)
	}
	return syncError
}

func syncRunResult(result Result, elapsed time.Duration) store.SyncRunResult {
	return store.SyncRunResult{
		NotModified:     result.NotModified,
		FeedDocuments:   result.FeedDocuments,
		Discovered:      result.Discovered,
		Fetched:         result.Fetched,
		FetchFailures:   result.FetchFailures,
		ParserFailures:  result.ParserFailures,
		Skipped:         result.Skipped,
		DurationSeconds: elapsed.Seconds(),
		Summary: fmt.Sprintf(
			"not_modified=%t documents=%d discovered=%d fetched=%d fetch_failures=%d parser_failures=%d skipped=%d",
			result.NotModified,
			result.FeedDocuments,
			result.Discovered,
			result.Fetched,
			result.FetchFailures,
			result.ParserFailures,
			result.Skipped,
		),
	}
}

func feedChanged(previous store.SyncState, current source.FeedResult) bool {
	if current.NotModified {
		return false
	}
	if previous.ETag != "" && current.ETag != "" {
		return previous.ETag != current.ETag
	}
	if previous.ETag == "" && previous.LastModified != "" && current.LastModified != "" {
		return previous.LastModified != current.LastModified
	}
	return false
}
