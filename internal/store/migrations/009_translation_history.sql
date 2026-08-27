CREATE INDEX presentation_translations_history_idx
ON presentation_translations(julianday(updated_at) DESC, id DESC)
WHERE request_kind <> 'imported';
