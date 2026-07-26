# HeatCheck Flutter Product and Frontend Guide

This document is the product, experience, and integration brief for the
HeatCheck Flutter application. Read it together with
[`api/openapi.yaml`](../api/openapi.yaml), which is the authoritative API
contract.

- This guide defines product intent, launch scope, navigation, behavior, state
  handling, and visual direction.
- The OpenAPI contract defines routes, payloads, validation, response schemas,
  security, and status codes.
- The policy API defines the current legal and community text. Do not ship
  policy copy as a hard-coded source of truth.

Production API base URL:

```text
https://heatcheck.dogi.watch
```

Production OpenAPI URL:

```text
https://heatcheck.dogi.watch/openapi.yaml
```

## 1. Product in One Page

HeatCheck is an unofficial, video-first gameplay challenge community. A
challenge gives players a creative objective, players submit a short proof
clip, automated systems check whether the entry is safe and appears to satisfy
the challenge, and the community scores the style of published entries.

The product is not a general social network and not a clip-hosting service. The
focus is a repeatable competitive loop:

1. Open today's challenge.
2. Understand the objective and rules.
3. Watch published attempts and inspect the leaderboard.
4. Record or select a 15-30 second proof clip.
5. Upload and submit the clip.
6. Wait for safety, media, and challenge verification.
7. Receive a published entry or a clear private outcome.
8. Collect style votes, improve rank and streak statistics, and share the
   server-rendered result card.

The original concept was built around the excitement and creativity of the
GTA VI player community, with a path to support other games later. HeatCheck is
not affiliated with, endorsed by, or sponsored by Rockstar Games or Take-Two
Interactive. That boundary must be visible in product decisions, not only in a
legal footer.

### Product promise

Give players one clear reason to create today, a trustworthy way to prove it,
and a result worth sharing.

### Launch audience

- Adults aged 18 or older.
- Players who enjoy expressive, skillful, funny, or unusual gameplay.
- Viewers who want a fast daily ritual without submitting every day.
- Competitive players who care about rank, streaks, and community recognition.

### What makes HeatCheck distinct

- Challenge-led rather than feed-led.
- Short proof clips rather than long-form video.
- Automated checks plus human review rather than unmoderated posting.
- Style voting rather than a generic like count.
- A daily cadence and visible submission allowance.
- Share cards generated from authoritative backend results.

## 2. Launch Product and Business Model

The implemented launch model supersedes the older free-with-watermark concept.
Do not build the old pricing model into the Flutter app.

### Free access

People may use HeatCheck without Pro to:

- View current public challenges.
- View the daily challenge.
- Watch public, verified, approved submissions.
- View challenge leaderboards.
- View public profile statistics.
- Register, manage their account, and read policies.
- Vote, report, and block after meeting the authentication, verification, and
  policy requirements enforced by the API.

Free access is meaningful discovery and community participation. It does not
include proof-clip submission.

### Pro access

An active RevenueCat entitlement whose ID is `pro` is required to initialize a
clip upload and submit an entry.

Default production allowances are:

- 1 submission reservation per UTC day.
- 30 submission reservations per UTC calendar month.
- A platform-wide ceiling of 100 new reservations per UTC day.

These values are server configuration, not permanent product constants. Always
render the `usage` object returned by `GET /v1/me/subscription`. Never calculate
remaining usage from locally counted submissions.

An upload reservation is consumed when a submission succeeds. An abandoned
reservation is eventually released. The global ceiling exists to bound media
processing and AI cost; it is not an indication that a user's subscription is
invalid.

### Pricing presentation

- Fetch product title, billing period, trial, eligibility, and localized price
  from the RevenueCat/store SDK.
- Never hard-code a price, currency, trial, discount, or renewal statement.
- The backend is authoritative for access; the store SDK is authoritative for
  purchasable product presentation.
- A store purchase is not enough to unlock submission in the UI. Call
  `POST /v1/me/subscription/sync` and require the returned
  `subscription.active` to be `true`.

### Future, not launch scope

The concept document discusses creator leagues, friend groups, head-to-head
battles, creator landing pages, custom share cards, Discord integration,
notifications, and expansion to other games. The current public API does not
implement those products. They may inform future architecture, but they must
not appear as functional launch navigation or mocked production UI.

## 3. Product Principles

### The challenge is the organizing object

Every discovery, submission, voting, ranking, and sharing journey should retain
the challenge context. Do not turn HeatCheck into an endless, context-free
video feed.

### Proof must feel trustworthy

Show the distinction between upload, processing, verification, moderation, and
publication. Never call an entry "live" until:

```text
verification_status == passed
AND
moderation_status == approved
```

### Pending is normal, not punishment

