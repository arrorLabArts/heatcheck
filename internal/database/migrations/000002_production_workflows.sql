ALTER TABLE users
    ADD COLUMN email_verified_at timestamptz;

ALTER TABLE users
    DROP CONSTRAINT users_status_check;

ALTER TABLE users
    ADD CONSTRAINT users_status_check
    CHECK (status IN ('active', 'suspended', 'deletion_pending', 'deleted'));

-- Accounts created before email verification existed remain usable.
UPDATE users
SET email_verified_at = created_at
WHERE email_verified_at IS NULL;

ALTER TABLE refresh_tokens
    ADD COLUMN family_id uuid,
    ADD COLUMN last_used_at timestamptz;

UPDATE refresh_tokens
SET family_id = id
WHERE family_id IS NULL;

ALTER TABLE refresh_tokens
    ALTER COLUMN family_id SET NOT NULL;

CREATE INDEX refresh_tokens_family
    ON refresh_tokens (family_id, created_at);

CREATE TABLE email_verification_tokens (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX email_verification_tokens_active_user
    ON email_verification_tokens (user_id, expires_at)
    WHERE consumed_at IS NULL;

CREATE TABLE password_reset_tokens (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX password_reset_tokens_active_user
    ON password_reset_tokens (user_id, expires_at)
    WHERE consumed_at IS NULL;

ALTER TABLE media_uploads
    ADD COLUMN duration_seconds numeric(8,3),
    ADD COLUMN width integer,
    ADD COLUMN height integer,
    ADD COLUMN video_codec text,
    ADD COLUMN scanned_at timestamptz,
    ADD COLUMN retained_until timestamptz,
    ADD COLUMN processed_object_key text UNIQUE,
    ADD COLUMN thumbnail_object_key text UNIQUE;

CREATE TABLE jobs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kind text NOT NULL,
    dedupe_key text,
    entity_id uuid,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    status text NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'running', 'succeeded', 'dead')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    max_attempts integer NOT NULL DEFAULT 5 CHECK (max_attempts > 0),
    available_at timestamptz NOT NULL DEFAULT now(),
    locked_at timestamptz,
    locked_by text,
    last_error text NOT NULL DEFAULT '',
    result jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz
);

CREATE UNIQUE INDEX jobs_active_dedupe
    ON jobs (dedupe_key)
    WHERE dedupe_key IS NOT NULL AND status IN ('queued', 'running');

CREATE INDEX jobs_claim
    ON jobs (available_at, created_at)
    WHERE status = 'queued';

CREATE TABLE worker_heartbeats (
    worker_id text PRIMARY KEY,
    version text NOT NULL DEFAULT '',
    current_job_id uuid REFERENCES jobs(id) ON DELETE SET NULL,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    started_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE account_exports (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'processing', 'ready', 'failed', 'expired')),
    object_key text,
    expires_at timestamptz,
    error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz
);

CREATE UNIQUE INDEX account_exports_one_active
    ON account_exports (user_id)
    WHERE status IN ('pending', 'processing', 'ready');

CREATE TABLE account_deletion_requests (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    execute_after timestamptz NOT NULL,
    cancelled_at timestamptz,
    completed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX account_deletion_requests_due
    ON account_deletion_requests (execute_after)
    WHERE cancelled_at IS NULL AND completed_at IS NULL;

CREATE TABLE rate_limits (
    key text NOT NULL,
    window_started_at timestamptz NOT NULL,
    count integer NOT NULL CHECK (count > 0),
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (key, window_started_at)
);

CREATE INDEX rate_limits_expiry ON rate_limits (expires_at);

INSERT INTO policies (
    kind, version, title, content, requires_acceptance, effective_at, is_current
) VALUES (
    'platform_affiliation_disclaimer',
    '2026-07-24',
    'Platform and Trademark Disclaimer',
    $policy$
# Platform and Trademark Disclaimer

HeatCheck is an independent community service. It is not affiliated with, endorsed by, sponsored by, or operated by Rockstar Games, Take-Two Interactive, or any game publisher, platform holder, or trademark owner unless HeatCheck states otherwise in writing.

Game names, logos, characters, footage, and other trademarks or copyrighted materials belong to their respective owners. References are used only to identify compatible games, discuss gameplay, and organize community challenges. Users must not present HeatCheck challenges, profiles, clips, or promotional material as official publisher content or imply an endorsement that does not exist.

HeatCheck may remove content or branding that creates confusion about source, sponsorship, or affiliation, or that otherwise violates a rights holder's trademark or copyright.
$policy$,
    false,
    '2026-07-24T00:00:00Z',
    true
);
