# Gateway Body Log

Open **Admin → Interaction logs → Capture settings** to enable body capture.
New installations start with capture disabled. Upgrades import the existing
`gateway_body_log_enabled`, `gateway_body_log_capture_request` and
`gateway_body_log_capture_response` switches when no v2 configuration exists.

The file backend defaults to complete capture (`capture_limit_bytes: 0`).
The previous 256 KiB default and 1 MiB ceiling do not apply to new file logs.
The gateway's own request, response and WebSocket limits still apply.

## Storage and configuration

PostgreSQL's private `bodylog` schema stores settings, request metadata, body
references, usage associations and background task links. Body bytes stay in
local files. The module initializes its own schema without allocating new
numbers in the upstream migration sequence.

Set `BODY_LOG_DIR` to an absolute path in a persistent volume, for example
`/app/data/bodylogs`. If unset, the directory is `bodylogs` under `pricing.data_dir`.
The directory must be empty on first use or already belong to this module
(`.bodylog-v2`). Files use mode `0600`; directories use `0700`.

This backend is for a single application instance. Keep its database and
file directory together when backing up or restoring it. A database-only backup
contains references, not the new body data. A future shared object-store backend
must preserve private references, access checks, incremental reads and deletion
semantics; no S3 upload backend is included in this version.

The admin API and page expose:

| Setting | Initial value | Meaning |
| --- | --- | --- |
| `enabled` | `false`, or the imported legacy switch | Capture new gateway traffic |
| `capture_request` | `true`, or the imported legacy switch | Save client and upstream request directions |
| `capture_response` | `true`, or the imported legacy switch | Save client and upstream response directions |
| `capture_limit_bytes` | `0` | No per-body size truncation; a positive value sets an optional limit |
| `retention_days` | `7` | Maximum age, measured from record creation |
| `storage_budget_bytes` | `10737418240` | 10 GiB for owned files, including temporary compression copies |
| `capacity_policy` | `delete_oldest_completed` | Remove oldest completed, indexed records under capacity pressure |

`pause_capture` preserves unexpired logs under capacity pressure. Capture gaps
remain visible and gateway traffic continues. Retention expiry still applies.
Lowering age or capacity limits can remove existing new-module logs on a
subsequent cleanup pass; increasing them cannot restore deleted data. Active
captures and unacknowledged index revisions are protected.
When the new budget is below current usage, cleanup permits one replacement
manifest at a time so it can reclaim space. That temporary metadata remains
accounted; body writes continue to enforce the new budget.

Settings are saved in PostgreSQL and take effect without rebuilding or restarting.
The existing `/api/v1/admin/settings` GET/PUT fields
`gateway_body_log_enabled`, `gateway_body_log_capture_request`,
`gateway_body_log_capture_response` and `gateway_body_log_max_bytes` remain
available through a compatibility adapter. The size field now accepts `0` for
complete capture. New controls live in the independent body-log settings API.
Capture switches and per-body limits are snapshotted when a capture session
starts; retention and capacity use the current configuration.
The writer also preserves 1 GiB of host filesystem space, independently of the
configured file budget. This floor is an internal implementation constant.

## What is recorded

The router discovers the complete route set registered by
`RegisterGatewayRoutes`, including aliases and native Gemini endpoints. Capture
starts before body limits, authentication and admission. Failed requests can be
found without a usage row. Health, login, management and static asset routes are
outside this route set.

The observation directions have distinct meanings:

| Direction | Observed bytes |
| --- | --- |
| `client_request` | Request bytes read by the application |
| `client_response` | Bytes accepted by the application's downstream writer |
| `upstream_request` | Final request bytes read by an application-level upstream exchange |
| `upstream_response` | Upstream response bytes read by the application |

Capturing does not drain unread bodies, alter business payloads or add retries.
An authentication failure whose input was not consumed is `not_read`, rather
than an empty successful capture. A successful writer call does not establish
delivery to the remote client. Headers and content encoding identify the
application observation point; these are not packet captures.

The shared upstream adapter covers HTTP, SSE, multipart and binary bodies.
Plugin transports and the dedicated Gemini/Vertex batch and GCS clients use the
same observer. Nested adapters avoid recording the same HTTP exchange twice.
HTTP request and response completion are tracked independently, so an early
response does not prematurely close an ongoing upload.

OpenAI passthrough/bridge frames, pool lease reads/writes, Grok Realtime, Live
sideband and Live's background observer are observed at message boundaries.
Pool capture belongs to the lease, not the reusable socket. Explicit
`response.create`, `response.created` and `message_start` boundaries start new
directional bodies so optional capture limits reset across those turns.
Unknown protocols retain their framed message stream. The record/parent tree
preserves gateway, upstream attempt and task association.

WebSocket downloads use a lossless `BWS1` archive: four magic bytes followed by
repeated 13-byte headers and payloads. Each header contains a big-endian 32-bit
payload length, one message-type byte, and a big-endian 64-bit Unix timestamp in
nanoseconds. The archive preserves binary messages without base64 expansion.
Its framing bytes count toward a configured body limit.