Automated checks may be inconclusive or temporarily unavailable. Use neutral
language such as "Checking your clip" and "A moderator will review this entry."
Do not imply cheating, policy violation, or guilt from `manual_review` or
`pending`.

### Server state wins

Ranks, scores, subscription access, allowances, policy versions, account
status, and timestamps come from the server. Optimistic UI is appropriate only
when it can be reconciled safely.

### Safety controls stay reachable

Report and block actions belong in the overflow menu on submissions and public
profiles. They must not be hidden behind multiple unrelated screens.

### No dark patterns

Do not disguise Pro requirements, global capacity limits, automatic renewal, or
account deletion consequences. Restore purchases and subscription management
must be easy to find.

## 4. Launch Information Architecture

Use a four-destination authenticated and unauthenticated app shell:

| Destination | Purpose | Primary API |
| --- | --- | --- |
| Today | Current challenge, time window, entry point, top attempts | `GET /v1/challenges/daily` |
| Challenges | Browse current and past public challenges | `GET /v1/challenges` |
| Rankings | Leaderboard and published attempts for the selected challenge | `GET /v1/challenges/{id}/leaderboard` |
| Profile | Public statistics, subscription, settings, safety, and account tools | `GET /v1/users/{id}`, `GET /v1/me` |

Use a platform-standard bottom navigation bar on phones. On larger widths, move
the same destinations to a navigation rail without changing their order or
meaning.

Submission is a contextual command on an active challenge. Do not add a global
center upload button that loses the challenge relationship.

### Anonymous entry

The first screen is Today, not a marketing page and not a forced login wall.
Anonymous users can understand the product by viewing the live challenge,
published attempts, leaderboard, and profiles. Ask them to authenticate at the
moment an action requires it.

### Route outline

```text
/
  /today
  /challenges
  /challenges/:challengeId
  /challenges/:challengeId/rankings
  /submissions/:submissionId
  /users/:userId
  /auth/login
  /auth/register
  /auth/verify-email?token=...
  /auth/forgot-password
  /auth/reset-password?token=...
  /policies/:kind
  /pro
  /submit/:challengeId
  /submit/:challengeId/status/:submissionId
  /settings
  /settings/subscription
  /settings/sessions
  /settings/export
  /settings/delete-account
  /safety/report
  /legal/copyright
  /legal/copyright/:noticeId/counter
  /moderation/:actionId/appeal
```

Moderator and administrator routes should live in a separate role-gated
workspace. Do not mix operational queues into the consumer navigation.

## 5. Screen Specifications

### Today

Purpose: answer "What is today's challenge, and can I take part?"

Required content:

- HeatCheck identity in the app bar.
- Challenge title, concise description, and active/closed state.
- Start/end time rendered in local time, based on server UTC timestamps.
- Human-readable rules.
- Primary action: `Submit attempt`, `Get Pro`, `Verify email`, `Accept updated
  policies`, or `Challenge closed`, depending on the current blocker.
- Remaining daily and monthly allowance for authenticated Pro users.
- Top published attempts using real thumbnails and clips.
- Link to the complete leaderboard.

If `GET /v1/challenges/daily` returns `404`, show a calm no-active-challenge
state and offer the Challenges destination. Do not present this as a network
failure.

### Challenges

Display public challenges in time-aware groups:

- Active now.
- Upcoming, when returned by the API.
- Closed/recent.

Use stable, compact rows or media-backed list items. Each item needs title,
short description, status, and date window. Pagination uses `limit` and
`offset`; append results without moving already visible content.

There is no public text-search or filtering API at launch. Do not implement a
search field that only searches the currently loaded page while implying it
searches the full catalog.

### Challenge detail

Required sections:

- Title, description, rules, status, and time window.
- Contextual submission action.
- Published attempts.
- Leaderboard preview.
- Report challenge action for authenticated eligible users.

Keep the rules visible before clip selection. A submission is an assertion that
the player followed those rules.

`Challenge.rules` is currently arbitrary JSON. See the integration blockers in
section 16 before implementing a rule-specific renderer.

### Submission detail

Required content:

- Playable clip and thumbnail fallback.
- Challenge context.
- Creator handle linking to public profile.
- Caption.
- Style score and vote count.
- A 1-5 style voting control.
- Share action using the backend share card.
- Report and block actions in an overflow menu.
- Status panel for the owner when the entry is not public.

Only one video should actively play at a time. Default to sound off when a clip
starts automatically and expose a clear sound control. Preserve captions and
controls under large text settings.

The API currently returns aggregate vote data but not the authenticated user's
existing score. Do not display a persisted selected vote unless the app knows
it from the current session. See section 16.

### Rankings

Use the leaderboard endpoint rather than deriving rank from a submission list.
Each row contains:

