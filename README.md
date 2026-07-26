# HeatCheck Backend

Go API and asynchronous worker for HeatCheck's gameplay challenges, proof clips,
OpenAI-assisted verification, voting, moderation, and account/legal workflows.

## Documentation

- [Flutter product and frontend guide](docs/FRONTEND_GUIDE.md)
- [OpenAPI 3.1 contract](api/openapi.yaml)
- Production API: `https://heatcheck.dogi.watch`
- Production OpenAPI: `https://heatcheck.dogi.watch/openapi.yaml`

## Included

- Argon2id email/password registration, verification, password reset, and an 18+ age gate by default
- Short-lived JWT access tokens, replay-resistant refresh-token families, and device-session revocation
- Immutable, versioned policy documents and per-user acceptance records
- Scheduled public and unlisted challenges
- Direct, signed uploads to S3-compatible object storage
- RevenueCat-backed Pro subscriptions with server-authoritative entitlements
- Paid-only clip participation with transactional daily and monthly allowances
- One submission per user per challenge
- Durable PostgreSQL jobs with retry, stale-lock recovery, and worker heartbeats
- ClamAV malware scanning and `ffprobe` duration/codec/dimension validation
- H.264/AAC MP4 transcoding, thumbnails, and 15-30 second enforcement
- OpenAI image moderation and strict-schema challenge verification from sampled frames
- Low-confidence, provider-failure, and ambiguous results routed to manual review
- Moderation-gated publication and audited automated decisions
- Style votes with transactional score aggregation
- Blocking-aware feeds and voting
- User reports and severe AI flags with encrypted urgent safety-alert delivery
- Audited moderator actions, suspensions, and appeals
- Copyright notices, status emails, legal alerts, enforced state transitions, and uploader counter-notices
- AES-GCM encryption for claimant, counter-notice, and queued-email data
- Account ZIP exports, deletion grace periods, PII anonymization, and media retention cleanup
- Public leaderboards, profile streak/ranking statistics, and vertical PNG share cards
- Shared per-IP and per-account PostgreSQL rate limits and trusted-reverse-proxy IP handling
- PostgreSQL migrations embedded into both service binaries
- OpenAPI 3.1 contract

## Local Development

Requirements:

- Go 1.25.12
- PostgreSQL 17+
- S3-compatible storage, such as MinIO
- An OpenAI API key
- A TLS-capable SMTP account
- A RevenueCat project with App Store and Play Store products
- Docker Compose for the packaged local environment

Start the packaged environment:

```bash
cp .env.example .env
# Fill every empty value in .env.
docker compose up --build
```

Compose starts PostgreSQL, object storage, ClamAV, the API, and the background
worker. The API listens on loopback `API_HOST_PORT`. MinIO's API and console
listen on loopback ports `9000` and `9001`; PostgreSQL, ClamAV, and the worker
have no public host ports.

The template is production-oriented: `APP_ENV=production`, CORS is empty for
the native Flutter client, and bootstrap administrators are disabled. For local
development only, set `APP_ENV=development` and add an address to
`BOOTSTRAP_ADMIN_EMAILS`.

After the initial administrator has registered and verified their email, promote
that existing account with the audited one-off command:

```bash
docker compose run --rm --no-deps \
  --entrypoint heatcheck-admin \
  -e ADMIN_EMAIL=admin@example.com \
  api
```

The command does not create users and refuses inactive, deleted, or unverified
accounts.

### Pre-Launch Sample Challenge

Migration `000004_prelaunch_sample_challenge.sql` publishes a temporary
prelaunch warm-up so the production Today screen is usable before an
administrator has built the challenge calendar. It asks players for footage
they record themselves in GTA V or GTA Online; it explicitly prohibits GTA VI
trailers, promotional footage, leaks, mods, cutscenes, and clips copied from
other creators.

The sample starts when the migration is deployed, runs for at most 14 days, and
is not inserted after GTA VI's announced November 19, 2026 launch date. It is a
bootstrap sample, not an automatic challenge scheduler. Administrators remain
responsible for publishing subsequent challenges through
`POST /v1/admin/challenges`.

### RevenueCat

HeatCheck is free to browse, but an active RevenueCat `pro` entitlement is
required before the API will issue a signed clip-upload URL. Configure matching
subscription products in App Store Connect, Google Play Console, and RevenueCat.
The price and localized product metadata remain store-managed rather than being
hard-coded in this backend.

Create a RevenueCat webhook integration pointing to:

```text
https://heatcheck.dogi.watch/v1/billing/revenuecat/webhook
```

