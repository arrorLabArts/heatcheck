CREATE EXTENSION IF NOT EXISTS citext;
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE policies (
    kind text NOT NULL,
    version text NOT NULL,
    title text NOT NULL,
    content text NOT NULL,
    requires_acceptance boolean NOT NULL DEFAULT false,
    effective_at timestamptz NOT NULL,
    is_current boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (kind, version)
);

CREATE UNIQUE INDEX policies_one_current_per_kind
    ON policies (kind)
    WHERE is_current;

CREATE TABLE users (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email citext NOT NULL UNIQUE,
    password_hash text NOT NULL,
    handle citext NOT NULL UNIQUE,
    display_name text NOT NULL,
    date_of_birth date NOT NULL,
    role text NOT NULL DEFAULT 'user'
        CHECK (role IN ('user', 'moderator', 'admin')),
    status text NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'suspended', 'deleted')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);

CREATE TABLE policy_acceptances (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind text NOT NULL,
    version text NOT NULL,
    accepted_at timestamptz NOT NULL DEFAULT now(),
    ip_address inet,
    PRIMARY KEY (user_id, kind, version),
    FOREIGN KEY (kind, version) REFERENCES policies(kind, version)
);

CREATE TABLE refresh_tokens (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    replaced_by uuid REFERENCES refresh_tokens(id),
    user_agent text NOT NULL DEFAULT '',
    ip_address inet,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX refresh_tokens_active_user
    ON refresh_tokens (user_id, expires_at)
    WHERE revoked_at IS NULL;

CREATE TABLE challenges (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug citext NOT NULL UNIQUE,
    title text NOT NULL,
    description text NOT NULL,
    rules jsonb NOT NULL DEFAULT '[]'::jsonb,
    status text NOT NULL DEFAULT 'draft'
        CHECK (status IN ('draft', 'published', 'closed', 'archived')),
    visibility text NOT NULL DEFAULT 'public'
        CHECK (visibility IN ('public', 'unlisted')),
    starts_at timestamptz NOT NULL,
    ends_at timestamptz NOT NULL,
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (ends_at > starts_at)
);

CREATE INDEX challenges_public_schedule
    ON challenges (starts_at DESC)
    WHERE visibility = 'public' AND status IN ('published', 'closed');

CREATE TABLE media_uploads (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    object_key text NOT NULL UNIQUE,
    content_type text NOT NULL,
    expected_size bigint NOT NULL CHECK (expected_size > 0),
    actual_size bigint,
    status text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'uploaded', 'consumed', 'expired', 'rejected')),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX media_uploads_user_status ON media_uploads (user_id, status);

CREATE TABLE submissions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    challenge_id uuid NOT NULL REFERENCES challenges(id),
    user_id uuid NOT NULL REFERENCES users(id),
    media_upload_id uuid NOT NULL UNIQUE REFERENCES media_uploads(id),
    caption text NOT NULL DEFAULT '',
    verification_status text NOT NULL DEFAULT 'pending'
        CHECK (verification_status IN ('pending', 'passed', 'failed', 'manual_review')),
    verification_details jsonb NOT NULL DEFAULT '{}'::jsonb,
    moderation_status text NOT NULL DEFAULT 'pending'
        CHECK (moderation_status IN ('pending', 'approved', 'rejected', 'removed')),
    style_score numeric(4,2) NOT NULL DEFAULT 0,
    vote_count integer NOT NULL DEFAULT 0,
    published_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (challenge_id, user_id)
);

CREATE INDEX submissions_public_challenge
    ON submissions (challenge_id, style_score DESC, created_at)
    WHERE moderation_status = 'approved';
CREATE INDEX submissions_moderation_queue
    ON submissions (created_at)
    WHERE moderation_status = 'pending';

CREATE TABLE votes (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    submission_id uuid NOT NULL REFERENCES submissions(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    score smallint NOT NULL CHECK (score BETWEEN 1 AND 5),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (submission_id, user_id)
);

CREATE TABLE user_blocks (
    blocker_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    blocked_user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (blocker_id, blocked_user_id),
    CHECK (blocker_id <> blocked_user_id)
);

CREATE TABLE reports (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    reporter_id uuid NOT NULL REFERENCES users(id),
    target_type text NOT NULL
        CHECK (target_type IN ('submission', 'user', 'challenge')),
    target_id uuid NOT NULL,
    reason text NOT NULL
        CHECK (reason IN (
            'harassment', 'hate', 'sexual_content', 'violence', 'self_harm',
            'privacy', 'spam', 'cheating', 'copyright', 'underage',
            'child_safety', 'other'
        )),
    details text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'open'
        CHECK (status IN ('open', 'reviewing', 'resolved', 'dismissed')),
    priority text NOT NULL DEFAULT 'normal'
        CHECK (priority IN ('low', 'normal', 'high', 'urgent')),
    assigned_to uuid REFERENCES users(id),
    resolution_note text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    resolved_at timestamptz
);

CREATE INDEX reports_moderation_queue ON reports (status, priority, created_at);
CREATE UNIQUE INDEX reports_one_active_duplicate
    ON reports (reporter_id, target_type, target_id, reason)
    WHERE status IN ('open', 'reviewing');

CREATE TABLE moderation_actions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    moderator_id uuid NOT NULL REFERENCES users(id),
    target_type text NOT NULL
        CHECK (target_type IN ('submission', 'user', 'challenge')),
    target_id uuid NOT NULL,
    action text NOT NULL
        CHECK (action IN (
            'approve', 'reject', 'remove', 'restore', 'warn',
            'suspend', 'unsuspend', 'close', 'archive'
        )),
    reason text NOT NULL,
    notes text NOT NULL DEFAULT '',
    report_id uuid REFERENCES reports(id),
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX moderation_actions_target
    ON moderation_actions (target_type, target_id, created_at DESC);

CREATE TABLE appeals (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id),
    action_id uuid NOT NULL REFERENCES moderation_actions(id),
    reason text NOT NULL,
    status text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'upheld', 'reversed')),
    reviewed_by uuid REFERENCES users(id),
    resolution_note text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    resolved_at timestamptz,
    UNIQUE (user_id, action_id)
);