- Server-provided `rank`.
- Thumbnail.
- Handle.
- Style score.
- Vote count.
- A direct route to the submission.

The first three entries may receive stronger visual emphasis, but all ranks
must remain legible and structurally identical. Never recalculate rank on the
device.

### Public profile

The current profile contains:

- Handle and display name.
- Member-since date.
- Submission count.
- Challenge wins.
- Current streak.
- Best streak.
- Average score.
- Report and block controls when viewing another user.

There is no user-submission listing endpoint and no avatar field. Do not create
an empty "Posts" tab, fake avatar upload, follow counts, biographies, or social
links.

### Own profile and settings

Combine the public statistics with:

- Email verification state.
- Pro status and usage.
- Manage subscription and restore/sync purchase actions.
- Active sessions.
- Policy documents.
- Account export.
- Pending deletion status/cancellation.
- Delete account.
- Copyright notice entry point.
- Log out.

Role-gate moderator/admin workspace entry using `user.role`.

### Authentication

Registration fields are email, password, handle, display name, date of birth,
and current required policy acceptances. Validate for usability on device, but
always display server validation results.

The minimum launch age is 18. The server remains authoritative. Do not ask only
for a checkbox that says the user is over 18; collect `date_of_birth`.

After registration:

- The user already has an access and refresh token.
- Show the verification requirement and resend action.
- Allow browsing.
- Gate active actions until `email_verified_at` is non-null.

Forgot-password always receives `202`, whether the address exists or not. Use
the same success message in all cases.

### Pro/paywall

The paywall should explain the actual launch benefit:

- Submit proof clips to daily challenges.
- Receive automated verification and community ranking.
- Current daily and monthly allowances.

It must also include:

- Store-provided localized price and billing period.
- Purchase command.
- Restore purchases command.
- Terms and privacy links.
- Automatic-renewal copy required by the stores.
- A close/back action when the paywall is not a mandatory route.

Do not advertise creator tools, unlimited uploads, no-watermark benefits, or
other unsupported concept features.

### Submission status

The owner status screen is a durable destination, not a transient toast.

| Verification | Moderation | User-facing state |
| --- | --- | --- |
| `pending` | `pending` | Checking media, safety, and challenge fit |
| `manual_review` | `pending` | Awaiting moderator review |
| `passed` | `pending` | Challenge verified; finishing safety review |
| `passed` | `approved` | Published |
| `failed` | any non-public state | Could not verify the challenge requirements |
| any | `rejected` | Not approved for publication |
| any | `removed` | Removed after publication |

Do not expose raw AI/provider output as a definitive accusation. Treat
`verification_details` as diagnostic data whose user-facing presentation must
be explicitly designed and allowlisted.

Poll `GET /v1/submissions/{id}` while processing. Use increasing intervals,
stop when the app is backgrounded, refresh immediately on resume, and stop on a
terminal state. The worker is asynchronous; do not impose a fake completion
timer.

## 6. Session and App Bootstrap

### Token storage

- Keep the access token in memory.
- Store the refresh token in platform-secure storage.
- Do not store either token in ordinary preferences, logs, analytics, crash
  metadata, URLs, or RevenueCat attributes.
- Store token expiry timestamps returned by the API.

Default server lifetimes are 15 minutes for access tokens and 30 days for
refresh tokens, but the app must use the returned timestamps.

### Refresh behavior

Refresh tokens rotate on every successful refresh. Implement one
process-wide refresh operation:

1. A request receives `401`, or the access token is close to expiry.
2. If a refresh is already running, wait for it.
3. Call `POST /v1/auth/refresh` once.
4. Atomically replace the stored refresh token and in-memory access token.
5. Retry the original request once.
6. If refresh fails, clear the local session and return to an anonymous state.

Parallel refresh requests can revoke a valid token family through replay
protection. Do not let each repository refresh independently.

### Authenticated bootstrap

After restoring a session:

1. Call `GET /v1/me`.
2. Configure the RevenueCat Flutter SDK with `user.id` as the exact,
   non-anonymous App User ID.
3. Call `GET /v1/me/subscription`.
4. Fetch current policies when policy UI or a missing acceptance is relevant.
5. Resolve route guards from account, verification, policy, subscription, and
   usage state.

When signed in, include the bearer token even on endpoints that permit
anonymous access. Challenge submission lists are blocking-aware for
authenticated users.

### Route guard priority

For an active action, resolve blockers in this order:

1. No session: authenticate.
2. Suspended/deleted account: show the account state.
3. Deletion pending: show deletion status and cancellation path.
4. Missing required policies: show the acceptance gate.
5. Unverified email: show verification gate.
6. Submission action without active Pro: show paywall.
7. Submission allowance exhausted: show reset time.
8. Continue to the requested action.

Return the user to the originally requested route after a blocker is resolved.

