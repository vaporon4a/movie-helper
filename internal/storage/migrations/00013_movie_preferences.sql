-- +goose Up
CREATE TABLE movie_preference_settings (
 chat_id INTEGER PRIMARY KEY REFERENCES chats(chat_id) ON DELETE CASCADE,
 mode TEXT NOT NULL DEFAULT 'shadow' CHECK (mode IN ('off','shadow','on')),
 effective_from INTEGER NOT NULL DEFAULT 0,
 policy_version TEXT NOT NULL DEFAULT 'taste-v1',
 updated_at INTEGER NOT NULL
);

ALTER TABLE movie_poll_options ADD COLUMN metadata_json TEXT NOT NULL DEFAULT '{}';
ALTER TABLE movie_poll_options ADD COLUMN selection_role TEXT NOT NULL DEFAULT 'legacy'
 CHECK (selection_role IN ('legacy','exploit','explore','wildcard'));
ALTER TABLE movie_poll_options ADD COLUMN selection_score REAL NOT NULL DEFAULT 0;
ALTER TABLE movie_poll_options ADD COLUMN policy_version TEXT NOT NULL DEFAULT 'legacy';

ALTER TABLE movie_recommendations ADD COLUMN genre_ids_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE movie_recommendations ADD COLUMN ranking_score REAL NOT NULL DEFAULT 0;
ALTER TABLE movie_recommendations ADD COLUMN ranking_breakdown_json TEXT NOT NULL DEFAULT '{}';
ALTER TABLE movie_recommendations ADD COLUMN policy_version TEXT NOT NULL DEFAULT 'legacy';

CREATE TABLE movie_ranking_candidates (
 round_id INTEGER NOT NULL REFERENCES movie_rounds(id) ON DELETE CASCADE,
 source_bucket TEXT NOT NULL,
 tmdb_id INTEGER NOT NULL,
 metadata_json TEXT NOT NULL,
 legacy_position INTEGER,
 adaptive_position INTEGER,
 ranking_score REAL NOT NULL,
 ranking_breakdown_json TEXT NOT NULL,
 policy_version TEXT NOT NULL,
 selected_mode TEXT NOT NULL CHECK (selected_mode IN ('legacy','adaptive','none')),
 created_at INTEGER NOT NULL,
 PRIMARY KEY(round_id,source_bucket,tmdb_id)
);
CREATE INDEX movie_ranking_candidate_round ON movie_ranking_candidates(round_id,source_bucket);

-- +goose Down
DROP INDEX movie_ranking_candidate_round;
DROP TABLE movie_ranking_candidates;
ALTER TABLE movie_recommendations DROP COLUMN policy_version;
ALTER TABLE movie_recommendations DROP COLUMN ranking_breakdown_json;
ALTER TABLE movie_recommendations DROP COLUMN ranking_score;
ALTER TABLE movie_recommendations DROP COLUMN genre_ids_json;
ALTER TABLE movie_poll_options DROP COLUMN policy_version;
ALTER TABLE movie_poll_options DROP COLUMN selection_score;
ALTER TABLE movie_poll_options DROP COLUMN selection_role;
ALTER TABLE movie_poll_options DROP COLUMN metadata_json;
DROP TABLE movie_preference_settings;
