-- 0002 schema v1 (P1.1): the data model of ARCHITECTURE §4.
--
-- Conventions
-- * IDs are uuid from gen_random_uuid() (core since PostgreSQL 13) [PG-UUID].
--   uuidv7() would need PostgreSQL 18; ADR-0002 still accepts 17.
-- * Mutable tables carry created_at and updated_at; a trigger maintains
--   updated_at. Immutable and append-only tables carry created_at only, and a
--   trigger rejects UPDATE on them.
-- * Enumerations are text plus CHECK, so adding a value is a one-line migration.
-- * Rows that must belong to the same app use composite foreign keys on
--   (app_id, id), so the database itself refuses cross-app references.

-- Shared trigger functions ----------------------------------------------------

CREATE FUNCTION shipyard_set_updated_at() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
	NEW.updated_at := now();
	RETURN NEW;
END
$$;

CREATE FUNCTION shipyard_reject_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
	RAISE EXCEPTION '% on % is not allowed: rows are immutable', TG_OP, TG_TABLE_NAME
		USING ERRCODE = 'restrict_violation';
END
$$;

-- Users and tokens (ADR-0007) --------------------------------------------------

CREATE TABLE users (
	id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
	name       text        NOT NULL UNIQUE CHECK (name ~ '^[a-z][a-z0-9_-]{0,62}$'),
	role       text        NOT NULL DEFAULT 'admin' CHECK (role IN ('admin')),
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE api_tokens (
	id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
	user_id      uuid        NOT NULL REFERENCES users ON DELETE CASCADE,
	name         text        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
	-- Shown in listings to identify a token; never enough to authenticate.
	prefix       text        NOT NULL CHECK (prefix ~ '^shp_[A-Za-z0-9_-]{4,16}$'),
	sha256_hash  bytea       NOT NULL UNIQUE CHECK (octet_length(sha256_hash) = 32),
	scopes       text[]      NOT NULL CHECK (cardinality(scopes) > 0),
	expires_at   timestamptz,
	last_used_at timestamptz,
	revoked_at   timestamptz,
	created_at   timestamptz NOT NULL DEFAULT now(),
	updated_at   timestamptz NOT NULL DEFAULT now(),
	CHECK (expires_at IS NULL OR expires_at > created_at)
);
CREATE INDEX api_tokens_user_id_idx ON api_tokens (user_id);

-- Applications -----------------------------------------------------------------

CREATE TABLE apps (
	id                     uuid         PRIMARY KEY DEFAULT gen_random_uuid(),
	owner_id               uuid         NOT NULL REFERENCES users ON DELETE RESTRICT,
	-- One DNS label: used in container, network, and image names.
	slug                   text         NOT NULL UNIQUE
	                                    CHECK (slug ~ '^[a-z]([a-z0-9-]{0,38}[a-z0-9])?$'),
	repo_full_name         text         NOT NULL
	                                    CHECK (repo_full_name ~ '^[A-Za-z0-9_.-]{1,100}/[A-Za-z0-9_.-]{1,100}$'),
	github_installation_id bigint       CHECK (github_installation_id > 0),
	branch                 text         NOT NULL CHECK (char_length(branch) BETWEEN 1 AND 255),
	-- Relative paths inside the checkout. The worker re-checks them after
	-- resolving symlinks (ADR-0004); these checks are the cheap first line.
	dockerfile_path        text         NOT NULL DEFAULT 'Dockerfile',
	build_context          text         NOT NULL DEFAULT '.',
	internal_port          integer      NOT NULL CHECK (internal_port BETWEEN 1 AND 65535),
	health_path            text         NOT NULL DEFAULT '/' CHECK (health_path ~ '^/[^[:space:]]*$'),
	health_timeout         interval     NOT NULL DEFAULT '60 seconds'
	                                    CHECK (health_timeout BETWEEN '1 second' AND '30 minutes'),
	-- Docker --cpus (fractional cores) and --memory in bytes; 6 MiB is Docker's minimum [DK-RESOURCES].
	cpu_limit              numeric(6,3) NOT NULL DEFAULT 1 CHECK (cpu_limit > 0),
	memory_limit           bigint       NOT NULL DEFAULT 536870912 CHECK (memory_limit >= 6291456),
	stop_timeout           interval     NOT NULL DEFAULT '10 seconds'
	                                    CHECK (stop_timeout BETWEEN '0 seconds' AND '10 minutes'),
	auto_deploy            boolean      NOT NULL DEFAULT false,
	created_at             timestamptz  NOT NULL DEFAULT now(),
	updated_at             timestamptz  NOT NULL DEFAULT now(),
	CHECK (dockerfile_path !~ '^/' AND dockerfile_path !~ '(^|/)\.\.(/|$)' AND dockerfile_path <> ''),
	CHECK (build_context   !~ '^/' AND build_context   !~ '(^|/)\.\.(/|$)' AND build_context   <> '')
);
CREATE INDEX apps_owner_id_idx ON apps (owner_id);

-- Secrets and environment revisions (ADR-0005) ---------------------------------

CREATE TABLE secret_values (
	id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
	app_id      uuid        NOT NULL REFERENCES apps ON DELETE CASCADE,
	key         text        NOT NULL CHECK (key ~ '^[A-Za-z_][A-Za-z0-9_]{0,254}$'),
	-- AES-256-GCM output: nonce + ciphertext + tag, never empty.
	ciphertext  bytea       NOT NULL CHECK (octet_length(ciphertext) > 0),
	wrapped_dek bytea       NOT NULL CHECK (octet_length(wrapped_dek) > 0),
	kek_id      text        NOT NULL CHECK (kek_id ~ '^[A-Za-z0-9_-]{1,64}$'),
	created_at  timestamptz NOT NULL DEFAULT now(),
	-- Targets for the composite foreign key from env_revision_entries.
	UNIQUE (app_id, key, id)
);

CREATE TABLE env_revisions (
	id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
	app_id     uuid        NOT NULL REFERENCES apps ON DELETE CASCADE,
	number     integer     NOT NULL CHECK (number > 0),
	created_at timestamptz NOT NULL DEFAULT now(),
	UNIQUE (app_id, number),
	UNIQUE (app_id, id)
);

CREATE TABLE env_revision_entries (
	revision_id     uuid NOT NULL,
	app_id          uuid NOT NULL,
	key             text NOT NULL CHECK (key ~ '^[A-Za-z_][A-Za-z0-9_]{0,254}$'),
	secret_value_id uuid,
	plain_value     text,
	PRIMARY KEY (revision_id, key),
	FOREIGN KEY (app_id, revision_id) REFERENCES env_revisions (app_id, id) ON DELETE CASCADE,
	-- The secret must belong to the same app and carry the same key; its AAD
	-- binds (app_id, key, value_id), so a mismatch could never decrypt.
	FOREIGN KEY (app_id, key, secret_value_id) REFERENCES secret_values (app_id, key, id) ON DELETE CASCADE,
	CHECK (num_nonnulls(secret_value_id, plain_value) = 1)
);
CREATE INDEX env_revision_entries_secret_value_id_idx ON env_revision_entries (secret_value_id);

-- Operations queue (ADR-0002) --------------------------------------------------

CREATE TABLE operations (
	id               uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
	app_id           uuid        NOT NULL REFERENCES apps ON DELETE CASCADE,
	kind             text        NOT NULL CHECK (kind IN ('deploy', 'rollback')),
	-- gh:<X-GitHub-Delivery> for webhooks; the client's Idempotency-Key otherwise.
	idempotency_key  text        NOT NULL UNIQUE CHECK (char_length(idempotency_key) BETWEEN 1 AND 255),
	status           text        NOT NULL DEFAULT 'queued'
	                             CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'cancelled')),
	phase            text        CHECK (char_length(phase) BETWEEN 1 AND 64),
	payload          jsonb       NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(payload) = 'object'),
	lease_owner      text        CHECK (char_length(lease_owner) BETWEEN 1 AND 255),
	lease_expires_at timestamptz,
	attempt          integer     NOT NULL DEFAULT 0 CHECK (attempt >= 0),
	max_attempts     integer     NOT NULL DEFAULT 3 CHECK (max_attempts > 0),
	run_after        timestamptz NOT NULL DEFAULT now(),
	last_error       text,
	finished_at      timestamptz,
	created_at       timestamptz NOT NULL DEFAULT now(),
	updated_at       timestamptz NOT NULL DEFAULT now(),
	UNIQUE (app_id, id),
	CHECK (attempt <= max_attempts),
	CHECK (status <> 'running' OR (lease_owner IS NOT NULL AND lease_expires_at IS NOT NULL)),
	CHECK ((status IN ('succeeded', 'failed', 'cancelled')) = (finished_at IS NOT NULL))
);
-- Invariant 4: at most one running operation per app.
CREATE UNIQUE INDEX operations_one_running_per_app ON operations (app_id) WHERE status = 'running';
-- Claim order for SELECT … FOR UPDATE SKIP LOCKED [PG-SELECT].
CREATE INDEX operations_claim_idx ON operations (run_after, created_at) WHERE status = 'queued';
-- Coalescing: find an app's queued deploys; the reconciler finds expired leases.
CREATE INDEX operations_queued_app_idx ON operations (app_id) WHERE status = 'queued';
CREATE INDEX operations_lease_idx ON operations (lease_expires_at) WHERE status = 'running';