## 7. Policies, Age, and Trademark Boundaries

### Policies

- Fetch `GET /v1/policies` before registration.
- Render `Policy.content` as sanitized Markdown.
- Send the exact `kind` and `version` accepted by the user.
- After login, use `missing_policy_acceptances` from `GET /v1/me`.
- If any active request returns `428`, fetch current policies and present a
  blocking acceptance flow.
- Do not silently accept policies, preselect consent boxes, or infer acceptance
  from app use.

Community guidelines and copyright policy are public product surfaces. Terms of
Use and Privacy Policy should also be published as versioned policy documents
when final legal text is available.

### Age

- Use an accessible date-of-birth picker with manual-entry support.
- Explain the minimum age before submission.
- Never expose another user's date of birth.
- Treat underage and child-safety reports as sensitive safety actions.

### Rockstar and GTA boundaries

Use HeatCheck's original identity. Do not:

- Use Rockstar, Take-Two, GTA, or GTA VI logos as HeatCheck branding.
- Copy official fonts, interface chrome, map shapes, loading screens, character
  art, cover compositions, or color treatments closely enough to imply an
  official product.
- Describe HeatCheck as an official companion, partner, or endorsed service.
- Use leaked, embargoed, or access-control-bypassed material in editorial or
  promotional assets.
- Make GTA/Rockstar marks more visually prominent than the HeatCheck brand.

Game names may be used nominatively when needed to identify the game a
challenge concerns. Put the unofficial-community disclosure in About, store
listing/legal copy, and appropriate web surfaces. Do not repeat a legal
disclaimer over every gameplay clip.

User-generated clips remain subject to the community guidelines and copyright
process. The app should make "I created this or have permission to share it"
clear before final submission.

## 8. RevenueCat Integration

RevenueCat handles store transactions. The HeatCheck API handles product
access.

### Identity rule

After HeatCheck login, configure or log in to RevenueCat using the exact UUID
returned as `user.id`. It must match `data.app_user_id` returned by
`GET /v1/me/subscription`.

Do not:

- Create a separate frontend customer identifier.
- Use email, handle, or device ID as the RevenueCat App User ID.
- Purchase anonymously and assume the backend can discover the entitlement.
- Ship the RevenueCat secret API key or webhook secret in the app.

Only platform-specific public RevenueCat SDK keys belong in Flutter build
configuration.

### Purchase/restore sequence

1. Fetch offerings through the RevenueCat SDK.
2. Present store product metadata.
3. Purchase or restore through the SDK.
4. Call `POST /v1/me/subscription/sync`.
5. Replace local subscription and usage state with the response.
6. Unlock submission only when `subscription.active == true`.

If the store succeeds but sync returns `502`, keep the receipt/customer state
and offer a retry. Do not ask the user to buy again.

### Subscription states

- `active`: participation unlocked, subject to usage limits.
- `canceled`: may remain active until `current_period_end`; trust `active`.
- `billing_issue`: show a billing-management path and trust `active`.
- `expired` or `inactive`: browsing remains available; submission is locked.

For account deletion, show `management_url` before confirmation and state that
deleting HeatCheck does not cancel App Store or Play Store renewal.

## 9. Clip Selection, Upload, and Submission

### Local preflight

Before reserving usage, validate as much as the platform can reliably provide:

- MIME type is `video/mp4`, `video/quicktime`, or `video/webm`.
- Exact file size is available and no more than the currently configured
  100 MiB launch maximum.
- Duration appears to be 15-30 seconds.
- The file remains readable while the upload runs.

The backend scans, inspects, and transcodes independently. Local validation
improves feedback but never overrides the server.

### Exact workflow

1. Resolve all route blockers and refresh subscription usage.
2. Let the user choose/record and preview the clip.
3. Collect an optional caption of at most 280 characters.
4. Show challenge rules and an ownership/permission acknowledgement.
5. Call `POST /v1/uploads` with exact `content_type` and `size_bytes`.
6. Persist the returned upload instruction and challenge/caption draft locally.
7. Send the raw file directly to `data.url` using `data.method` and every
   returned header.
8. Do not attach the HeatCheck bearer token to the object-storage request.
9. Require a successful object-storage response.
10. Call `POST /v1/uploads/{uploadID}/complete`.
11. Call `POST /v1/challenges/{challengeID}/submissions` with
    `media_upload_id` and `caption`.
12. Clear the local upload instruction only after submission creation succeeds.
13. Route to the durable submission status screen.

The signed upload URL normally expires after 15 minutes. Completing it extends
the reservation, normally for 24 hours, so submission creation can finish.

### Progress and recovery

Use explicit phases:

```text
Preparing -> Uploading -> Confirming -> Creating entry -> Checking -> Published
```

