# Hermes Performance Benchmarks

This document records the submit-path optimizations and the benchmark results measured in the Codespaces environment on 2026-10-31.

## What Changed

The API now supports:

- Native CGO SQLite through `github.com/mattn/go-sqlite3`.
- WAL mode with configurable SQLite durability.
- One database connection, matching SQLite's single-writer model.
- A prepared insert statement reused for every request.
- `POST /submit/batch`, which inserts a JSON array in one transaction.
- `POST /submit/batch?response=none`, which returns `204` after commit instead of serializing every inserted row.
- `HERMES_DB` for selecting the database location.
- `HERMES_ADDR` for running isolated benchmark servers.
- `HERMES_SQLITE_SYNCHRONOUS` for selecting SQLite durability.
- `HERMES_SQLITE_JOURNAL_MODE` for selecting `WAL`, `DELETE`, or `OFF` journaling.
- `HERMES_SQLITE_LOCKING_MODE` for selecting `EXCLUSIVE` or `NORMAL` locking.
- `HERMES_SQLITE_TEMP_STORE`, `HERMES_SQLITE_CACHE_SIZE`, and `HERMES_SQLITE_MMAP_SIZE` for memory/cache tuning.

The default durability remains `NORMAL`, and the default database remains `database.db`.

## Reproducing The Benchmark

Build the API:

```sh
go build -o /tmp/hermes-api ./cmd/api
```

Start it on a local temporary filesystem:

```sh
HERMES_DB=/tmp/hermes-bench.db \
HERMES_ADDR=:3000 \
/tmp/hermes-api
```

Run one scenario at a time:

```sh
k6 run \
  -e SCENARIO=constant_high \
  -e DURATION=30s \
  -e HIGH_RATE=1000 \
  -e BATCH_SIZE=100 \
  -e BASE_URL=http://localhost:3000 \
  stress.js
```

The random profile can be run with:

```sh
k6 run -e SCENARIO=random_spikes -e BATCH_SIZE=100 stress.js
```

`HIGH_RATE` is the offered batch-request rate. The `batch_writes` metric is the actual submission-row rate and is the important throughput number for batch tests.

## Benchmark Matrix

Each case used:

- 100 preallocated VUs, up to 2,000 VUs.
- `constant_high` with an offered rate of 10,000 batch requests/sec.
- A fresh database.
- A 5-second duration.
- Native SQLite, WAL mode, one connection, and a prepared statement.
- 100% successful completed requests.

The load generator offered more work than the API could process in every case. Therefore, completed throughput and dropped iterations are reported separately.

| Storage | Sync | Batch | Response | Completed batches/sec | Submission rows/sec | p95 latency | Dropped iterations/sec |
| --- | --- | ---: | --- | ---: | ---: | ---: | ---: |
| Workspace loop mount | `NORMAL` | 1 | Full | 1,823 | 1,823 | 2.03s | 6,736 |
| `/tmp` | `NORMAL` | 1 | Full | 5,257 | 5,257 | 315ms | 4,447 |
| `/tmp` | `NORMAL` | 100 | Full | 828 | 82,837 | 3.75s | 6,627 |
| `/tmp` | `OFF` | 100 | Full | 912 | 91,157 | 3.68s | 6,471 |
| `/tmp` | `NORMAL` | 100 | None | 928 | 92,793 | 3.44s | 6,651 |
| `/tmp` | `OFF` | 100 | None | 962 | 96,185 | 3.40s | 6,701 |

These are saturation measurements, not latency targets. The high p95 values and dropped iterations mean the offered rate exceeded the available VU or server capacity.

## Journal And SQLite Configuration Matrix

This second matrix used the same five-second saturated test, `/tmp` storage, batch size 100, `response=none`, native SQLite, and normal k6 scenario settings.

| Journal | Sync | Locking | Temp/cache/mmap | Submission rows/sec | p95 latency |
| --- | --- | --- | --- | ---: | ---: |
| `WAL` | `NORMAL` | `EXCLUSIVE` | Tuned | 122,262 | 3.16s |
| `DELETE` | `NORMAL` | `EXCLUSIVE` | Tuned | 46,804 | 6.67s |
| `OFF` | `NORMAL` | `EXCLUSIVE` | Tuned | 75,481 | 4.82s |
| `WAL` | `OFF` | `EXCLUSIVE` | Tuned | 134,487 | 3.03s |
| `WAL` | `NORMAL` | `NORMAL` | Tuned | 124,128 | 3.24s |
| `WAL` | `NORMAL` | `EXCLUSIVE` | Defaults | 35,726 | 8.05s |

The test intentionally offered 10,000 batch requests/sec, which exceeded the API capacity. Use the rows/sec column for throughput comparisons and do not treat the p95 values as acceptable user-facing latency.

### Configuration Interpretation