CREATE TABLE operation_events (
	operation_id uuid        NOT NULL REFERENCES operations ON DELETE CASCADE,
	-- Monotonic per operation; the SSE id for Last-Event-ID resume [WHATWG-SSE].
	seq          bigint      NOT NULL CHECK (seq > 0),
	ts           timestamptz NOT NULL DEFAULT now(),
	level        text        NOT NULL CHECK (level IN ('debug', 'info', 'warn', 'error')),
	message      text        NOT NULL CHECK (char_length(message) <= 16384),
	PRIMARY KEY (operation_id, seq)
);

-- Deployments -------------------------------------------------------------------

CREATE TABLE deployments (
	id                   uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
	app_id               uuid        NOT NULL REFERENCES apps ON DELETE CASCADE,
	operation_id         uuid        NOT NULL UNIQUE,
	kind                 text        NOT NULL CHECK (kind IN ('build', 'rollback')),
	-- For a rollback: the earlier deployment whose image and env it reuses.
	source_deployment_id uuid,
	source_commit_sha    text        NOT NULL CHECK (source_commit_sha ~ '^([0-9a-f]{40}|[0-9a-f]{64})$'),
	-- The Engine image ID is the release identity; tags are for humans (ADR-0004).
	image_id             text        CHECK (image_id ~ '^sha256:[0-9a-f]{64}$'),
	build_metadata       jsonb       CHECK (jsonb_typeof(build_metadata) = 'object'),
	env_revision_id      uuid,
	container_id         text        CHECK (container_id ~ '^[0-9a-f]{64}$'),
	status               text        NOT NULL DEFAULT 'queued'
	                                 CHECK (status IN ('queued', 'building', 'starting', 'health_checking',
	                                                   'switching', 'active', 'superseded', 'failed', 'cancelled')),
	failure_reason       text,
	queued_at            timestamptz NOT NULL DEFAULT now(),
	building_at          timestamptz,
	starting_at          timestamptz,
	health_checking_at   timestamptz,
	switching_at         timestamptz,
	active_at            timestamptz,
	ended_at             timestamptz,
	created_at           timestamptz NOT NULL DEFAULT now(),
	updated_at           timestamptz NOT NULL DEFAULT now(),
	UNIQUE (app_id, id),
	FOREIGN KEY (app_id, operation_id) REFERENCES operations (app_id, id) ON DELETE CASCADE,
	FOREIGN KEY (app_id, source_deployment_id) REFERENCES deployments (app_id, id),
	FOREIGN KEY (app_id, env_revision_id) REFERENCES env_revisions (app_id, id),
	CHECK ((kind = 'rollback') = (source_deployment_id IS NOT NULL)),
	CHECK (source_deployment_id <> id),
	CHECK ((status = 'failed') = (failure_reason IS NOT NULL)),
	-- Only a started, identified candidate can serve (invariants 5 and 6).
	CHECK (status NOT IN ('health_checking', 'switching', 'active', 'superseded')
	       OR (image_id IS NOT NULL AND container_id IS NOT NULL))
);
-- At most one serving deployment per app: the switch transaction marks the
-- candidate active and the previous one superseded together (ARCHITECTURE §5).
CREATE UNIQUE INDEX deployments_one_active_per_app ON deployments (app_id) WHERE status = 'active';
CREATE INDEX deployments_app_created_idx ON deployments (app_id, created_at DESC);

