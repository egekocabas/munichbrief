package gazetteer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// A failed refresh may reuse recent, validated downloads, never an arbitrary
// old active source. Changing the parser contract or source definition prevents
// reuse. Publication still requires every source to pass validation.
const stagedSourceLifetime = 24 * time.Hour

type stagedSource struct {
	ContentHash, ETag, LastModified string
	Entries                         []Entry
}

func (s *Store) stagedSource(ctx context.Context, definition SourceDefinition, now time.Time) (SourceSnapshot, bool, error) {
	var fingerprint, fetched, payload string
	err := s.db.QueryRowContext(ctx, `SELECT definition_hash,fetched_at,snapshot_json FROM gazetteer_staged_sources WHERE source_key=?`, definition.Key).Scan(&fingerprint, &fetched, &payload)
	if err == sql.ErrNoRows {
		return SourceSnapshot{}, false, nil
	}
	if err != nil {
		return SourceSnapshot{}, false, err
	}
	at := parseTime(fetched)
	if fingerprint != aggregateSourceHash([]SourceSnapshot{{Definition: definition}}, nil) || at.IsZero() || at.After(now) || now.Sub(at) >= stagedSourceLifetime {
		return SourceSnapshot{}, false, nil
	}
	var cached stagedSource
	if err := json.Unmarshal([]byte(payload), &cached); err != nil {
		return SourceSnapshot{}, false, fmt.Errorf("decode staged %s: %w", definition.Key, err)
	}
	if cached.ContentHash == "" || len(cached.Entries) < definition.MinimumRows || len(cached.Entries) > definition.MaximumRows {
		return SourceSnapshot{}, false, fmt.Errorf("invalid staged %s snapshot", definition.Key)
	}
	return SourceSnapshot{Definition: definition, ContentHash: cached.ContentHash, ETag: cached.ETag, LastModified: cached.LastModified, FetchedAt: at, Entries: cached.Entries}, true, nil
}

func (s *Store) stageSource(ctx context.Context, snapshot SourceSnapshot) error {
	payload, err := json.Marshal(stagedSource{ContentHash: snapshot.ContentHash, ETag: snapshot.ETag, LastModified: snapshot.LastModified, Entries: snapshot.Entries})
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO gazetteer_staged_sources(source_key,definition_hash,fetched_at,snapshot_json) VALUES (?,?,?,?) ON CONFLICT(source_key) DO UPDATE SET definition_hash=excluded.definition_hash,fetched_at=excluded.fetched_at,snapshot_json=excluded.snapshot_json`, snapshot.Definition.Key, aggregateSourceHash([]SourceSnapshot{{Definition: snapshot.Definition}}, nil), formatTime(snapshot.FetchedAt), string(payload))
	return err
}
