CREATE TABLE contact_settings (
 id INTEGER PRIMARY KEY CHECK(id = 1),
 form_enabled INTEGER NOT NULL DEFAULT 1 CHECK(form_enabled IN (0,1)),
 notifications_enabled INTEGER NOT NULL DEFAULT 1 CHECK(notifications_enabled IN (0,1))
);
INSERT INTO contact_settings(id) VALUES(1);
ALTER TABLE contact_messages ADD COLUMN is_test INTEGER NOT NULL DEFAULT 0 CHECK(is_test IN (0,1));
