-- +goose Up
CREATE TABLE film_research_sources (
 id TEXT PRIMARY KEY CHECK(length(id)=64),
 tmdb_id INTEGER NOT NULL CHECK(tmdb_id>0),
 snapshot_json TEXT NOT NULL CHECK(json_valid(snapshot_json)),
 created_at INTEGER NOT NULL,
 UNIQUE(id,tmdb_id)
);
CREATE TABLE film_research_dossiers (
 id TEXT PRIMARY KEY CHECK(length(id)=64),
 tmdb_id INTEGER NOT NULL CHECK(tmdb_id>0),
 snapshot_json TEXT NOT NULL CHECK(json_valid(snapshot_json)),
 created_at INTEGER NOT NULL,
 UNIQUE(id,tmdb_id)
);
CREATE TABLE film_research_dossier_sources (
 dossier_id TEXT NOT NULL,
 source_id TEXT NOT NULL,
 tmdb_id INTEGER NOT NULL,
 PRIMARY KEY(dossier_id,source_id),
 FOREIGN KEY(dossier_id,tmdb_id) REFERENCES film_research_dossiers(id,tmdb_id),
 FOREIGN KEY(source_id,tmdb_id) REFERENCES film_research_sources(id,tmdb_id)
);
CREATE UNIQUE INDEX club_review_session_watch_scope ON club_review_sessions(id,watch_id,chat_id);
CREATE TABLE film_research_preparations (
 id TEXT PRIMARY KEY CHECK(length(id)=64),
 chat_id INTEGER NOT NULL,
 session_id INTEGER NOT NULL,
 watch_id INTEGER NOT NULL,
 watch_revision INTEGER NOT NULL CHECK(watch_revision>0),
 tmdb_id INTEGER NOT NULL,
 dossier_id TEXT NOT NULL,
 policy_version TEXT NOT NULL,
 snapshot_json TEXT NOT NULL CHECK(json_valid(snapshot_json)),
 created_at INTEGER NOT NULL,
 FOREIGN KEY(session_id,watch_id,chat_id) REFERENCES club_review_sessions(id,watch_id,chat_id),
 FOREIGN KEY(watch_id,chat_id) REFERENCES club_watches(id,chat_id),
 FOREIGN KEY(watch_id,watch_revision) REFERENCES club_watch_audit(watch_id,revision),
 FOREIGN KEY(dossier_id,tmdb_id) REFERENCES film_research_dossiers(id,tmdb_id)
);
CREATE INDEX film_research_session ON film_research_preparations(chat_id,session_id,watch_revision);

-- +goose StatementBegin
CREATE TRIGGER film_research_source_immutable BEFORE UPDATE ON film_research_sources
BEGIN SELECT RAISE(ABORT,'immutable film research source'); END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER film_research_dossier_immutable BEFORE UPDATE ON film_research_dossiers
BEGIN SELECT RAISE(ABORT,'immutable film research dossier'); END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER film_research_links_immutable BEFORE UPDATE ON film_research_dossier_sources
BEGIN SELECT RAISE(ABORT,'immutable film research evidence link'); END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER film_research_preparation_immutable BEFORE UPDATE ON film_research_preparations
BEGIN SELECT RAISE(ABORT,'immutable film research preparation'); END;
-- +goose StatementEnd

-- +goose Down
DROP TABLE film_research_preparations;
DROP INDEX club_review_session_watch_scope;
DROP TABLE film_research_dossier_sources;
DROP TABLE film_research_dossiers;
DROP TABLE film_research_sources;
