package redis

import (
	"context"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"sync"
	"testing"
	"time"
)

func TestRedisLockBasic(t *testing.T) {
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	// 清理测试前可能存在的键
	ctx := context.Background()
	client.Del(ctx, "test_key")

	lock := NewRedisLock(client, "test_key", 10*time.Second)

	// 测试加锁
	err := lock.Lock(ctx)
	assert.NoError(t, err)
	assert.True(t, lock.obtained)

	// 测试解锁
	err = lock.Unlock(ctx)
	assert.NoError(t, err)
	assert.False(t, lock.obtained)

	// 测试重复解锁
	err = lock.Unlock(ctx)
	assert.NoError(t, err)
}

func TestRedisLockTryLock(t *testing.T) {
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	// 清理测试前可能存在的键
	ctx := context.Background()
	client.Del(ctx, "try_lock_key")

	// 第一个客户端获取锁
	lock1 := NewRedisLock(client, "try_lock_key", 10*time.Second)
	success, err := lock1.TryLock(ctx)
	assert.NoError(t, err)
	assert.True(t, success)
	assert.True(t, lock1.obtained)

	// 第二个客户端尝试获取同一把锁，应该失败
	lock2 := NewRedisLock(client, "try_lock_key", 10*time.Second)
	success, err = lock2.TryLock(ctx)
	assert.NoError(t, err)
	assert.False(t, success)
	assert.False(t, lock2.obtained)

	// 第一个客户端释放锁
	err = lock1.Unlock(ctx)
	assert.NoError(t, err)

	// 第二个客户端再次尝试获取锁，应该成功
	success, err = lock2.TryLock(ctx)
	assert.NoError(t, err)
	assert.True(t, success)
	assert.True(t, lock2.obtained)

	// 清理
	lock2.Unlock(ctx)
}

func TestRedisLockRefresh(t *testing.T) {
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	// 清理测试前可能存在的键
	ctx := context.Background()
	client.Del(ctx, "refresh_lock_key")

	// 设置一个短TTL以便测试
	lock := NewRedisLock(client, "refresh_lock_key", 2*time.Second)

	// 获取锁
	err := lock.Lock(ctx)
	assert.NoError(t, err)

	// 等待一段时间，但不要超过TTL
	time.Sleep(1 * time.Second)

	// 刷新锁
	err = lock.Refresh(ctx)
	assert.NoError(t, err)

	// 再次等待，总时间超过原始TTL，但小于刷新后的TTL
	time.Sleep(1 * time.Second)

	// 检查锁是否仍然有效
	value, err := client.Get(ctx, "refresh_lock_key").Result()
	assert.NoError(t, err)
	assert.Equal(t, lock.value, value)

	// 清理
	lock.Unlock(ctx)
}

func TestRedisLockGetTTL(t *testing.T) {
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	// 清理测试前可能存在的键
	ctx := context.Background()
	client.Del(ctx, "ttl_lock_key")

	// 设置一个5秒的TTL
	lock := NewRedisLock(client, "ttl_lock_key", 5*time.Second)

	// 获取锁
	err := lock.Lock(ctx)
	assert.NoError(t, err)

	// 获取TTL，应该接近5秒
	ttl, err := lock.GetTTL(ctx)
	assert.NoError(t, err)
	assert.True(t, ttl > 4*time.Second && ttl <= 5*time.Second)

	// 等待一段时间
	time.Sleep(1 * time.Second)

	// 再次获取TTL，应该减少约1秒
	newTTL, err := lock.GetTTL(ctx)
	assert.NoError(t, err)
	assert.True(t, newTTL >= 3*time.Second && newTTL <= 4*time.Second)

	// 清理
	lock.Unlock(ctx)

	// 解锁后再获取TTL应该失败
	ttl, err = lock.GetTTL(ctx)
	assert.Error(t, err)
}

func TestRedisLockMutualExclusion(t *testing.T) {
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	// 清理测试前可能存在的键
	ctx := context.Background()
	client.Del(ctx, "mutex_lock_key")

	// 测试锁互斥
	var wg sync.WaitGroup
	counter := 0
	mutex := sync.Mutex{}

	// 创建10个goroutine，每个都尝试获取锁并增加计数器
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l := NewRedisLock(client, "mutex_lock_key", 5*time.Second)
			if err := l.Lock(ctx); err == nil {
				// 增加计数器前加本地锁，确保计数准确
				mutex.Lock()
				counter++
				mutex.Unlock()
				// 模拟一些工作
				time.Sleep(100 * time.Millisecond)
				l.Unlock(ctx)
			}
		}()
	}

	wg.Wait()
	// 确保所有10个goroutine都成功获取了锁
	assert.Equal(t, 10, counter)
}

func TestRedisLockWatchdog(t *testing.T) {
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	// 清理测试前可能存在的键
	ctx := context.Background()
	client.Del(ctx, "watchdog_lock_key")

	// 设置一个短TTL以便测试看门狗
	lock := NewRedisLock(client, "watchdog_lock_key", 3*time.Second)

	// 获取锁
	err := lock.Lock(ctx)
	assert.NoError(t, err)

	// 等待足够长的时间，超过TTL，但看门狗应该会自动续期
	time.Sleep(5 * time.Second)

	// 检查锁是否仍然存在并且值正确
	value, err := client.Get(ctx, "watchdog_lock_key").Result()
	assert.NoError(t, err)
	assert.Equal(t, lock.value, value)

	// 尝试解锁，应该成功
	err = lock.Unlock(ctx)
	assert.NoError(t, err)
}

func TestRedisLockNotOwned(t *testing.T) {
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	// 清理测试前可能存在的键
	ctx := context.Background()
	client.Del(ctx, "not_owned_lock_key")

	// 第一个客户端获取锁
	lock1 := NewRedisLock(client, "not_owned_lock_key", 10*time.Second)
	err := lock1.Lock(ctx)
	assert.NoError(t, err)

	// 第二个客户端尝试获取的锁
	lock2 := NewRedisLock(client, "not_owned_lock_key", 10*time.Second)
	ok, err := lock2.TryLock(ctx)
	assert.False(t, ok)

	// 第一个客户端仍然可以正常解锁
	err = lock1.Unlock(ctx)
	assert.NoError(t, err)
}
