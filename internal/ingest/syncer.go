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

type Repository interface {
	GetSyncState(context.Context) (store.SyncState, error)
	RecordSyncAttempt(context.Context, time.Time) error
	RecordSyncSuccess(context.Context, string, string, time.Time, string) error
	RecordSyncFailure(context.Context, time.Time, error) error
	UpsertSourceMetadata(context.Context, []domain.SourceDocument, time.Time) error
	ListDocumentsForFetch(context.Context, time.Time, time.Time, time.Time, bool) ([]store.SourceDocumentRecord, error)
	ReplaceDocumentIncidents(context.Context, int64, string, []domain.Incident, time.Time) error
	MarkDocumentFetchFailed(context.Context, int64, time.Time, error) error
}

type Syncer struct {
	mu           sync.Mutex
	repository   Repository
	client       source.LiveClient
	clock        func() time.Time
	location     *time.Location
	refreshAfter time.Duration
	logger       *slog.Logger
}

type Result struct {
	NotModified     bool
	Discovered      int
	Fetched         int
	ArticleFailures int
	FetchFailures   int
	ParserFailures  int
	Skipped         int
	WindowStart     time.Time
	WindowEnd       time.Time
}

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

func (s *Syncer) Sync(ctx context.Context) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.clock()
	syncStartedAt := time.Now()
	start, end := s.threeDayWindow(now)
	result := Result{WindowStart: start, WindowEnd: end}
	s.logger.Info("RSS synchronization started", "window_start", start, "window_end", end)

	state, err := s.repository.GetSyncState(ctx)
	if err != nil {
		return result, err
	}
	if err := s.repository.RecordSyncAttempt(ctx, now); err != nil {
		return result, err
	}

	feedStartedAt := time.Now()
	s.logger.Info("RSS feed request started", "conditional", state.ETag != "" || state.LastModified != "")
	feed, err := s.client.FetchFeed(ctx, state.ETag, state.LastModified)
	if err != nil {
		s.logger.Warn("RSS feed request failed", durationAttributes(feedStartedAt, time.Now())...)
		return result, s.fail(ctx, now, err)
	}
	feedAttributes := []any{
		"not_modified", feed.NotModified,
		"documents", len(feed.Documents),
		"skipped", feed.Skipped,
	}
	feedAttributes = append(feedAttributes, durationAttributes(feedStartedAt, time.Now())...)
	s.logger.Info("RSS feed response received", feedAttributes...)
	result.NotModified = feed.NotModified
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
			return result, s.fail(ctx, now, err)
		}
	}

	forceRefresh := feedChanged(state, feed)
	documents, err := s.repository.ListDocumentsForFetch(ctx, start, end, now.Add(-s.refreshAfter), forceRefresh)
	if err != nil {
		return result, s.fail(ctx, now, err)
	}
	for _, document := range documents {
		articleStartedAt := time.Now()
		s.logger.Info("press release request started",
			"document_id", document.ID,
			"external_id", document.ExternalID,
			"source_url", document.SourceURL,
			"published_at", document.PublishedAt,
		)
		contents, fetchErr := s.client.FetchArticle(ctx, document.SourceURL)
		if fetchErr != nil {
			attributes := []any{"document_id", document.ID, "external_id", document.ExternalID, "failure_kind", "fetch"}
			attributes = append(attributes, durationAttributes(articleStartedAt, time.Now())...)
			s.logger.Warn("press release request failed", attributes...)
			result.FetchFailures++
			result.ArticleFailures++
			if markErr := s.repository.MarkDocumentFetchFailed(ctx, document.ID, now, fetchErr); markErr != nil {
				return result, s.fail(ctx, now, errors.Join(fetchErr, markErr))
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
		s.logger.Info("press release response received", responseAttributes...)
		parsed, err := parser.ParsePoliceRelease(contents)
		if err != nil {
			attributes := []any{"document_id", document.ID, "external_id", document.ExternalID, "failure_kind", "parse"}
			attributes = append(attributes, durationAttributes(articleStartedAt, time.Now())...)
			s.logger.Warn("press release processing failed", attributes...)
			result.ParserFailures++
			result.ArticleFailures++
			if markErr := s.repository.MarkDocumentFetchFailed(ctx, document.ID, now, err); markErr != nil {
				return result, s.fail(ctx, now, errors.Join(err, markErr))
			}
			continue
		}
		if err := s.repository.ReplaceDocumentIncidents(ctx, document.ID, parsed.SourceHash, parsed.Incidents, now); err != nil {
			return result, s.fail(ctx, now, err)
		}
		attributes := []any{
			"document_id", document.ID,
			"external_id", document.ExternalID,
			"incidents", len(parsed.Incidents),
		}
		attributes = append(attributes, durationAttributes(articleStartedAt, time.Now())...)
		s.logger.Info("press release processed and stored", attributes...)
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
	resultText := fmt.Sprintf(
		"not_modified=%t discovered=%d fetched=%d article_failures=%d skipped=%d",
		result.NotModified,
		result.Discovered,
		result.Fetched,
		result.ArticleFailures,
		result.Skipped,
	)
	if err := s.repository.RecordSyncSuccess(ctx, etag, lastModified, now, resultText); err != nil {
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
	s.logger.Info("RSS synchronization completed", syncAttributes...)
	return result, nil
}

func durationAttributes(startedAt, completedAt time.Time) []any {
	duration := completedAt.Sub(startedAt)
	return []any{
		"duration", duration.Round(time.Millisecond).String(),
		"duration_seconds", duration.Seconds(),
	}
}

func (s *Syncer) threeDayWindow(value time.Time) (time.Time, time.Time) {
	local := value.In(s.location)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, s.location)
	start := today.AddDate(0, 0, -2)
	end := today.AddDate(0, 0, 1)
	return start, end
}

func (s *Syncer) fail(ctx context.Context, failedAt time.Time, syncError error) error {
	if recordError := s.repository.RecordSyncFailure(ctx, failedAt, syncError); recordError != nil {
		return errors.Join(syncError, recordError)
	}
	return syncError
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
