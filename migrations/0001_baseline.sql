-- 0001 baseline: starts Shipyard's schema history.
-- The runner creates schema_migrations itself. Application tables arrive in
-- P1.1 as 0002 onwards. Never edit this file once applied; add a new one.
COMMENT ON TABLE schema_migrations IS 'Forward-only migration history managed by shipyard-api migrate';
