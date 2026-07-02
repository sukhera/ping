package worker

import (
	"context"
	"testing"
)

// A nil client and a nil receiver must both make Write a safe no-op — the
// heartbeat is fail-open, exactly like store.Allow with a nil Redis client.
func TestHeartbeat_NilClientIsNoop(t *testing.T) {
	NewHeartbeat(nil).Write(context.Background(), "scheduler") // must not panic
}

func TestHeartbeat_NilReceiverIsNoop(t *testing.T) {
	var h *Heartbeat
	h.Write(context.Background(), "scheduler") // must not panic
}
