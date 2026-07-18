// Package hang is a fixture used by hangdog's end-to-end test. TestHang blocks
// forever; TestMain keeps a background goroutine alive so the Go runtime's
// deadlock detector stays quiet, exactly as a real service with a worker would.
package hang

import (
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	go func() {
		for {
			time.Sleep(time.Second)
		}
	}()
	m.Run()
}

func TestFast(t *testing.T) {
	t.Parallel()
	time.Sleep(20 * time.Millisecond)
}

func TestHang(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	go worker(started)
	<-started
	select {} // blocks forever; the keepalive goroutine hides it from the deadlock detector
}

func worker(started chan struct{}) {
	close(started)
	for {
		time.Sleep(time.Second)
	}
}
