CREATE TABLE billing_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    provider text NOT NULL
        CHECK (provider IN ('revenuecat')),
    external_event_id text NOT NULL,
    event_type text NOT NULL,
    user_id uuid REFERENCES users(id) ON DELETE SET NULL,
    environment text NOT NULL
        CHECK (environment IN ('production', 'sandbox', 'unknown')),
    event_timestamp timestamptz NOT NULL,
    payload_sha256 text NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    processed_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (provider, external_event_id)
);

CREATE INDEX billing_events_user_created
    ON billing_events (user_id, processed_at DESC);

CREATE TABLE subscriptions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider text NOT NULL
        CHECK (provider IN ('revenuecat')),
    entitlement_id text NOT NULL,
    product_id text NOT NULL DEFAULT '',
    store text NOT NULL DEFAULT '',
    environment text NOT NULL
        CHECK (environment IN ('production', 'sandbox', 'unknown')),
    status text NOT NULL
        CHECK (status IN (
            'inactive', 'active', 'canceled', 'billing_issue', 'expired'
        )),
    active boolean NOT NULL DEFAULT false,
    will_renew boolean NOT NULL DEFAULT false,
    current_period_start timestamptz,
    current_period_end timestamptz,
    management_url text NOT NULL DEFAULT '',
    source_updated_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (provider, user_id, entitlement_id)
);

CREATE INDEX subscriptions_active_user
    ON subscriptions (user_id, entitlement_id, current_period_end)
    WHERE active;

CREATE TABLE entitlements (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    entitlement_id text NOT NULL,
    provider text NOT NULL
        CHECK (provider IN ('revenuecat')),
    product_id text NOT NULL DEFAULT '',
    active boolean NOT NULL DEFAULT false,
    valid_from timestamptz,
    valid_until timestamptz,
    source_updated_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, entitlement_id)
);

CREATE INDEX entitlements_active_user
    ON entitlements (user_id, entitlement_id, valid_until)
    WHERE active;

CREATE TABLE submission_usage_reservations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    entitlement_id text NOT NULL,
    media_upload_id uuid NOT NULL UNIQUE
        REFERENCES media_uploads(id) ON DELETE CASCADE,
    submission_id uuid UNIQUE
        REFERENCES submissions(id) ON DELETE SET NULL,
    status text NOT NULL DEFAULT 'reserved'
        CHECK (status IN ('reserved', 'consumed', 'released')),
    reserved_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    released_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX submission_usage_user_reserved
    ON submission_usage_reservations (user_id, reserved_at)
    WHERE status IN ('reserved', 'consumed');

CREATE INDEX submission_usage_user_consumed
    ON submission_usage_reservations (user_id, consumed_at)
    WHERE status = 'consumed';

CREATE INDEX submission_usage_consumed
    ON submission_usage_reservations (consumed_at)
    WHERE status = 'consumed';

CREATE INDEX submission_usage_expiry
    ON submission_usage_reservations (expires_at)
    WHERE status = 'reserved';
