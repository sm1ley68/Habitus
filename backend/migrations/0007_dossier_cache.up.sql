ALTER TABLE chat_search_results
    ADD COLUMN dossier JSONB,
    ADD COLUMN dossier_version TEXT,
    ADD COLUMN dossier_updated_at TIMESTAMPTZ;
