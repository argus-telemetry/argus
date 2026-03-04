// RedisStore implements CounterStore backed by Redis with Lua-atomic counter
// updates. Supports multi-writer access (safe for concurrent normalizer pods)
// and automatic key expiry via TTL. See docs/architecture/counter-store-evolution.md.
package normalizer

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/argus-5g/argus/internal/telemetry"
	"github.com/redis/go-redis/v9"
)

// counterLuaScript is the atomic swap Lua script from the architecture doc.
// Collapses Get+Put into a single Redis round trip, eliminating the race window
// between delta computation and persistence. The normalizer computes
// delta = newValue - tonumber(prev) client-side.
//
// KEYS[1] = counter key
// ARGV[1] = new counter value
// ARGV[2] = TTL in seconds
//
// Returns: previous value (nil if first scrape), counter_reset flag (0 or 1)
const counterLuaScript = `
local prev = redis.call('GET', KEYS[1])
redis.call('SET', KEYS[1], ARGV[1], 'EX', tonumber(ARGV[2]))
local reset = 0
if prev ~= false then
  if tonumber(ARGV[1]) < tonumber(prev) then
    reset = 1
  end
end
return {prev, reset}
`

// RedisStore persists counter values in Redis. Each key is a string mapping
// sourceKey:kpiName → float64 value. Keys expire at 2x scrape interval so
// stale counters are cleaned automatically.
type RedisStore struct {
	client    redis.UniversalClient
	scriptSHA string
	keyTTL    time.Duration
	metrics   *telemetry.Metrics
	ctx       context.Context
}

// RedisStoreConfig holds Redis connection and behavior parameters.
type RedisStoreConfig struct {
	Addr         string        `yaml:"addr"`
	Password     string        `yaml:"password"`
	DB           int           `yaml:"db"`
	KeyTTL       time.Duration `yaml:"key_ttl"`
	DialTimeout  time.Duration `yaml:"dial_timeout"`
	ReadTimeout  time.Duration `yaml:"read_timeout"`
	WriteTimeout time.Duration `yaml:"write_timeout"`
	PoolSize     int           `yaml:"pool_size"`
}

// RedisStoreOption configures optional RedisStore behavior.
type RedisStoreOption func(*RedisStore)

// WithRedisMetrics attaches telemetry metrics to the RedisStore.
func WithRedisMetrics(m *telemetry.Metrics) RedisStoreOption {
	return func(rs *RedisStore) {
		rs.metrics = m
	}
}

// NewRedisStore connects to Redis, loads the Lua script via SCRIPT LOAD, and
// returns a ready-to-use RedisStore. Fails fast if Redis is unreachable or the
// script cannot be loaded — no silent fallback to EVAL.
func NewRedisStore(cfg RedisStoreConfig, opts ...RedisStoreOption) (*RedisStore, error) {
	if cfg.KeyTTL == 0 {
		cfg.KeyTTL = 120 * time.Second
	}
	if cfg.DialTimeout == 0 {
		cfg.DialTimeout = 5 * time.Second
	}
	if cfg.ReadTimeout == 0 {
		cfg.ReadTimeout = 3 * time.Second
	}
	if cfg.WriteTimeout == 0 {
		cfg.WriteTimeout = 3 * time.Second
	}
	if cfg.PoolSize == 0 {
		cfg.PoolSize = 10
	}

	client := redis.NewClient(&redis.Options{
		Addr:         cfg.Addr,
		Password:     cfg.Password,
		DB:           cfg.DB,
		DialTimeout:  cfg.DialTimeout,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		PoolSize:     cfg.PoolSize,
	})

	rs := &RedisStore{
		client: client,
		keyTTL: cfg.KeyTTL,
		ctx:    context.Background(),
	}
	for _, opt := range opts {
		opt(rs)
	}

	// Verify connectivity.
	ctx, cancel := context.WithTimeout(rs.ctx, cfg.DialTimeout)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		if rs.metrics != nil {
			rs.metrics.CounterStoreErrors.WithLabelValues("redis", "open").Inc()
		}
		return nil, fmt.Errorf("redis ping %s: %w", cfg.Addr, err)
	}

	// Load Lua script via SCRIPT LOAD. We use EVALSHA exclusively at runtime —
	// if the script is evicted, the operation fails rather than falling back to
	// EVAL, so we detect misconfigured Redis instances immediately.
	sha, err := client.ScriptLoad(ctx, counterLuaScript).Result()
	if err != nil {
		if rs.metrics != nil {
			rs.metrics.CounterStoreErrors.WithLabelValues("redis", "script_load").Inc()
		}
		return nil, fmt.Errorf("redis SCRIPT LOAD: %w", err)
	}
	rs.scriptSHA = sha

	return rs, nil
}

// redisKey builds the Redis key for a counter.
// Format: counter:{sourceKey}:{kpiName}
func redisKey(sourceKey, kpiName string) string {
	return "counter:" + sourceKey + ":" + kpiName
}

func (r *RedisStore) Get(sourceKey, kpiName string) (float64, bool) {
	val, err := r.client.Get(r.ctx, redisKey(sourceKey, kpiName)).Result()
	if err == redis.Nil {
		return 0, false
	}
	if err != nil {
		if r.metrics != nil {
			r.metrics.CounterStoreErrors.WithLabelValues("redis", "read").Inc()
		}
		return 0, false
	}
	f, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

func (r *RedisStore) Put(sourceKey, kpiName string, value float64) {
	key := redisKey(sourceKey, kpiName)
	ttlSec := int(r.keyTTL.Seconds())

	start := time.Now()
	result, err := r.client.EvalSha(r.ctx, r.scriptSHA,
		[]string{key},
		strconv.FormatFloat(value, 'f', -1, 64),
		strconv.Itoa(ttlSec),
	).Result()
	elapsed := time.Since(start)

	if r.metrics != nil {
		r.metrics.CounterLuaEvalDuration.WithLabelValues().Observe(elapsed.Seconds())
	}

	if err != nil {
		if r.metrics != nil {
			r.metrics.CounterStoreErrors.WithLabelValues("redis", "write").Inc()
		}
		return
	}

	if r.metrics != nil {
		r.metrics.CounterStatePersisted.WithLabelValues("redis").Inc()
	}

	// Parse Lua script return: {prev, reset_flag}
	arr, ok := result.([]interface{})
	if !ok || len(arr) < 2 {
		return
	}

	// Check counter reset flag.
	if resetFlag, ok := arr[1].(int64); ok && resetFlag == 1 {
		if r.metrics != nil {
			// Extract vendor and nf_type from sourceKey (format: "vendor:nfType:instanceID").
			vendor, nfType := parseSourceKeyLabels(sourceKey)
			r.metrics.CounterResetTotal.WithLabelValues(vendor, nfType).Inc()
		}
	}
}

func (r *RedisStore) Close() error {
	return r.client.Close()
}

// ScriptSHA returns the loaded Lua script SHA for testing EVALSHA is used.
func (r *RedisStore) ScriptSHA() string {
	return r.scriptSHA
}

// parseSourceKeyLabels extracts vendor and nf_type from a sourceKey
// formatted as "vendor:nfType:instanceID".
func parseSourceKeyLabels(sourceKey string) (vendor, nfType string) {
	vendor = "unknown"
	nfType = "unknown"
	first := -1
	second := -1
	for i, c := range sourceKey {
		if c == ':' {
			if first == -1 {
				first = i
			} else {
				second = i
				break
			}
		}
	}
	if first > 0 {
		vendor = sourceKey[:first]
	}
	if first > 0 && second > first {
		nfType = sourceKey[first+1 : second]
	}
	return
}