- Display byte progress during the direct `PUT`.
- Keep the screen awake where platform policy permits.
- A user cancellation stops transfer but does not immediately release the
  server reservation.
- On network loss, preserve the draft and upload instruction.
- Retry a direct upload only while the signed URL is valid.
- Retry completion when the object upload succeeded but API confirmation
  failed.
- Retry submission creation when completion succeeded but entry creation
  failed.
- Treat `409` on submission creation as a possible already-created/conflicting
  state and reconcile rather than creating another reservation.

The API has no upload lookup or explicit reservation-cancel endpoint at launch.
Recovery therefore depends on locally persisting the instruction, and abandoned
capacity is released by backend expiry.

### Media URLs

`clip_url`, `thumbnail_url`, export URLs, and upload URLs are short-lived signed
URLs. Never use them as database keys or permanent cache identity. Key caches
by HeatCheck resource ID and replace signed URLs whenever a fresh API response
arrives.

## 10. Voting, Sharing, and Social Safety

### Style votes

Votes are integer scores from 1 to 5. Use a stable five-position control with
clear selected, focus, disabled, and submitted states.

- Do not allow voting on one's own submission; reconcile `403`.
- `PUT` is an upsert, so a current-session vote may be changed.
- Use the returned submission as the new aggregate source.
- Avoid changing the displayed average before the API succeeds.
- Provide an accessible label such as "Style score 4 out of 5" for every
  choice.

### Sharing

The backend renders the authoritative 1080x1920 PNG:

```text
GET /v1/submissions/{submissionID}/share-card.png
```

Download and share this image through the platform share sheet. Do not recreate
rank, score, handle, or streak graphics in Flutter. A share action should also
include the canonical submission link when deep linking is available.

### Blocking

Blocking is a user safety action, not a cosmetic local filter. Call the API and
remove affected content from the current client state. Ask for confirmation,
but do not add unnecessary friction.

### Reports

Use the API reason enum exactly. Present human-readable labels and request
optional details, especially for `other`.

For copyright complaints, route rights holders to the copyright notice flow
instead of treating a normal content report as a substitute for the legal
process.

Never reveal a report's priority or internal moderation handling to other
users.

## 11. Account, Privacy, and Legal Workflows

### Sessions

Display device/user-agent, approximate IP when provided, created time, last
used time, and expiry. Support revoking one session and all sessions.

The current API does not identify "this device" explicitly. Do not guess based
only on a user-agent string.

### Account export

1. Call `POST /v1/me/exports`.
2. Route to an export status screen.
3. Poll `GET /v1/me/exports/{id}` while pending/processing.
4. Enable download only for `ready`.
5. Treat the returned download URL as short-lived.
6. Explain failed and expired states with a retry path.

### Account deletion

Before requesting deletion:

- Fetch subscription state.
- Show the store `management_url` when present.
- Explain that deletion does not cancel store renewal.
- Require the current password.
- Show the server-provided `execute_after` timestamp after `202`.
- Provide `DELETE /v1/me/deletion` during the grace period.

Do not immediately erase the secure refresh token after the server schedules
deletion if the product is intentionally allowing the user to cancel from the
same session. Follow actual API session behavior and test this flow end to end.

### Copyright

The notice form is public and must collect every OpenAPI-required declaration.
Do not echo sensitive claimant data after submission; show only the returned
receipt identifier and status.

The counter-notice route is authenticated and limited to the affected uploader.
Its declarations have legal consequences. Do not pre-check declaration boxes
or replace the signature field with a casual confirmation button.

### Appeals

Appeals are tied to a moderation action ID. The current consumer API creates an
appeal but does not list a user's moderation actions or appeals. Only present
the form when the app has a valid action ID from a trusted notification or
moderation surface.

## 12. Visual and Interaction Design

### Desired character

HeatCheck should feel immediate, competitive, credible, and native to gaming
culture without looking like a game publisher imitation. The content supplies
the energy; the interface supplies structure.

### Composition

- Use real submission thumbnails and video as the primary visual material.
- Prefer full-width media and unframed page sections.
- Keep compact metadata close to the content it describes.
- Use cards only for repeated objects or truly contained tools.
- Never place cards inside cards.
- Keep corner radius at 8 px or less unless a platform control requires
  otherwise.
- Avoid oversized marketing heroes inside the app.
- Avoid glass panels, decorative gradient orbs, bokeh, and ornamental 3D
  objects.
- Keep the next content section visible below the first viewport.

### Color direction

Use a near-neutral foundation, not a single-hue theme:

- Near-black for immersive media surfaces.
- Clean off-white for primary light text.
- A hot coral/red-orange brand accent for primary HeatCheck actions.
- Acid-lime sparingly for streaks, verified success, and competitive moments.
- Cyan for informational states and links.
- Amber for waiting/limits.
- Red reserved for destructive or policy failure states.

