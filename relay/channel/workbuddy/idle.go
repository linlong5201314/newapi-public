// idle.go 聊天 SSE 流中空闲监控：活跃吐数据续命不掐，静默超过阈值才断流。
// 移植自 _research/workbuddy2api-panel-main internal/upstream/idle.go。
package workbuddy

import (
	"io"
	"sync"
	"time"
)

// idleMonitoringBody 包在聊天 SSE body 外层：每次读到底层数据刷新 lastRead；
// 后台 goroutine 周期检查，静默超过 idle 就关闭底流，中断阻塞中的 Read。
type idleMonitoringBody struct {
	rc       io.ReadCloser
	mu       sync.Mutex
	lastRead time.Time
	stopOnce sync.Once
	stopCh   chan struct{}
	closer   func()
}

func (b *idleMonitoringBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	if n > 0 {
		b.mu.Lock()
		b.lastRead = time.Now()
		b.mu.Unlock()
	}
	return n, err
}

// Close 停掉后台 goroutine、关闭底流，保证无泄漏。
func (b *idleMonitoringBody) Close() error {
	b.stopOnce.Do(func() { close(b.stopCh) })
	return b.rc.Close()
}

func (b *idleMonitoringBody) idleFor() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	return time.Since(b.lastRead)
}

// monitorBody 若 idle<=0 直接返回原底流；否则包上流中空闲监控。
// closer 为掐流时调用的动作（通常 resp.Body.Close / cancel）。
func monitorBody(rc io.ReadCloser, idle time.Duration, closer func()) io.ReadCloser {
	if idle <= 0 {
		return rc
	}
	b := &idleMonitoringBody{
		rc:       rc,
		lastRead: time.Now(),
		stopCh:   make(chan struct{}),
		closer:   closer,
	}
	go func() {
		t := time.NewTicker(idleTick(idle))
		defer t.Stop()
		for {
			select {
			case <-b.stopCh:
				return
			case <-t.C:
				if b.idleFor() > idle {
					if b.closer != nil {
						b.closer()
					} else {
						_ = b.rc.Close()
					}
					return
				}
			}
		}
	}()
	return b
}

// idleTick 返回监控周期：idle/4，钳在 [10ms, 1s]。
func idleTick(idle time.Duration) time.Duration {
	d := idle / 4
	if d > time.Second {
		d = time.Second
	}
	if d < 10*time.Millisecond {
		d = 10 * time.Millisecond
	}
	return d
}