Async image execution and batch jobs retain a parent association separately
from their business payloads; batch and Live links are persisted in the
extension's task table. Usage IDs are optional associations added after billing.
The module does not change billing, routing, scheduling, retry or task payloads.
WebRTC media sent directly between a client and provider does not pass through
the gateway and is outside capture.

Authorization, API keys, cookies, Live attestation and known signed query
credentials are redacted from metadata. Body bytes are preserved as observed.
Only administrators can inspect them. Sensitive reads and setting changes use
the existing admin authentication and audit middleware.

## Reading large logs

The detail view separates input/history/instructions/tool results, assistant
output/tool calls, parameters/metadata and original bodies. Tool links use
explicit call IDs; ambiguous or absent IDs do not produce guessed links.
Original roles remain available, including Anthropic tool results inside `user`
messages and Gemini's `model` role. Attachments are collapsed and external media
is not loaded by rendered log Markdown.

Anthropic, Chat Completions, Responses and Gemini use read-only parsers.
SSE multi-line data, streamed text/tool arguments and final Responses snapshots
are assembled without modifying the archive. The parser is versioned independently
of the body format.

Each initial text item is at most 64 KiB; item lists use 50-item pages and
virtualize longer lists. Parsing reads at most 32 MiB per body and allows two
concurrent parses. A larger, binary, unknown or interrupted body can still be
read in the original-body view or downloaded in full. Original-body pages replace
one another and support previous/next navigation, rather than accumulating a
full copy in browser memory.

Downloads use administrator-issued, one-use local capabilities with a one-minute
expiry and `no-store`. They stream through the application and do not publish
storage URLs.

The historical `GET /api/v1/admin/usage/:id/body-log` endpoint continues reading
the original BYTEA table. The usage-row link first queries new records by usage
ID and falls back to that endpoint for old records. Historical truncation stays
visible. The original `145_gateway_body_logs.sql` and
`146_gateway_body_logs_usage_log_id.sql` files remain unchanged for migration
ledger compatibility. No historical BYTEA cleanup or bulk migration is performed.

## Durability and resource behavior

Body writers use 64 KiB buffers, with a combined 64 MiB active buffer budget,
and roll into new files every 8 MiB. Buffer exhaustion marks the affected
capture unavailable while business traffic continues. Sealed
files are fsynced, then compressed by a bounded background worker. Compression
replaces the manifest before unlinking the raw file. Metadata index failures
are retried from manifests; a bounded scan resumes interrupted captures after
restart. An unflushed tail can be lost after a crash and is reported as partial.
Repeated archive updates for the same record share one pending queue entry;
updates arriving during processing can schedule one additional pass.

The file budget includes manifests, bodies and temporary compression output.
Cleanup writes and indexes a tombstone before deleting owned payloads, protects
readers, and verifies that the latest manifest revision has been indexed.
Expired content becomes unavailable with a retention/capacity reason.
Old database bodies and unrelated filesystem content are outside cleanup.

Local writes, flushes and fsyncs have a cost on the capture path; compression
and normal metadata indexing run in the background. Storage errors do not become
gateway reader/writer errors. A bounded metadata fallback can preserve searchable
request details when a manifest cannot be saved. Simultaneous storage/index
failure can exhaust that fallback; failure counts and the latest error are
exposed in the page. Initialization failures are reported through application
startup errors.

This moves body traffic out of PostgreSQL and its WAL, but is not a production
throughput guarantee. Measure representative payloads, concurrency, TTFT,
latency, memory and filesystem throughput before selecting a production budget.
The maximum age and budget do not guarantee seven days of retained traffic.

## Maintenance and validation

See [real application acceptance and performance measurements](GATEWAY_BODY_LOG_VALIDATION.md)
for the 2026-11-04 local gateway/browser results and measured capture overhead.
The [main integration review](GATEWAY_BODY_LOG_INTEGRATION_REVIEW.md) covers
retiring old fork behavior, preserving unfinished work and the legacy database
upgrade test.

Implementation lives in `backend/internal/bodylog/`, isolated repository/service
adapters and dedicated frontend components. Core forwarding result types,
protocol function signatures and the global settings DTO carry no body buffers.
When syncing upstream, check added gateway routes, dedicated HTTP clients,
WebSocket implementations and detached/background contexts against these
boundaries.

Useful checks:

```sh
cd backend
go test -race ./internal/bodylog
go test ./internal/service -run '^TestBodyLog'
go test ./internal/server/routes -run '^TestBodyLog' -v
# Use only a disposable, isolated database: this test writes its fixtures.
BODYLOG_TEST_DSN='postgres://…/bodylog_test?sslmode=disable' \
  go test -tags=integration ./internal/bodylog -run TestPostgres -v
```

The tests cover complete bodies larger than 1 MiB, rolling compression and
restoration, late usage association, unread/failed requests, EOF/close behavior,
active previews, crash recovery, capture budget failure, retention/index
protection, parser/tool semantics, every registered gateway route and socket
reuse across different users.