Final colors must pass WCAG contrast for their actual text size and background.
Do not rely on color alone for subscription, moderation, rank, or verification
states.

### Typography

- Use a licensed, original sans-serif family with a sturdy display weight and a
  highly readable text weight.
- Do not imitate GTA/Rockstar display lettering.
- Use approximately 28 px for page titles, 22 px for major headings, 18 px for
  section headings, 15-16 px for body, and 12-13 px for metadata.
- Letter spacing is 0; do not use negative tracking.
- Respect system text scaling and test the largest supported accessibility
  size.
- Reserve display type for page identity, not compact settings panels.

### Controls

- Use icons for familiar commands such as back, close, share, overflow, mute,
  play, pause, and refresh.
- Pair unfamiliar icons with labels or tooltips.
- Use segmented controls for challenge/leaderboard modes.
- Use a five-position control for votes.
- Use toggles or checkboxes only for binary settings or explicit declarations.
- Use a progress bar for byte upload and a spinner only for short,
  indeterminate work.
- Keep touch targets at least 48 logical pixels.

### Motion

- Motion should communicate navigation, state change, upload progress, and rank
  movement.
- Keep routine transitions around 180-240 ms.
- Do not autoplay multiple moving previews.
- Honor reduced-motion preferences.
- Never use celebratory motion for a state that has not been confirmed by the
  server.

### Accessibility

- Support screen readers, switch access, large text, and landscape.
- Provide semantic labels for votes, scores, playback, status, and destructive
  actions.
- Ensure focus order follows visual order.
- Provide text equivalents for every color/status icon.
- Keep video controls visible against light and dark footage.
- Do not block the UI indefinitely on an auto-playing clip.

## 13. Flutter Architecture

Use feature-first boundaries so product areas can evolve without a single
global service layer:

```text
lib/
  app/
    bootstrap/
    routing/
    theme/
  core/
    api/
    auth/
    errors/
    persistence/
    telemetry/
    widgets/
  features/
    policies/
    authentication/
    challenges/
    submissions/
    upload/
    billing/
    profiles/
    safety/
    account/
    administration/
```

Each feature should separate:

- API data transfer models.
- Domain models/state.
- Repository orchestration.
- Presentation state.
- Screens and reusable widgets.

Use one established state-management system throughout the app. Use one router
that supports guarded routes and incoming links. Use one HTTP stack with
central authentication, request IDs, timeout policy, and structured error
decoding.

### API client

- Generate or strongly type models from the checked-in OpenAPI contract.
- Pin the contract used by each app release.
- Preserve unknown enum values as an explicit `unknown` state so a backend
  addition does not crash an older app.
- Do not model arbitrary JSON as an unsafe cast spread through widgets.
- Keep the production base URL in build configuration, not editable user
  preferences.
- Send `Accept: application/json` to the API.
- Preserve the object-storage upload as a separate unauthenticated HTTP client.

### State shape

Every remote screen should represent:

```text
initial
loading
content
empty
refreshing-with-content
recoverable failure
terminal/not-found
```

Refreshing should retain content. Empty is not an error. A cached anonymous
discovery view may be shown read-only with a stale indicator, but purchases,
votes, reports, policy acceptance, deletion, and submissions must not be
blindly queued offline.

### Local persistence

Appropriate:

- Rotating refresh token in secure storage.
- Non-sensitive preferences.
- Recently fetched discovery content with expiry metadata.
- In-progress upload instruction and draft, encrypted/protected as appropriate.
- Current-session vote selection as a non-authoritative convenience.

Inappropriate:

- Access/refresh tokens in ordinary preferences.
- Permanent signed media URLs.
- Claimant/counter-notice form data after submission.
- Raw AI analysis.
- Store secrets or backend credentials.

### Telemetry

Track product events without content or secrets:

- Challenge viewed.
- Paywall viewed.
- Purchase/restore outcome category.
- Upload phase and failure category.
- Submission created.
- Processing terminal state.
- Vote success.
- Share initiated.

Never send passwords, tokens, dates of birth, full IP addresses, private policy
form values, copyright claimant data, signed URLs, raw clips, or report details
to analytics.

## 14. API Integration Rules

### Envelopes

Most successful JSON resources are under `data`. Paginated responses also have:

```json
{
  "pagination": {
    "limit": 20,
    "offset": 0
  }
}
```

There is no total count in the pagination object. Stop paging when a page
contains fewer than `limit` items.

### Errors

Errors use:

```json
{
  "error": {
    "code": "validation_failed",
    "message": "The request failed validation.",
    "details": {
      "caption": "must contain at most 280 characters"
    }
  }
}
```

