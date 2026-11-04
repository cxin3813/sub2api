# Gateway Body Log acceptance — 2026-11-04

The file-backed implementation passed real local gateway, database and browser
acceptance on upstream base `4a4a83ee5` (0.2.19). This is an uncommitted development
candidate on `codex/bodylog-v2`. Production integration and deployment are separate
steps.

Sanitized measurements and case results are in
[`2026-11-04.json`](../backend/internal/bodylog/testdata/acceptance/2026-11-04.json).
Configuration and operating semantics are in [Gateway Body Log](GATEWAY_BODY_LOG.md).

## Real application acceptance

The embedded production frontend and compiled backend ran against disposable
PostgreSQL 16 and Redis 8 containers. A local synthetic provider handled real HTTP,
SSE and WebSocket traffic; it also accepted image uploads through a synthetic S3
endpoint. Admin users, gateway credentials, groups and provider accounts were
created through the application APIs. Tests used no production database or user
traffic.

The browser loaded the actual application and called its actual APIs without
route mocks. Synthetic provider content makes byte comparison deterministic;
these runs do not validate external vendor availability.

| Path | Evidence |
| --- | --- |
| Chat JSON, Responses JSON/SSE, Chat SSE, Embeddings | Four observation directions downloaded through the admin API and compared with actual client bytes and provider fixture bytes |
| Large request and tool history | 10,350,376 input bytes retained across two rolling parts; original roles, tool results and output call IDs parsed |
| Audio response | 1,048,580 binary bytes preserved; raw access works while semantic parsing reports unsupported |
| Multipart upload | 2,097,312 input bytes preserved, including binary content |
| Denied request | HTTP 401 still indexed; unconsumed input reported as `not_read` |
| WebSocket pool and passthrough | Two rounds per session, two request and six response frames per upstream archive; lossless client and upstream archive comparisons |
| Pool reuse between users | Same upstream connection reused by two synthetic users; each lease's archive contains only that user's frames |
| Asynchronous image | Submit 202, background provider request, synthetic S3 upload and completed poll; `gateway_http → async_task → upstream_http` associations verified |
| Legacy BYTEA | One synthetic legacy row read through the actual `/admin/usage/:id/body-log` endpoint with both bodies intact |
| Browser | Input/output and tool rendering, raw next/previous pages, native 10,350,376-byte download, settings save/restore, 390px layout without horizontal overflow; no page errors |
| Runtime capacity change | Budget lowered from above usage to 600,249,620 bytes; usage fell from 602,346,772 to 600,201,250 bytes in 0.484 seconds; 67 records expired for capacity and failure count stayed zero |

Some SSE readers stop at a protocol terminator without observing transport EOF.
Their upstream body remains `partial` at the byte-observation layer even when
its downloaded bytes match the provider's entire response. The verifier permits
this state only after the complete fixture-byte comparison. Capture does not
drain the provider stream to change that state.

Usage associations are asynchronous. Immediate case snapshots can contain an
empty usage list; subsequent database inspection confirmed late associations.

Other protocol adapters, all 125 registered gateway routes, recovery and failure
paths are covered by the module's automated tests. Live vendor sessions,
WebRTC media, and every possible provider payload were not exercised by these
local end-to-end cases.

## Performance measurements

The host was an Intel Core i3-12300T (4 cores / 8 threads), shared with other
processes. The provider ran on the same host and wrote its own wire fixtures.
Inputs used a deterministic base64 pattern rather than a claim of representative
production compressibility. Both client and upstream directions were captured,
with no configured body truncation.

Two measurements answer different questions:

- Burst runs: four warmups, then off/on/on/off; 80 requests at concurrency 8 for
  64 KiB JSON, 40 at concurrency 4 for 1 MiB JSON, and 48 at concurrency 8 for
  SSE with 16 KiB input. Each enabled run required all roots indexed and ended,
  no remaining raw/compression files, and zero capture failures.
- Rate-controlled runs: 80 requests at 10 requests/second for each input size,
  off then on. All 80 enabled roots were indexed and ended with zero failures.

Latency is client-observed completion time. TTFB is time until the first byte
read by the local client. CPU and physical write bytes come from the backend
process, excluding PostgreSQL and provider processes. Request-window writes omit
any later archive drain. Archive drain is the time after responses until the
queue is empty and raw/compression files are gone. These are short local
measurements, with no production QPS or p99 claim.

### At 10 requests/second, capture adds measurable latency and writes