Set its Authorization header to the exact
`REVENUECAT_WEBHOOK_AUTHORIZATION` value and enable HMAC signing with
`REVENUECAT_WEBHOOK_SIGNING_SECRET`. The API validates both mechanisms, rejects
stale signatures, deduplicates event IDs, and fetches RevenueCat's current
customer state before changing access. Production rejects sandbox purchases.

The Flutter RevenueCat SDK must be configured after HeatCheck authentication
using the authenticated HeatCheck `user.id` as its exact, non-anonymous App
User ID. Public RevenueCat SDK keys belong in the Flutter build configuration;
`REVENUECAT_SECRET_API_KEY` must remain server-only.

To run without containers, install `ffmpeg`, `ffprobe`, and ClamAV, then start
the API and worker separately:

```bash
set -a
. ./.env
set +a
export DATABASE_URL='postgres://heatcheck:<password>@localhost:5432/heatcheck?sslmode=disable'
make run
make run-worker
```

Both binaries apply pending migrations under an advisory lock. The API
readiness endpoint also requires a recent worker heartbeat in production.

## Vultr Deployment

`.env` is the single deployment configuration source. `compose.yaml` reads all
credentials, endpoints, limits, and application settings from it. The `S3_*`
variables configure an S3-compatible object-storage protocol; they do not
require AWS.
The production API base URL is `https://heatcheck.dogi.watch`.

The API and worker use the same `heatcheck-app:local` image, so the existing
deployment runner remains valid:

```bash
git pull --ff-only
docker compose config --quiet
docker compose build --pull api
docker compose up -d --remove-orphans
docker compose ps
```

`docker compose up` recreates both services from the newly built shared image
when its image ID changes.

### MinIO on the Vultr VPS

The packaged Compose environment runs single-node MinIO beside the API. `minio-init`
creates the configured bucket, a dedicated application user, and a policy
restricted to reading, writing, and deleting objects in that bucket. For
production, set `MINIO_DATA_PATH` to an absolute mounted Vultr Block Storage
path and expose MinIO's API through its configured HTTPS hostname. PostgreSQL
can be placed on a mounted path in the same way with `POSTGRES_DATA_PATH`.

```dotenv
S3_ENDPOINT=minio:9000
S3_PUBLIC_ENDPOINT=storage.heatcheck.dogi.watch
S3_ACCESS_KEY=heatcheck-api
S3_SECRET_KEY=<generated-application-secret>
S3_BUCKET=heatcheck-clips
S3_REGION=us-east-1
S3_INTERNAL_USE_SSL=false
S3_PUBLIC_USE_SSL=true
```

`S3_ENDPOINT` is used by the API inside the private Docker network. `S3_PUBLIC_ENDPOINT` is embedded in signed upload and download URLs returned to browsers. Do not include `http://` or `https://` in either value.

Configure the reverse proxy, TLS, block-volume backups, and lifecycle policies
before accepting uploads. Native Flutter clients do not require bucket CORS;
configure it separately if a web client is added.
An API and object-storage reverse-proxy template is provided at
[`deploy/nginx.conf.example`](deploy/nginx.conf.example).

MinIO's open-source repository is archived. The Compose file pins its final
community image and uses the service in single-node mode. A managed
S3-compatible service such as Vultr Object Storage is the preferred production
choice because it receives provider-managed security and durability updates.

### Vultr Object Storage

Vultr's managed Object Storage is also S3-compatible and works without a code change:

```dotenv
S3_ENDPOINT=ewr1.vultrobjects.com
S3_PUBLIC_ENDPOINT=ewr1.vultrobjects.com
S3_ACCESS_KEY=<vultr-object-storage-access-key>
S3_SECRET_KEY=<vultr-object-storage-secret-key>
S3_BUCKET=heatcheck-clips
S3_REGION=us-east-1
S3_INTERNAL_USE_SSL=true
S3_PUBLIC_USE_SSL=true
```

Replace the example endpoint with the hostname shown by the Vultr control
panel. Native Flutter clients do not use browser CORS. If a web client is added,
allow its exact HTTPS origin for signed `PUT` and `GET` operations with the
`Content-Type` header.

## Frontend Workflow

### Registration

1. Fetch `GET /v1/policies`.
2. Display every policy where `requires_acceptance` is true.
3. Send the accepted `kind` and `version` values to `POST /v1/auth/register`.
4. Store the access token in memory and the refresh token in platform-secure storage.
5. Complete the emailed link through `POST /v1/auth/verify-email`.
6. Call `GET /v1/me` after login. If it returns missing policy acceptances, submit them to `POST /v1/me/policy-acceptances`.

### Clip Submission

1. Authenticate RevenueCat with the exact HeatCheck `user.id`.
2. Purchase or restore the product through the RevenueCat Flutter SDK.
3. Call `POST /v1/me/subscription/sync`, then confirm `active=true` through
   `GET /v1/me/subscription`.
