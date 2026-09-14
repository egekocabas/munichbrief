CREATE TABLE contact_messages (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 submission_hash TEXT NOT NULL UNIQUE,
 email TEXT NOT NULL,
 topic TEXT NOT NULL CHECK(topic IN ('correction','privacy','general')),
 message TEXT NOT NULL,
 language TEXT NOT NULL,
 created_at INTEGER NOT NULL,
 read_at INTEGER NOT NULL DEFAULT 0,
 resolved_at INTEGER NOT NULL DEFAULT 0,
 hold_reason TEXT NOT NULL DEFAULT '',
 hold_review_at INTEGER NOT NULL DEFAULT 0,
 notification_state TEXT NOT NULL DEFAULT 'pending' CHECK(notification_state IN ('pending','sending','accepted','retry','failed','uncertain','cancelled')),
 notification_attempts INTEGER NOT NULL DEFAULT 0,
 next_attempt_at INTEGER NOT NULL DEFAULT 0,
 notification_code TEXT NOT NULL DEFAULT '',
 provider_id TEXT NOT NULL DEFAULT ''
);
CREATE INDEX contact_created ON contact_messages(created_at DESC, id DESC);
CREATE INDEX contact_notification ON contact_messages(notification_state, next_attempt_at, id);
CREATE INDEX contact_retention ON contact_messages(resolved_at) WHERE resolved_at > 0;
CREATE TABLE contact_budgets (period TEXT PRIMARY KEY, attempts INTEGER NOT NULL DEFAULT 0);