-- Routes (ADR-0003) --------------------------------------------------------------

CREATE TABLE routes (
	id             uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
	-- Lowercase so that uniqueness is case-insensitive; one app per hostname.
	hostname       text        NOT NULL UNIQUE
	                           CHECK (char_length(hostname) <= 253
	                                  AND hostname ~ '^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]([a-z0-9-]{0,61}[a-z0-9])?$'),
	app_id         uuid        NOT NULL REFERENCES apps ON DELETE CASCADE,
	deployment_id  uuid,
	upstream       text        CHECK (char_length(upstream) BETWEEN 1 AND 255),
	dns_checked_at timestamptz,
	created_at     timestamptz NOT NULL DEFAULT now(),
	updated_at     timestamptz NOT NULL DEFAULT now(),
	FOREIGN KEY (app_id, deployment_id) REFERENCES deployments (app_id, id),
	CHECK ((deployment_id IS NULL) = (upstream IS NULL))
);
CREATE INDEX routes_app_id_idx ON routes (app_id);

-- GitHub webhook deliveries --------------------------------------------------------

CREATE TABLE webhook_deliveries (
	-- The X-GitHub-Delivery GUID; a redelivery reuses it [GH-BP].
	delivery_id   text        PRIMARY KEY CHECK (delivery_id ~ '^[A-Za-z0-9-]{1,64}$'),
	event         text        NOT NULL CHECK (char_length(event) BETWEEN 1 AND 64),
	repository_id bigint      CHECK (repository_id > 0),
	ref           text        CHECK (char_length(ref) BETWEEN 1 AND 255),
	after_sha     text        CHECK (after_sha ~ '^([0-9a-f]{40}|[0-9a-f]{64})$'),
	received_at   timestamptz NOT NULL DEFAULT now(),
	outcome       text        CHECK (outcome IN ('queued', 'ignored', 'rejected')),
	operation_id  uuid        REFERENCES operations ON DELETE SET NULL,
	updated_at    timestamptz NOT NULL DEFAULT now()
);

