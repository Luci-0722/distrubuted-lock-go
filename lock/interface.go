package lock

import (
	"context"
	"time"
)

// DistributedLock 分布式锁接口
type DistributedLock interface {
	// Lock 获取锁
	Lock(ctx context.Context) error

	// TryLock 尝试获取锁
	TryLock(ctx context.Context) (bool, error)

	// Unlock 释放锁
	Unlock(ctx context.Context) error

	// Refresh 续期
	Refresh(ctx context.Context) error

	// GetTTL 获取剩余时间
	GetTTL(ctx context.Context) (time.Duration, error)
}

// LockOptions 锁选项
type LockOptions struct {
	Key        string
	Value      string
	TTL        time.Duration
	WaitTime   time.Duration
	RetryCount int
}