Prefer specific `error.code` behavior over matching English messages.

| HTTP | Typical frontend behavior |
| --- | --- |
| `400` | Invalid client request; log sanitized diagnostics and show a safe error |
| `401` | Refresh once, retry once, otherwise clear the session |
| `402` | Refresh subscription, then show Pro when still inactive |
| `403` | Show account, role, ownership, or action restriction |
| `404` | Show resource unavailable; daily challenge uses an intentional empty state |
| `409` | Reconcile duplicate or invalid concurrent state |
| `422` | Attach field errors or explain the invalid state |
| `428` | Fetch and present required policy acceptances |
| `429` | Show a retry/reset time; distinguish rate limit from submission allowance |
| `500-599` | Retain user work, offer retry, and avoid duplicate mutation |

For `submission_limit_reached`, `details` can include `period`, `limit`, and
`resets_at`. Render `resets_at`, not a locally assumed midnight.

### Time

- All API timestamps are RFC 3339 UTC.
- Parse into timezone-aware values.
- Display in the user's locale unless a label explicitly says UTC.
- Use server reset timestamps for countdowns.
- Recompute countdown labels when the app resumes.
- Never decide whether a challenge is open using only a stale local timer.

### Mutation rules

- Disable duplicate taps while a mutation is in flight.
- Use `PUT` vote and block operations as defined, not `POST`.
- Treat `204` as success with no JSON body.
- Retry idempotent `GET`, `PUT`, and `DELETE` conservatively.
- Do not automatically retry account creation, upload reservation, submission,
  report, appeal, export, or legal notice without reconciling the response.

### Request IDs and support

The server returns `X-Request-ID`. Capture it in sanitized error diagnostics and
make it available to support tooling. Never expose internal stack traces.

## 15. Screen-to-Endpoint Map

| Screen/action | Endpoint |
| --- | --- |
| Today | `GET /v1/challenges/daily` |
| Challenge list | `GET /v1/challenges?limit=&offset=` |
| Challenge detail | `GET /v1/challenges/{challengeID}` |
| Published attempts | `GET /v1/challenges/{challengeID}/submissions` |
| Leaderboard | `GET /v1/challenges/{challengeID}/leaderboard` |
| Submission detail/status | `GET /v1/submissions/{submissionID}` |
| Share card | `GET /v1/submissions/{submissionID}/share-card.png` |
| Vote | `PUT /v1/submissions/{submissionID}/vote` |
| Public profile | `GET /v1/users/{userID}` |
| Register/login | `POST /v1/auth/register`, `POST /v1/auth/login` |
| Refresh/logout | `POST /v1/auth/refresh`, `POST /v1/auth/logout` |
| Verify/reset | `POST /v1/auth/verify-email`, `POST /v1/auth/reset-password` |
| Current account | `GET /v1/me` |
| Policies | `GET /v1/policies`, `POST /v1/me/policy-acceptances` |
| Pro status/usage | `GET /v1/me/subscription` |
| Purchase reconciliation | `POST /v1/me/subscription/sync` |
| Upload | `POST /v1/uploads`, direct signed `PUT`, completion endpoint |
| Create entry | `POST /v1/challenges/{challengeID}/submissions` |
| Block/unblock | `PUT`/`DELETE /v1/users/{userID}/block` |
| Report | `POST /v1/reports` |
| Sessions | `GET`/`DELETE /v1/me/sessions` |
| Export | `POST /v1/me/exports`, `GET /v1/me/exports/{exportID}` |
| Delete account | `DELETE /v1/me`, `GET`/`DELETE /v1/me/deletion` |
| Copyright notice | `POST /v1/copyright/notices` |
| Copyright counter | `POST /v1/copyright/notices/{noticeID}/counter` |
| Appeal | `POST /v1/moderation/actions/{actionID}/appeals` |

The RevenueCat webhook is server-to-server. The Flutter app must never call:

```text
POST /v1/billing/revenuecat/webhook
```

## 16. Known Integration Decisions and API Boundaries

Resolve these before calling the Flutter application release-ready.

### Universal/app links

Verification and reset emails point to:

```text
https://heatcheck.dogi.watch/auth/verify-email?token=...
https://heatcheck.dogi.watch/auth/reset-password?token=...
```

The Flutter router must accept both routes and submit the token to the matching
API endpoint. Android App Links and iOS Universal Links also require association
files on `heatcheck.dogi.watch`, which cannot be finalized until Android
package/signing and Apple Team ID/bundle ID are known. Provide a useful HTTPS
web fallback; without link association or fallback, these URLs open a backend
404 in a browser.

### RevenueCat applications

