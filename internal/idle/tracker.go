package idle

import (
	"log"
	"runtime"
	"sync"
	"time"

	"github.com/mbianchidev/sql-not-so-lite/internal/store"
)

type Tracker struct {
	manager       *store.Manager
	timeout       time.Duration
	checkInterval time.Duration
	stopCh        chan struct{}
	wg            sync.WaitGroup
	isDormant     bool
	mu            sync.RWMutex
}

func NewTracker(manager *store.Manager, timeout, checkInterval time.Duration) *Tracker {
	return &Tracker{
		manager:       manager,
		timeout:       timeout,
		checkInterval: checkInterval,
		stopCh:        make(chan struct{}),
	}
}

func (t *Tracker) Start() {
	t.wg.Add(1)
	go t.loop()
}

func (t *Tracker) Stop() {
	close(t.stopCh)
	t.wg.Wait()
}

func (t *Tracker) IsDormant() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.isDormant
}

func (t *Tracker) loop() {
	defer t.wg.Done()

	ticker := time.NewTicker(t.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-t.stopCh:
			return
		case <-ticker.C:
			t.check()
		}
	}
}

func (t *Tracker) check() {
	closed := t.manager.CloseIdle(t.timeout)
	if closed > 0 {
		log.Printf("idle tracker: closed %d idle database(s)", closed)
	}

	active := t.manager.ActiveCount()

	t.mu.Lock()
	wasDormant := t.isDormant
	t.isDormant = active == 0
	t.mu.Unlock()

	if t.isDormant && !wasDormant {
		log.Println("idle tracker: entering dormant mode")
		runtime.GC()
	} else if !t.isDormant && wasDormant {
		log.Println("idle tracker: waking from dormant mode")
	}
}
