package ingest

import (
	"context"
	"errors"
	"fmt"
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

func NewSyncer(repository Repository, client source.LiveClient, refreshAfter time.Duration, clock func() time.Time) (*Syncer, error) {
	if repository == nil || client == nil {
		return nil, errors.New("repository and live source client are required")
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
	}, nil
}

func (s *Syncer) Sync(ctx context.Context) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.clock()
	start, end := s.threeDayWindow(now)
	result := Result{WindowStart: start, WindowEnd: end}

	state, err := s.repository.GetSyncState(ctx)
	if err != nil {
		return result, err
	}
	if err := s.repository.RecordSyncAttempt(ctx, now); err != nil {
		return result, err
	}

	feed, err := s.client.FetchFeed(ctx, state.ETag, state.LastModified)
	if err != nil {
		return result, s.fail(ctx, now, err)
	}
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
		contents, fetchErr := s.client.FetchArticle(ctx, document.SourceURL)
		if fetchErr != nil {
			result.FetchFailures++
			result.ArticleFailures++
			if markErr := s.repository.MarkDocumentFetchFailed(ctx, document.ID, now, fetchErr); markErr != nil {
				return result, s.fail(ctx, now, errors.Join(fetchErr, markErr))
			}
			continue
		}
		parsed, err := parser.ParsePoliceRelease(contents)
		if err != nil {
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
	return result, nil
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