4. Call `POST /v1/uploads` with the MIME type and exact byte size.
5. Upload the file to the returned signed URL using the returned HTTP method and headers.
6. Call `POST /v1/uploads/{uploadID}/complete`.
7. Call `POST /v1/challenges/{challengeID}/submissions`.
8. The durable worker scans, validates, transcodes, samples, moderates, and verifies the clip.
9. Poll `GET /v1/submissions/{submissionID}`. Publication requires both `verification_status=passed` and `moderation_status=approved`.

Creating an upload atomically reserves one submission allowance. The default is
one reservation per UTC day and 30 per UTC calendar month. An abandoned signed
upload releases capacity when its short reservation expires; completing the
upload extends the reservation long enough to create the submission. A
successful submission consumes it permanently for that allowance window.
`GLOBAL_DAILY_SUBMISSION_LIMIT` places a second, platform-wide ceiling on new
reservations and defaults to 100 per UTC day, bounding daily processing cost.

Download URLs are short-lived and regenerated in API responses. Object keys and storage credentials are never public.

The OpenAI key is available only to the worker and is never returned to the
Flutter app. The verifier uses `gpt-5.6-sol` by default and
`omni-moderation-latest`; both are configurable in `.env`.

### Moderation

Moderators use:

- `GET /v1/admin/submissions?status=pending`
- `PUT /v1/admin/submissions/{id}/verification`
- `GET /v1/admin/reports?status=open`
- `POST /v1/admin/moderation/actions`
- `POST /v1/admin/reports/{id}/dismiss`
- `GET /v1/admin/appeals?status=pending`
- `PUT /v1/admin/appeals/{id}`

Linking a moderation action to `report_id` resolves that report in the same transaction. Every enforcement action writes an audit event.

### Copyright

Anyone may submit a notice through `POST /v1/copyright/notices`. The public response contains only its identifier and status; claimant contact information is restricted to moderator APIs.

After review, a moderator can move the notice to `actioned`, which restricts the
linked submission in the same transaction and emails the parties. Only the
uploader of that submission can file a counter-notice. A resolved claim restores
the submission only when no other copyright or moderation restriction remains.

The included policy is an operational starting point, not jurisdiction-specific legal advice. Final notice fields, deadlines, designated-agent details, and retention rules should be reviewed for each launch market.

### Accounts

- `POST /v1/me/exports` creates a ZIP containing account JSON and retained source
  clips, or standardized MP4 clips after source retention expires.
- `GET /v1/me/exports/{id}` returns status and a short-lived URL when ready.
- `DELETE /v1/me` requires the current password and schedules deletion.
- `DELETE /v1/me/deletion` cancels during the configured grace period.
- `GET /v1/me/sessions` and the session `DELETE` routes manage active devices.

Account deletion removes the RevenueCat customer record through the durable
worker and detaches retained billing audit events from the deleted profile. It
does not cancel an App Store or Play Store renewal. The Flutter deletion
confirmation must show the subscription `management_url` returned by
`GET /v1/me/subscription` and tell the user to cancel the store subscription
separately.

## API Conventions

- JSON request and response bodies
- Bearer access tokens via `Authorization: Bearer <token>`
- Response resources under `data`
- Structured errors under `error`
- Offset pagination via `limit` and `offset`
- `428 Precondition Required` when a current required policy has not been accepted
- `402 Payment Required` when paid participation requires an active Pro entitlement
- `429 Too Many Requests` when a submission allowance or rate limit is exhausted
- `422 Unprocessable Entity` for validation or invalid state transitions

See [api/openapi.yaml](api/openapi.yaml) for the frontend contract. The running
service exposes the same embedded document at
`https://heatcheck.dogi.watch/openapi.yaml`.

## Verification

```bash
make fmt
make test
make vet
```

## Production Operations

The application behavior is implemented, but production durability and legal
approval remain deployment responsibilities:

- Put `heatcheck.dogi.watch` and the public S3 endpoint behind TLS.
- Store `.env` with mode `0600`, restrict root access, and rotate secrets through a managed secret store where possible.
- Back up PostgreSQL and object storage off-host, encrypt backups, and test restoration.
- Monitor `/readyz`, container restarts, dead jobs, disk space, SMTP delivery, OpenAI errors/cost, and ClamAV signature updates.
- Monitor RevenueCat webhook failures, subscription reconciliation errors, paid conversion, allowance exhaustion, and per-submission infrastructure cost.
- Publish counsel-reviewed Terms of Use and Privacy Policy through the policy administration endpoint.
- Replace the seeded policy contact language with the designated copyright agent and jurisdiction-specific deadlines.
- Document the human on-call procedure behind `SAFETY_ALERT_EMAIL`; software delivery alone is not an escalation response.