-- Audit (ADR-0007) -------------------------------------------------------------------

CREATE TABLE audit_events (
	id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
	ts         timestamptz NOT NULL DEFAULT now(),
	-- A reference such as token:<prefix>, webhook, or system; never a secret.
	actor      text        NOT NULL CHECK (char_length(actor) BETWEEN 1 AND 255),
	action     text        NOT NULL CHECK (char_length(action) BETWEEN 1 AND 128),
	target     text        CHECK (char_length(target) <= 255),
	result     text        NOT NULL CHECK (result IN ('success', 'failure', 'denied')),
	request_id text        CHECK (char_length(request_id) <= 64)
);
CREATE INDEX audit_events_ts_idx ON audit_events (ts);

-- Triggers ------------------------------------------------------------------------------

CREATE TRIGGER users_updated_at              BEFORE UPDATE ON users              FOR EACH ROW EXECUTE FUNCTION shipyard_set_updated_at();
CREATE TRIGGER api_tokens_updated_at         BEFORE UPDATE ON api_tokens         FOR EACH ROW EXECUTE FUNCTION shipyard_set_updated_at();
CREATE TRIGGER apps_updated_at               BEFORE UPDATE ON apps               FOR EACH ROW EXECUTE FUNCTION shipyard_set_updated_at();
CREATE TRIGGER operations_updated_at         BEFORE UPDATE ON operations         FOR EACH ROW EXECUTE FUNCTION shipyard_set_updated_at();
CREATE TRIGGER deployments_updated_at        BEFORE UPDATE ON deployments        FOR EACH ROW EXECUTE FUNCTION shipyard_set_updated_at();
CREATE TRIGGER routes_updated_at             BEFORE UPDATE ON routes             FOR EACH ROW EXECUTE FUNCTION shipyard_set_updated_at();
CREATE TRIGGER webhook_deliveries_updated_at BEFORE UPDATE ON webhook_deliveries FOR EACH ROW EXECUTE FUNCTION shipyard_set_updated_at();

-- Immutable (ADR-0005) and append-only rows. DELETE stays possible for cascades
-- and retention (ADR-0006), except for audit events, which are kept forever.
CREATE TRIGGER secret_values_immutable        BEFORE UPDATE ON secret_values        FOR EACH ROW EXECUTE FUNCTION shipyard_reject_change();
CREATE TRIGGER env_revisions_immutable        BEFORE UPDATE ON env_revisions        FOR EACH ROW EXECUTE FUNCTION shipyard_reject_change();
CREATE TRIGGER env_revision_entries_immutable BEFORE UPDATE ON env_revision_entries FOR EACH ROW EXECUTE FUNCTION shipyard_reject_change();
CREATE TRIGGER operation_events_append_only   BEFORE UPDATE ON operation_events     FOR EACH ROW EXECUTE FUNCTION shipyard_reject_change();
CREATE TRIGGER audit_events_append_only       BEFORE UPDATE OR DELETE ON audit_events FOR EACH ROW EXECUTE FUNCTION shipyard_reject_change();
