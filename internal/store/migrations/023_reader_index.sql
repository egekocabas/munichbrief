-- Derived public reader data. Rebuilt from current presentations at startup.
CREATE TABLE reader_documents (
 id INTEGER PRIMARY KEY,
 incident_id INTEGER NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
 language TEXT NOT NULL,
 run_id INTEGER NOT NULL,
 title TEXT NOT NULL,
 summary TEXT NOT NULL,
 area TEXT NOT NULL,
 category TEXT NOT NULL,
 assistance TEXT NOT NULL,
 event_date TEXT NOT NULL,
 event_time TEXT NOT NULL,
 day_part TEXT NOT NULL,
 time_group INTEGER NOT NULL,
 published_at TEXT NOT NULL,
 published_date TEXT NOT NULL,
 position INTEGER NOT NULL,
 number TEXT NOT NULL,
 search_text TEXT NOT NULL,
 UNIQUE(incident_id,language)
);
CREATE INDEX reader_publication_idx ON reader_documents(language,published_at DESC,position,incident_id);
CREATE INDEX reader_event_idx ON reader_documents(language,event_date DESC,time_group,event_time DESC,published_at DESC,incident_id);
CREATE INDEX reader_area_idx ON reader_documents(language,area);
CREATE INDEX reader_category_idx ON reader_documents(language,category);
CREATE INDEX reader_number_idx ON reader_documents(language,number);
CREATE VIRTUAL TABLE reader_fts USING fts5(search_text,content='reader_documents',content_rowid='id',tokenize='trigram');
CREATE TRIGGER reader_fts_insert AFTER INSERT ON reader_documents BEGIN
 INSERT INTO reader_fts(rowid,search_text) VALUES(new.id,new.search_text);
END;
CREATE TRIGGER reader_fts_delete AFTER DELETE ON reader_documents BEGIN
 INSERT INTO reader_fts(reader_fts,rowid,search_text) VALUES('delete',old.id,old.search_text);
END;
