# Body Log integration review — 2026-11-04

Use the accepted upstream-based candidate as the final application tree, and
retain the old fork's history through a one-time history reconciliation.
An ordinary merge from the old fork would retain retired custom behavior.

The reviewed refs are local `main` / `origin/main` at `bf07fa5d4` and
`upstream/main` / `codex/bodylog-v2` base at `4a4a83ee5` (0.2.19).
This review does not advance the upstream baseline. The candidate's implementation
and acceptance changes are still uncommitted.

## The candidate contains Body Log and its integration points

The candidate starts from upstream, with independent capture/storage/parsing/API
code in `backend/internal/bodylog/`, dedicated repository/service adapters,
frontend components, tests, operating documentation and acceptance data.
Changes to existing upstream files connect HTTP and WebSocket observation,
background task context, identity/usage association, startup/shutdown, admin
authorization/auditing and the page.

The following selected business files match upstream byte for byte:

- `gateway_service.go` and `gateway_usage_billing.go`
- `account_test_service.go` and `domain_constants.go`
- `openai_gateway_passthrough.go`
- `instructions_gpt5_2.txt` and `instructions_gpt6_astra.txt`
- Both account `AccountTestModal.vue` files

Old `ForwardResult` capture buffers, payload rewriting, custom User-Agent and
account-test fallback implementations are absent from this candidate. Logging
observes the upstream behavior rather than restoring those fork policies.
Application data such as existing account credentials is not rewritten.

The large main-to-candidate diff includes the upstream 0.2.16-to-0.2.19 update.
It should not be interpreted as the Body Log refactor's footprint. Review the
upstream-to-candidate diff to inspect this feature.

## Ordinary merging preserves unwanted behavior

A `git merge-tree` preflight of old `main` and the pinned upstream base found
10 conflicting files. This preflight uses the candidate's base, before applying
the uncommitted new Body Log files; it is not a conflict count for the final
candidate or for future upstream updates.

More importantly, conflict-free files retain old fork behavior. The generated
merge tree retains the custom account-test User-Agent/fallback code and
OpenAI passthrough request normalization, field stripping and optional image-tool
retry. Resolving only the reported conflicts would therefore fail the agreed
scope.

The preflight writes its objects and private index under
`/tmp/bodylog-main-review`, without changing repository refs or its real index.
The directory also contains complete binary-capable patches from upstream and
old main to the candidate, including new files.

## Legacy database upgrade passed

The migration directory comparison found 296 common SQL files, all with
identical contents. Both legacy Body Log migrations, `145_gateway_body_logs.sql`
and `146_gateway_body_logs_usage_log_id.sql`, are also identical to old main.
The four new upstream SQL files are:

1. `246_enable_gemini_image_generation_groups.sql`
2. `247_scoped_admin_api_keys.sql`
3. `248_group_image_count_billing.sql`
4. `249_mcp_oauth.sql`

`TestBodyLogUpgradeFromLegacyFork` ran against a new empty database in the
test-owned PostgreSQL 16 container. Using the current migration runner, it:

1. Applied the old main's 298 migration files.
2. Added synthetic user/account/key/usage rows, a legacy BYTEA body and the old
   capture settings.
3. Applied the candidate's migration set, reaching 302 migration ledger rows.
4. Initialized the independent `bodylog` schema.
5. Verified unchanged legacy bytes and imported request/response/enabled
   switches, with the new complete-capture size default.
6. Wrote and restored a new file-backed body with its usage association.
7. Repeated core migrations and module initialization successfully.

The test passed in 1.42 seconds. It exercises a historical schema and synthetic
data, rather than a copy of production data or the old server binary.

To repeat it, export the old migration files and create a fresh disposable
database. The test refuses a database with existing public tables:

```sh
mkdir -p /tmp/bodylog-old-source
git archive bf07fa5d4 backend/migrations | tar -x -C /tmp/bodylog-old-source
cd backend
BODYLOG_UPGRADE_MIGRATIONS_DIR=/tmp/bodylog-old-source/backend/migrations \
BODYLOG_UPGRADE_TEST_DSN='postgres://postgres@127.0.0.1:45449/bodylog_upgrade?sslmode=disable' \
  go test ./internal/repository -run '^TestBodyLogUpgradeFromLegacyFork$' -v -count=1
```

## Keep the original dirty worktree on a preservation branch

The original source directory has separate, unfinished work:

| Paths | Meaning | Integration treatment |
| --- | --- | --- |
| Both `AccountTestModal.vue` files and `utils/modelOptions*` | Fall back to model ID when display name is blank | Keep in original worktree; omit from the upstream-aligned application |
| `.gitattributes` | Union merge rule for local Trellis journals | Keep with local tooling work |
| Tracked and untracked `.trellis/` content | Journals, specifications and design/implementation records | Keep in original worktree |
| `.agents/` | Local agent files | Keep in original worktree |

The two modal changes are a non-Body-Log customization. Their omission follows
the user's decision to retain only Body Log. They remain available for later
review rather than being deleted or silently committed.

## Recommended integration sequence

This sequence requires authorization to create commits and advance `main`.
The review itself has not performed those steps.

1. Commit the accepted candidate on `codex/bodylog-v2`, including the upgrade
   test and review artifacts.
2. Merge old `main` into that branch with the `ours` **merge strategy**:
   `git merge --no-ff -s ours main`. This records both histories while retaining
   the already reviewed candidate tree. `-X ours` is a different operation and
   would still retain conflict-free legacy changes.
3. Verify that old main and the pinned upstream are ancestors, and the
   reconciliation commit's tree is identical to the candidate commit's tree.
4. In the original source worktree, create and switch to
   `codex/preserve-main-before-bodylog-v2` at old main. Switching at the same
   commit retains its staged, unstaged and untracked work.
5. Advance `main` to the reconciliation commit with an expected-old-value
   check against `bf07fa5d4`, then switch the current Body Log worktree to
   `main`. This is a history-preserving advance; no reset or forced push is
   needed.

The resulting layout is:

| Worktree | Branch / contents |
| --- | --- |
| Current Body Log worktree | `main`, accepted upstream + Body Log tree |
| Original source directory | `codex/preserve-main-before-bodylog-v2`, old main and its existing unfinished changes |

Push and production deployment remain separate actions. The single-instance
file backend also requires a persistent `BODY_LOG_DIR` volume at deployment.

The `ours` strategy above is specifically for retiring old fork policies after
their replacement has been reviewed. Later upstream updates should use ordinary
compatibility merges and review the Body Log adapters, new gateway routes and
socket/task boundaries. Independent modules reduce the shared edit surface;
WebSocket and background-task hooks still require semantic review.

See [application acceptance](GATEWAY_BODY_LOG_VALIDATION.md) and
[machine-readable integration evidence](../backend/internal/bodylog/testdata/acceptance/2026-11-04-integration.json).
