package wire

import (
	"io"
	"log"
	"sync"
	"testing"
	"time"
)

// TestSubscribeUnsubscribeRace exercises the fan-out path under concurrent
// subscribe/unsubscribe churn while captures are recorded. It guards against the
// send-on-closed-channel panic: unsubscribe must NOT close the channel, since
// record() sends without holding the lock. Run with -race for full coverage.
func TestSubscribeUnsubscribeRace(t *testing.T) {
	srv, err := NewServer(t.TempDir(), log.New(io.Discard, "", 0), time.Now())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	var wg sync.WaitGroup

	// Recorders: hammer the fan-out path.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 2000; j++ {
				srv.Inject(Capture{Host: "api.anthropic.com", Endpoint: "anthropic/messages"})
			}
		}()
	}

	// Subscribers: repeatedly subscribe then immediately unsubscribe, creating
	// the exact window where record() could send on a just-removed channel.
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				_, unsub := srv.Subscribe()
				unsub()
			}
		}()
	}

	wg.Wait()
	// Reaching here without a panic is the assertion.
}
