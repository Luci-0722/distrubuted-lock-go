package redis

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"time"

	"github.com/go-redis/redis/v8"
)

type Lock struct {
	client   *redis.Client
	key      string
	value    string
	ttl      time.Duration
	obtained bool
}

func NewRedisLock(client *redis.Client, key string, ttl time.Duration) *Lock {
	return &Lock{
		client: client,
		key:    key,
		ttl:    ttl,
	}
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

func (rl *Lock) Lock(ctx context.Context) error {
	if err := rl.generateValue(); err != nil {
		return err
	}

	for {
		ok, err := rl.client.SetNX(ctx, rl.key, rl.value, rl.ttl).Result()
		if err != nil {
			return err
		}
		if ok {
			rl.obtained = true
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
	if err := rl.generateValue(); err != nil {
		return false, err
	}

	ok, err := rl.client.SetNX(ctx, rl.key, rl.value, rl.ttl).Result()
	if err != nil {
		return false, err
	}

	rl.obtained = ok
	return ok, nil
}

func (rl *Lock) Unlock(ctx context.Context) error {
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
	return nil
}