- `WAL` was the best journal mode. It lets readers proceed while the writer appends to the WAL and avoids the heavier rollback-journal cycle.
- `DELETE` was substantially slower because each transaction manages the rollback journal directly.
- `OFF` journaling was faster than `DELETE` but still slower than WAL in this run and provides no crash-recovery journal.
- `synchronous=OFF` gave the highest result, but it can lose recent data or damage the database after a crash. Keep `NORMAL` for durable data.
- `locking_mode=NORMAL` performed almost the same as `EXCLUSIVE`. `EXCLUSIVE` is reasonable for one process but prevents other processes from opening the database concurrently.
- Removing the memory temp store, cache, and mmap settings caused the largest regression in this matrix. Keep the tuned defaults for this workload.

Example configuration comparison:

```sh
HERMES_DB=/tmp/hermes-bench.db \
HERMES_ADDR=:3000 \
HERMES_SQLITE_JOURNAL_MODE=WAL \
HERMES_SQLITE_SYNCHRONOUS=NORMAL \
HERMES_SQLITE_LOCKING_MODE=EXCLUSIVE \
HERMES_SQLITE_TEMP_STORE=MEMORY \
HERMES_SQLITE_CACHE_SIZE=-65536 \
HERMES_SQLITE_MMAP_SIZE=268435456 \
/tmp/hermes-api
```

## Why The Optimizations Helped

### Native SQLite

The original driver was pure Go. The native SQLite driver moves SQLite execution into the optimized C implementation and reduced per-statement overhead.

**Cost:** CGO and a C compiler are required in the build environment.

**Mitigation:** Ensure the image contains `gcc` and use a build check such as `CGO_ENABLED=1 go test ./cmd/api`.

### One Connection

SQLite permits concurrent readers but serializes writes. A five-connection pool caused writers to compete for the same SQLite write lock. One connection avoids that extra contention.

**Cost:** This is not suitable for scaling SQLite writes across multiple API processes.

**Mitigation:** Use PostgreSQL or another client/server database when multiple writers or replicas are required.

### Prepared Inserts

Preparing the insert once avoids reparsing and recompiling the SQL statement for every request.

**Cost:** The prepared statement is tied to the API's database connection lifecycle.

**Mitigation:** Keep the statement open for the process lifetime, as the API currently does.

### Batch Transactions

`/submit/batch` executes N prepared inserts and commits once. This amortizes transaction setup, WAL synchronization, and filesystem commit cost across the entire batch.

**Cost:** A failed batch rolls back all rows. Larger batches also increase request size, memory use, response size, and per-request latency.

**Mitigation:** Choose a bounded batch size such as 50-500, validate the full batch before starting the transaction, and retry failed batches with an idempotency key if duplicate protection matters.

### Temporary Filesystem

The workspace is a loop-backed mount. SQLite WAL commits are much slower there than on `/tmp` in this Codespace.

**Cost:** `/tmp` is ephemeral and can disappear when the container restarts.

**Mitigation:** Use `/tmp` only for benchmarks or disposable data. For durable data, use a local Docker volume, managed database, or PostgreSQL volume designed for database I/O.

### `synchronous=OFF`

This removes SQLite's strongest durability guarantees and produced a smaller additional gain after batching.

**Cost:** A host or process crash can lose recently committed transactions and may corrupt the database in severe failure scenarios.

**Mitigation:** Keep the default at `NORMAL`. Use `OFF` only for disposable benchmarks, rebuildable caches, or explicitly accepted data loss.

### Response Suppression

Returning the full batch causes the API to JSON-encode every inserted row and sends all payloads back to k6. `response=none` returns `204` after the transaction and measures write throughput more directly.

**Cost:** Clients do not receive inserted IDs or echoed records.

**Mitigation:** Use full responses for normal API calls and response suppression for ingestion endpoints or clients that only need an accepted count. A future option could return only `{\"inserted\": N}`.

## Reading The Results

The largest improvement is batching: one transaction for 100 rows increased row throughput from roughly 5.3k rows/sec to 82.8k rows/sec on `/tmp` with normal durability.

Moving the database from the workspace mount to `/tmp` increased single-row throughput from 1.8k to 5.3k rows/sec. This confirms that Codespaces filesystem latency is a major part of the original gap.

The final `/tmp` result reached approximately 96k rows/sec with batch size 100, write-only responses, and `synchronous=OFF`. That number is not durable production throughput. The more defensible production-oriented result is the `/tmp`, `NORMAL`, batch-100 configuration at approximately 83k rows/sec, subject to the saturation and dropped-iteration caveats above.

For sustained durable throughput beyond SQLite's single-writer limits, move the write path to PostgreSQL and use multi-row inserts or `COPY`. For lower latency, reduce the offered rate until dropped iterations reach zero and compare p95 latency at that steady rate.
