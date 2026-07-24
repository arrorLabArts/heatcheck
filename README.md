# HeatCheck Backend

Go API for HeatCheck's gameplay challenges, proof clips, voting, moderation, and copyright workflows.

## Included

- Email/password registration with an 18+ age gate by default
- Short-lived JWT access tokens and rotating, revocable refresh tokens
- Immutable, versioned policy documents and per-user acceptance records
- Scheduled public and unlisted challenges
- Direct, signed uploads to S3-compatible object storage
- One submission per user per challenge
- Moderation-gated publication and verification status
- Style votes with transactional score aggregation
- Blocking-aware feeds and voting
- User reports with safety-based priority
- Audited moderator actions, suspensions, and appeals
- Copyright notices, content restriction, and uploader counter-notices
- PostgreSQL migrations embedded into the API binary
- OpenAPI 3.1 contract

## Local Development

Requirements:

- Go 1.25.3
- PostgreSQL 17+
- S3-compatible storage, such as MinIO
- Docker Compose for the packaged local environment

Start the packaged environment:

```bash
docker compose up --build
```

The API listens on `http://localhost:8080`. MinIO listens on `http://localhost:9000`, and its console is at `http://localhost:9001`.

The local bootstrap administrator is `admin@example.com`. Register that email first to create the administrator account. `BOOTSTRAP_ADMIN_EMAILS` is intentionally rejected when `APP_ENV=production`; production roles must be provisioned through a controlled database or operations process.

To run without containers:

```bash
cp .env.example .env
set -a
. ./.env
set +a
make run
```

The API applies pending embedded migrations and creates the configured media bucket on startup.

## Vultr Deployment

The `S3_*` variables configure an S3-compatible object-storage protocol. They do not require AWS.

### MinIO on the Vultr VPS

The packaged Compose environment runs MinIO beside the API. For production, mount a persistent Vultr Block Storage path into MinIO's `/data` directory and expose MinIO's API through an HTTPS hostname such as `s3.example.com`.

```dotenv
S3_ENDPOINT=minio:9000
S3_PUBLIC_ENDPOINT=s3.example.com
S3_ACCESS_KEY=<dedicated-minio-application-key>
S3_SECRET_KEY=<dedicated-minio-application-secret>
S3_BUCKET=heatcheck-clips
S3_REGION=us-east-1
S3_INTERNAL_USE_SSL=false
S3_PUBLIC_USE_SSL=true
```

`S3_ENDPOINT` is used by the API inside the private Docker network. `S3_PUBLIC_ENDPOINT` is embedded in signed upload and download URLs returned to browsers. Do not include `http://` or `https://` in either value.

Use a dedicated MinIO application identity rather than the root credentials. Configure the reverse proxy, TLS, bucket CORS rules, block-volume backups, and lifecycle policies before accepting uploads.

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

Replace the example endpoint with the hostname shown by the Vultr control panel. The bucket must allow CORS requests from the frontend origin for signed `PUT` and `GET` operations with the `Content-Type` header.

## Frontend Workflow

### Registration

1. Fetch `GET /v1/policies`.
2. Display every policy where `requires_acceptance` is true.
3. Send the accepted `kind` and `version` values to `POST /v1/auth/register`.
4. Store the access token in memory and the refresh token in platform-secure storage.
5. Call `GET /v1/me` after login. If it returns missing policy acceptances, submit them to `POST /v1/me/policy-acceptances`.

### Clip Submission

1. Call `POST /v1/uploads` with the MIME type and exact byte size.
2. Upload the file to the returned signed URL using the returned HTTP method and headers.
3. Call `POST /v1/uploads/{uploadID}/complete`.
4. Call `POST /v1/challenges/{challengeID}/submissions`.
5. The submission remains private to its owner and moderators until approved.

Download URLs are short-lived and regenerated in API responses. Object keys and storage credentials are never public.

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

After review, a moderator can move the notice to `actioned`, which removes the linked submission in the same transaction. Only the uploader of that submission can file a counter-notice. Restoring a notice republishes the submission and records a corresponding moderation action.

The included policy is an operational starting point, not jurisdiction-specific legal advice. Final notice fields, deadlines, designated-agent details, and retention rules should be reviewed for each launch market.

## API Conventions

- JSON request and response bodies
- Bearer access tokens via `Authorization: Bearer <token>`
- Response resources under `data`
- Structured errors under `error`
- Offset pagination via `limit` and `offset`
- `428 Precondition Required` when a current required policy has not been accepted
- `422 Unprocessable Entity` for validation or invalid state transitions

See [api/openapi.yaml](api/openapi.yaml) for the frontend contract.

## Verification

```bash
make fmt
make test
make vet
```

## Production Notes

- Replace the in-process rate limiter with a shared limiter at the ingress or Redis layer.
- Put the API and object storage behind TLS and use a managed secret store.
- Add email verification, password reset, and session-management screens before public registration.
- Add account export and reviewed account-deletion/retention behavior before public launch.
- Connect the verification-status endpoint to an asynchronous media scanning and gameplay-verification worker.
- Add transcoding, duration validation, malware scanning, and media retention jobs.
- Send urgent child-safety reports to a separately documented escalation process.
- Configure database backups, object lifecycle policies, monitoring, and alerting.
- Publish reviewed Terms of Use and Privacy Policy through the policy administration endpoint before launch.
- Encrypt copyright-claimant and counter-notice contact fields at rest with tightly scoped key access.
