# Counter Store Evolution

## 1. Current Implementation (v0.2–v0.3)

The counter store persists per-source counter values between scrapes so the normalizer can compute deltas (rate-of-change) from cumulative Prometheus counters. Without persistence, every pod restart resets delta state and produces a spurious spike on the first scrape.

**Implementation**: bbolt (pure-Go B+tree, single-file embedded database).

- Single bbolt file at a configurable path (default `/data/argus/counters.db`)
- In-memory cache for O(1) reads; writes go through to bbolt for durability
- On startup, all existing keys are loaded into the cache
- `argus_counter_state_recovered_total` gauge reports how many keys were loaded — operators can verify restart recovery by checking this metric is non-zero after a rolling update
- `argus_counter_state_persisted_total` counter tracks successful writes
- `argus_counter_store_errors_total` counter (labels: `error_type=read|write|open`) surfaces silent failures

**Deployment**: StatefulSet with a PVC mounted at `/data/argus`. The bbolt file survives pod restarts because the PVC is retained across restart cycles of the same StatefulSet ordinal.

**Limitations**:
- Does not survive pod *replacement* (PVC is bound to the ordinal; scaling down and back up loses state)
- No exactly-once semantics — a crash between scrape and write loses the delta
- Single-writer only — cannot shard across multiple normalizer pods
- No split-brain protection — two normalizer pods writing to the same bbolt file will corrupt it (bbolt uses flock, so the second writer blocks, but does not fail cleanly under all container runtimes)

## 2. Limitations in Detail

### No multi-worker scaling

The Engine holds a mutex around store access. This is correct for single-pod, but means normalization throughput is bounded by a single goroutine's counter store write latency. At ~50μs per bbolt write, this caps at ~20k Put/s, which is sufficient for current deployments (hundreds of NFs) but will not scale to thousands of NFs across multiple PLMNs.

### Split-brain on concurrent normalizer pods

If two Argus pods are accidentally scheduled with the same PVC (misconfigured Deployment instead of StatefulSet), bbolt's flock prevents corruption but one pod will block indefinitely on Open. The operator sees a hung pod with no log output. The `argus_counter_store_errors_total{error_type=open}` metric now surfaces this, but it requires the pod to actually fail (the current 1s timeout will fire).

### No exactly-once delta computation

If the normalizer crashes after computing a delta but before persisting the new counter value, the next scrape will re-compute the same delta. This is acceptable for gauge-like derived KPIs (success rates) but can double-count event counters (registration attempts). The window for this race is small (~μs between compute and persist) but non-zero.

## 3. v0.4 Target: Redis + WAL

The production counter store replaces bbolt with a Redis-backed implementation that supports multi-worker normalization and survives pod replacement.

### Architecture

```
┌──────────────┐     ┌──────────────┐     ┌──────────────┐
│ Normalizer-0 │     │ Normalizer-1 │     │ Normalizer-N │
│  (Engine)    │     │  (Engine)    │     │  (Engine)    │
└──────┬───────┘     └──────┬───────┘     └──────┬───────┘
       │                    │                    │
       └────────────┬───────┘────────────────────┘
                    │
            ┌───────▼────────┐
            │  RedisStore    │
            │  (Lua atomics) │
            └───────┬────────┘
                    │
            ┌───────▼────────┐
            │  Redis Cluster │
            │  (3-node HA)   │
            └────────────────┘
```

### RedisStore design

```go
type RedisStore struct {
    client redis.UniversalClient
    wal    *WAL  // local WAL for crash recovery
}
```

**Atomic delta via Lua script**:
```lua
-- KEYS[1] = "argus:counter:{sourceKey}:{kpiName}"
-- ARGV[1] = new counter value
-- Returns: previous value (or nil if first scrape)
local prev = redis.call('GET', KEYS[1])
redis.call('SET', KEYS[1], ARGV[1])
return prev
```

This collapses Get+Put into a single atomic operation, eliminating the race window between delta computation and persistence. The normalizer computes `delta = newValue - tonumber(prev)` client-side.

**WAL for crash recovery**: Before issuing the Redis SET, the normalizer appends `{sourceKey, kpiName, newValue}` to a local WAL file. On startup, the WAL is replayed to Redis. This handles the case where the normalizer crashes after computing the delta but before the Redis write completes. The WAL is truncated after successful replay.

**Multi-worker support**: Each normalizer pod operates independently against Redis. The Lua script is atomic per key, so concurrent pods writing different source keys have no contention. Same-key writes from different pods are serialized by Redis — this is correct because source keys include the instance ID, and the collector router ensures each instance is scraped by exactly one normalizer pod.

### Configuration

```yaml
counter_store:
  type: redis          # "bbolt" (default) or "redis"
  redis:
    addrs:
      - redis-0:6379
      - redis-1:6379
      - redis-2:6379
    password_env: ARGUS_REDIS_PASSWORD
    db: 0
    key_prefix: "argus:counter:"
    wal_path: /data/argus/counter-wal
```

### Migration path

1. Deploy Redis alongside existing bbolt-backed Argus
2. Set `counter_store.type: redis` in config
3. On first startup with Redis, the store detects an existing bbolt file and bulk-loads all keys into Redis (one-time migration)
4. After migration, bbolt file can be removed
5. Rollback: set `counter_store.type: bbolt` — the bbolt file is recreated from scratch (loses delta state for one scrape cycle, acceptable)

### Metrics (additive to v0.3)

- `argus_counter_store_redis_latency_seconds` histogram — per-operation latency
- `argus_counter_store_wal_entries` gauge — pending WAL entries (should be 0 in steady state; non-zero indicates Redis write failures)
- `argus_counter_store_migration_keys_total` counter — keys migrated from bbolt

## 4. Interface Stability Guarantee

The `CounterStore` interface is stable:

```go
type CounterStore interface {
    Get(sourceKey, kpiName string) (float64, bool)
    Put(sourceKey, kpiName string, value float64)
    Close() error
}
```

Both `BoltStore` and the planned `RedisStore` implement this interface. The normalization engine does not know or care which backend is active. Upgrading from bbolt to Redis requires only a config change — no code changes, no interface changes, no recompilation.

This is a deliberate design choice: the counter store is a hot-path component (called once per KPI per scrape), and the interface must remain minimal to avoid forcing performance-sensitive abstractions onto implementations.
