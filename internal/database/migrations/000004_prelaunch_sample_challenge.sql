-- System-seeded challenges have no human administrator as their author.
ALTER TABLE challenges
    ALTER COLUMN created_by DROP NOT NULL;

-- GTA VI is not released yet. This temporary challenge uses only footage that
-- players record themselves in GTA V or GTA Online. It is seeded only before
-- the announced November 19, 2026 launch and remains active for at most 14 days
-- from deployment so operators still own the ongoing challenge calendar.
WITH inserted_challenge AS (
    INSERT INTO challenges (
        slug,
        title,
        description,
        rules,
        status,
        visibility,
        starts_at,
        ends_at,
        created_by
    )
    SELECT
        'prelaunch-sunset-getaway',
        'Pre-Launch Warm-Up: Sunset Getaway',
        'GTA VI is not released yet, so this warm-up uses GTA V or GTA Online. Record a stylish 15-30 second sunset getaway using footage you capture yourself. Make the route, driving, and finish easy to judge in one continuous clip.',
        '[
            "Use only gameplay footage you personally record in GTA V or GTA Online.",
            "Submit one continuous 15-30 second clip with no edits or cuts.",
            "Show a moving getaway during sunset lighting and keep the vehicle visible.",
            "Finish the clip in control; crashes, a destroyed vehicle, and death do not qualify.",
            "Do not upload GTA VI trailers, promotional footage, leaks, cutscenes, mods, or another creator''s clip.",
            "Do not use cheats, exploits, bots, or manipulated evidence."
        ]'::jsonb,
        'published',
        'public',
        date_trunc('day', now()),
        LEAST(
            date_trunc('day', now()) + interval '14 days',
            timestamptz '2026-11-19T00:00:00Z'
        ),
        NULL
    WHERE now() < timestamptz '2026-11-19T00:00:00Z'
    ON CONFLICT (slug) DO NOTHING
    RETURNING id
)
INSERT INTO audit_events (
    actor_id,
    action,
    entity_type,
    entity_id,
    metadata
)
SELECT
    NULL,
    'challenge.seeded',
    'challenge',
    id::text,
    '{
        "source": "000004_prelaunch_sample_challenge.sql",
        "game": "Grand Theft Auto V / GTA Online",
        "temporary": true
    }'::jsonb
FROM inserted_challenge;
