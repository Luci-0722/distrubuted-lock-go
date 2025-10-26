package redis

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
)

type Lock struct {
	mu           sync.RWMutex
	client       *redis.Client
	key          string
	value        string
	ttl          time.Duration
	obtained     bool
	stopWatchdog chan struct{}
}

func NewRedisLock(client *redis.Client, key string, ttl time.Duration) *Lock {
	lock := &Lock{
		client: client,
		key:    key,
		ttl:    ttl,
	}
	lock.generateValue()
	return lock
}

func (rl *Lock) generateValue() error {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		return err
	}
	rl.value = base64.StdEncoding.EncodeToString(b)
	return nil
}

// startWatchdog 启动看门狗，用于续期锁
func (rl *Lock) startWatchdog() {
	go func() {
		ticker := time.NewTicker(rl.ttl / 3)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				rl.mu.RLock()
				if rl.obtained {
					_, err := rl.client.Expire(context.Background(), rl.key, rl.ttl).Result()
					if err != nil {
						// 处理续期失败，比如记录日志
						log.Printf("failed to expire lock: %v", err)
					}
				}
				rl.mu.RUnlock()
			case <-rl.stopWatchdog:
				return
			}
		}
	}()
}

func (rl *Lock) Lock(ctx context.Context) error {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	for {
		ok, err := rl.client.SetNX(ctx, rl.key, rl.value, rl.ttl).Result()
		if err != nil {
			return err
		}
		if ok {
			rl.obtained = true

			// 启动看门狗
			rl.stopWatchdog = make(chan struct{})
			rl.startWatchdog()
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
			// 继续重试
		}
	}
}

func (rl *Lock) TryLock(ctx context.Context) (bool, error) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	ok, err := rl.client.SetNX(ctx, rl.key, rl.value, rl.ttl).Result()
	if err != nil {
		return false, err
	}

	rl.obtained = ok
	// 如果成功获取锁，启动看门狗
	if ok {
		rl.stopWatchdog = make(chan struct{})
		rl.startWatchdog()
	}
	return ok, nil
}

func (rl *Lock) Unlock(ctx context.Context) error {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if !rl.obtained {
		return nil
	}

	// 使用Lua脚本保证原子性
	script := `
    if redis.call("get", KEYS[1]) == ARGV[1] then
        return redis.call("del", KEYS[1])
    else
        return 0
    end
    `

	result, err := rl.client.Eval(ctx, script, []string{rl.key}, rl.value).Result()
	if err != nil {
		return err
	}

	if result.(int64) == 0 {
		return errors.New("unlock failed: lock value mismatch or lock not exists")
	}

	rl.obtained = false
	// 停止看门狗
	close(rl.stopWatchdog)
	return nil
}

// Refresh 刷新锁的过期时间
func (rl *Lock) Refresh(ctx context.Context) error {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	if !rl.obtained {
		return errors.New("cannot refresh unobtained lock")
	}

	// 使用Lua脚本保证原子性，只有持有锁的客户端才能刷新
	script := `
    if redis.call("get", KEYS[1]) == ARGV[1] then
        return redis.call("pexpire", KEYS[1], ARGV[2])
    else
        return 0
    end
    `

	ttlMs := int64(rl.ttl / time.Millisecond)
	result, err := rl.client.Eval(ctx, script, []string{rl.key}, rl.value, ttlMs).Result()
	if err != nil {
		return err
	}

	if result.(int64) == 0 {
		return errors.New("refresh failed: lock value mismatch or lock not exists")
	}

	return nil
}

// GetTTL 获取锁的剩余过期时间
func (rl *Lock) GetTTL(ctx context.Context) (time.Duration, error) {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	if !rl.obtained {
		return 0, errors.New("cannot get TTL of unobtained lock")
	}

	// 检查锁是否存在并且值匹配
	currentValue, err := rl.client.Get(ctx, rl.key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return 0, errors.New("lock does not exist")
		}
		return 0, err
	}

	if currentValue != rl.value {
		return 0, errors.New("lock value mismatch")
	}

	// 获取剩余过期时间
	ttl, err := rl.client.TTL(ctx, rl.key).Result()
	if err != nil {
		return 0, err
	}

	return ttl, nil
}