CREATE INDEX appeals_queue ON appeals (status, created_at);

CREATE TABLE copyright_notices (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    claimant_name text NOT NULL,
    claimant_email citext NOT NULL,
    claimant_address text NOT NULL,
    relationship text NOT NULL,
    copyrighted_work text NOT NULL,
    infringing_url text NOT NULL,
    submission_id uuid REFERENCES submissions(id),
    good_faith boolean NOT NULL,
    accuracy boolean NOT NULL,
    signature text NOT NULL,
    status text NOT NULL DEFAULT 'received'
        CHECK (status IN (
            'received', 'reviewing', 'actioned', 'rejected',
            'countered', 'restored', 'closed'
        )),
    resolution_note text NOT NULL DEFAULT '',
    actioned_at timestamptz,
    counter_notice_due timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (good_faith AND accuracy)
);

CREATE INDEX copyright_notices_queue
    ON copyright_notices (status, created_at);

CREATE TABLE copyright_counter_notices (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    notice_id uuid NOT NULL UNIQUE REFERENCES copyright_notices(id),
    user_id uuid NOT NULL REFERENCES users(id),
    full_name text NOT NULL,
    address text NOT NULL,
    phone text NOT NULL,
    email citext NOT NULL,
    good_faith boolean NOT NULL,
    consent_to_process boolean NOT NULL,
    signature text NOT NULL,
    status text NOT NULL DEFAULT 'received'
        CHECK (status IN ('received', 'forwarded', 'rejected', 'resolved')),
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (good_faith AND consent_to_process)
);

CREATE TABLE audit_events (
    id bigserial PRIMARY KEY,
    actor_id uuid REFERENCES users(id),
    action text NOT NULL,
    entity_type text NOT NULL,
    entity_id text NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX audit_events_entity
    ON audit_events (entity_type, entity_id, created_at DESC);
CREATE INDEX audit_events_actor
    ON audit_events (actor_id, created_at DESC);

INSERT INTO policies (
    kind, version, title, content, requires_acceptance, effective_at, is_current
) VALUES (
    'community_guidelines',
    '2026-07-24',
    'HeatCheck Community Guidelines',
    $policy$
# HeatCheck Community Guidelines

HeatCheck is for creative, fair, and safe gaming challenges. These rules apply to profiles, challenge entries, video clips, captions, votes, and communications.

## Safety and respect

- Do not threaten, harass, bully, stalk, dox, or encourage abuse of another person.
- Do not publish private information without permission.
- Hate speech, sexual exploitation, sexual content involving minors, and credible threats are prohibited.
- Do not promote self-harm, dangerous real-world stunts, or illegal activity.

## Gameplay and submissions

- Submit only clips you created or have permission to share.
- Do not submit leaks, isolated story spoilers, cheats, exploits, unauthorized modifications, or content obtained by bypassing access controls.
- Entries must follow the challenge rules. Manipulated evidence, impersonation, vote trading, bots, and coordinated score manipulation are prohibited.
- Clearly label potentially sensitive content where HeatCheck provides a label.

## Intellectual property

- Respect copyright, trademark, privacy, publicity, and other rights.
- A valid rights-holder complaint may cause content to be restricted while it is reviewed.
- Repeated infringement may result in account suspension or termination.

## Enforcement

HeatCheck may restrict distribution, remove content, issue warnings, suspend accounts, or terminate accounts. Serious safety issues can be escalated to the appropriate authorities. Users may appeal eligible moderation decisions.
$policy$,
    true,
    '2026-07-24T00:00:00Z',
    true
), (
    'copyright_policy',
    '2026-07-24',
    'HeatCheck Copyright Policy',
    $policy$
# HeatCheck Copyright Policy

Rights holders or their authorized representatives may report material they believe infringes their rights. A notice must identify the copyrighted work, identify the allegedly infringing material, provide contact information, include good-faith and accuracy statements, and contain a physical or electronic signature.

HeatCheck may restrict the identified material while reviewing a notice. The affected uploader will be notified where legally permitted and may submit a counter-notice through the account associated with the submission. Counter-notice contact details may be shared with the original claimant as required to process the dispute.

HeatCheck may restore content when a claim is withdrawn, rejected, successfully countered, or otherwise resolved. Accounts associated with repeated infringement may be suspended or terminated. Knowingly submitting false claims or counter-notices may have legal consequences.
$policy$,
    false,
    '2026-07-24T00:00:00Z',
    true
);
