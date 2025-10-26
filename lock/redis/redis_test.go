package redis

import (
	"context"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"sync"
	"testing"
	"time"
)

func TestRedisLock(t *testing.T) {
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	lock := NewRedisLock(client, "test_key", 10*time.Second)

	ctx := context.Background()

	// 测试加锁解锁
	err := lock.Lock(ctx)
	assert.NoError(t, err)

	err = lock.Unlock(ctx)
	assert.NoError(t, err)

	// 测试锁互斥
	var wg sync.WaitGroup
	counter := 0

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l := NewRedisLock(client, "counter_lock", 5*time.Second)
			if err := l.Lock(ctx); err == nil {
				counter++
				l.Unlock(ctx)

			}
		}()
	}

	wg.Wait()
	assert.Equal(t, 10, counter)
}