| Input | P50 off / on (ms) | P95 off / on (ms) | TTFB P95 off / on (ms) | Backend CPU off / on (s) | Backend writes off / on (MiB) |
| --- | --- | --- | --- | --- | --- |
| 64 KiB JSON | 25.42 / 31.46 | 29.42 / 43.44 | 29.28 / 43.29 | 1.23 / 1.94 | 0.344 / 29.945 |
| 1 MiB JSON | 54.54 / 67.55 | 65.50 / 85.69 | 65.36 / 85.55 | 4.46 / 5.58 | 0.344 / 330.242 |

Enabled archive drain was 0.057 and 0.172 seconds respectively. P95 completion
overhead was approximately 14 and 20 ms in these runs. Full four-direction
capture and local flush/fsync work explain why moving bodies out of PostgreSQL
does not remove capture cost.

### Bursts expose throughput cost and variability

Ranges below contain the two runs per setting after the queue correction.

| Workload | Throughput off / on (requests/s) | P95 off / on (ms) |
| --- | --- | --- |
| 64 KiB JSON, concurrency 8 | 345.7–376.6 / 151.2–174.5 | 25.63–34.70 / 60.18–151.12 |
| 1 MiB JSON, concurrency 4 | 67.1–89.2 / 46.9–47.9 | 52.87–68.34 / 109.38–123.86 |
| SSE, concurrency 8 | 297.4–374.2 / 182.2–188.4 | 25.91–32.86 / 51.19–53.23 |

The 151.12 ms P95 outlier is retained. Run ordering and shared-host noise limit
cross-run causal claims. This short burst test does not establish sustainable
maximum throughput.

Backend peak RSS across burst and controlled runs was roughly 138–219 MiB.
Garbage collection and retained heap make off/on peaks unsuitable for a memory
improvement claim. The 64 MiB capture-buffer budget bounds capture buffers,
not total application RSS.

No legacy BYTEA-versus-file benchmark was run. The design moves body writes out
of PostgreSQL and its WAL; these measurements establish the new implementation's
capture overhead, not a measured speedup over the old storage implementation.

## Acceptance found and corrected three defects

Repeated start/part/end/usage updates previously occupied separate entries in
the bounded archive queue. Under short bursts, duplicate IDs crowded out distinct
records and pushed their work onto the recovery scan. The queue now coalesces
pending IDs and permits one additional pass for updates arriving during a sync.
The bounded fallback recovery remains in place.

| Enabled workload | Archive drain before / after queue correction |
| --- | --- |
| 64 KiB JSON | 12.34–19.25 s / 0.44–0.68 s |
| SSE | 22.45–31.34 s / 0.47–0.49 s |
| 1 MiB JSON | 0.117–0.127 s / 0.139–0.170 s |

This corrects archive lag; the measurements do not show a request-latency
improvement. `TestArchiveBurstDoesNotCrowdOutDistinctRequests` protects the
distinct-record queue behavior.

Lowering the file budget below current usage also prevented cleanup from
reserving its replacement tombstone manifest. Cleanup now permits one accounted
replacement manifest while reclaiming an existing record. Body writers still
enforce the budget, and cleanup still indexes the tombstone before deletion.
`TestReducingBudgetBelowCurrentUsageStillCleansCompletedLogs` reproduced the
failure before the fix and passes after it. The runtime capacity case above
verified the correction through the actual application API.

The settings dialog previously exposed its cached form while reloading current
configuration. A late response could overwrite an administrator's edits.
The dialog now waits for current settings before showing editable inputs,
and stale configuration responses cannot replace newer ones. A delayed-response
frontend regression covers loading and subsequent save. Mobile screenshots
wait for the upstream layout's sidebar transition to finish before checking
content width and overflow.

## Candidate checks and next use

After both storage corrections, `go test -race ./internal/bodylog`, the embedded
backend build, and `git diff --check` passed. The real application cases and
browser acceptance were rerun on that binary. The subsequent settings-dialog
correction passed seven targeted frontend tests, three locale checks, lint,
TypeScript checking and a production build, followed by an embedded rebuild and
real-browser acceptance. Performance measurements remain applicable to the
unchanged capture path; the storage correction after measurement affects capacity
reclamation.

Earlier implementation checks include the isolated PostgreSQL integration test,
service/socket regressions, frontend tests, type checking, lint and production
build. Four external Aliyun captcha tests failed in the broader backend run;
the backend unit suite passed with those external tests excluded. This
acceptance did not rerun the whole suite or validate production services.

Before deployment, select a persistent `BODY_LOG_DIR` volume and size the budget
for actual traffic. The current backend serves one instance. Full capture is
best effort within gateway limits, available disk, configured retention and
failure states. The 7-day / 10-GiB defaults are runtime configurable; they cannot
guarantee seven days of retained traffic at arbitrary request volume.