The Flutter app does not yet exist, so its App Store/Play Store applications and
RevenueCat public SDK keys cannot be finalized. The backend currently validates
one configured `REVENUECAT_APP_ID`. Launching both iOS and Android with separate
RevenueCat app IDs requires a corresponding backend configuration change or a
confirmed RevenueCat setup that produces the configured ID.

### Challenge rules

`Challenge.rules` is valid JSON but has no enforced public schema. Before
building rich rule widgets, define and validate a versioned shape in the API,
for example a version plus ordered human-readable items. Until then, render
only explicitly supported string/list forms and fail gracefully rather than
showing raw JSON to users.

### My entries

There is no endpoint to list the authenticated user's submissions. The app can
persist IDs it creates and poll those entries, but it cannot reconstruct a
complete activity history after reinstall or on another device. Do not promise
a My Entries screen until the API supports it.

### Existing vote

Submission responses expose aggregate score and vote count, not the current
user's vote. The app cannot restore the selected 1-5 value on another device.
Do not imply a known existing selection until the API adds it.

### Profiles and social graph

There is no profile editing, avatar, bio, follow, friend, message, notification,
or user-search API. Do not create launch UI for these features.

### Appeals and copyright inbox

Consumer endpoints create appeals and counter-notices, but do not provide a
general consumer inbox of moderation actions, notices, or appeal status. Entry
points need trusted IDs from an email/deep link or a future account endpoint.

## 17. Moderator and Administrator Experience

Moderator/admin UI should be a dense, quiet operational workspace:

- Submission verification queue.
- Report queue and dismissal.
- Moderation action creation.
- Appeal review.
- Copyright notice review.
- Audit event inspection.
- Challenge creation.
- Policy publication for admins only.

Requirements:

- Require `moderator` or `admin` role before navigation and on every request.
- Show source content, target, current state, timestamps, and prior context
  before an action.
- Require reason fields; make destructive actions visually explicit.
- Never use optimistic success for moderation, copyright, or policy mutations.
- Preserve exact API enums.
- Treat audit data as append-only and non-editable.
- Separate public claimant receipts from sensitive moderator-only claimant
  records.

The OpenAPI Administration tag is the exact route and payload reference.

## 18. Definition of Done

### Functional

- Anonymous discovery works without authentication.
- Registration fetches and submits exact current policy versions.
- Email verification and password-reset links open the correct native route and
  have an HTTPS fallback.
- Rotating refresh tokens survive app restart and concurrent `401` responses.
- RevenueCat always uses the authenticated HeatCheck UUID.
- Purchase and restore reconcile through the backend before unlock.
- Upload uses exact byte count, MIME type, signed method, and signed headers.
- Upload recovery does not create unnecessary duplicate reservations.
- Submission status distinguishes verification, moderation, and publication.
- Published media, vote, rank, profile stats, and share card use server data.
- Report, block, export, session, copyright, and deletion flows are wired.
- The app handles all documented status codes without losing user-entered work.

### Visual

- HeatCheck is the first-viewport brand signal.
- Real gameplay content is visible in primary discovery and submission views.
- No Rockstar/GTA trade dress is copied.
- No card nesting, decorative orb backgrounds, or oversized in-app heroes.
- Text fits at supported widths and accessibility sizes.
- Touch targets, contrast, focus order, and screen-reader labels are verified.
- Loading, empty, stale, error, pending, and terminal states are all designed.

### Test matrix

Test at minimum:

- Fresh anonymous launch.
- Register underage, duplicate email/handle, and invalid policy version.
- Login, refresh rotation, concurrent `401`, revoked session, and logout.
- Unverified email, missing updated policy, suspended account, and pending
  deletion route guards.
- Free user, active Pro, canceled-but-active, billing issue, expired Pro,
  purchase success/sync failure, and restore.
- Daily, monthly, and global submission limit responses.
- Expired signed upload, interrupted upload, size mismatch, unsupported media,
  completion retry, and duplicate submission.
- Every verification/moderation state pair used by the UI.
- Vote on own entry, changed vote, rate limit, block, report, and content
  disappearing after block.
- Empty daily challenge, empty leaderboard, pagination, deleted content, and
  expired signed media URL.
- Export success/failure/expiry and deletion cancellation.
- Deep links from cold start, warm app, logged out, and logged in.
- Phone/tablet, portrait/landscape, largest text size, screen reader, reduced
  motion, slow network, offline transition, and app background/resume.

## 19. Source of Truth Checklist

When documents disagree, use this order:

1. Current backend behavior and deployed database state.
2. `api/openapi.yaml` for the public contract.
3. This guide for launch product and frontend behavior.
4. Current policy documents returned by `/v1/policies` for policy text.
5. The original concept PDF for long-term intent only.

Do not silently compensate for a contract gap in Flutter. Record it, resolve it
with the backend, update OpenAPI, and then update this guide.
