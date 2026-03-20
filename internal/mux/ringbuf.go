package mux

import (
	"context"
	"sync"
)

// RawRingBuffer is a thread-safe ring buffer for raw IP packet frames.
// When the buffer is full, Push blocks until a consumer calls Pop,
// providing backpressure up the DTLS/TURN chain so that TCP flow
// control on the internet side naturally throttles senders.
type RawRingBuffer struct {
	mu     sync.Mutex
	buf    []*Frame
	cap    int
	head   int // index of oldest item
	count  int // number of items in buffer
	notify chan struct{}
	space  chan struct{} // semaphore: one token per free slot
	closed bool
}

// NewRawRingBuffer creates a ring buffer with the given capacity.
// The buffer blocks producers when full instead of evicting data.
func NewRawRingBuffer(capacity int) *RawRingBuffer {
	space := make(chan struct{}, capacity)
	for i := 0; i < capacity; i++ {
		space <- struct{}{}
	}
	return &RawRingBuffer{
		buf:    make([]*Frame, capacity),
		cap:    capacity,
		notify: make(chan struct{}, 1),
		space:  space,
	}
}

// Push adds a frame to the buffer. Blocks if the buffer is full until
// a slot is freed by Pop or the context is cancelled.
// Returns nil on success, context error if cancelled, or ErrClosed.
func (rb *RawRingBuffer) Push(ctx context.Context, f *Frame) error {
	// Wait for a free slot (backpressure).
	select {
	case <-rb.space:
	case <-ctx.Done():
		return ctx.Err()
	}

	rb.mu.Lock()
	if rb.closed {
		rb.mu.Unlock()
		// Return the token — buffer is closed.
		select {
		case rb.space <- struct{}{}:
		default:
		}
		return errClosed
	}
	idx := (rb.head + rb.count) % rb.cap
	rb.buf[idx] = f
	rb.count++
	rb.mu.Unlock()

	// Non-blocking signal to consumer.
	select {
	case rb.notify <- struct{}{}:
	default:
	}
	return nil
}

var errClosed = context.Canceled // reuse standard error

// Pop removes and returns the oldest frame. Returns nil, false if empty.
func (rb *RawRingBuffer) Pop() (*Frame, bool) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	if rb.count == 0 {
		return nil, false
	}
	f := rb.buf[rb.head]
	rb.buf[rb.head] = nil // help GC
	rb.head = (rb.head + 1) % rb.cap
	rb.count--

	// Return a slot token to unblock a waiting Push.
	select {
	case rb.space <- struct{}{}:
	default:
	}
	return f, true
}

// Ready returns a channel that is signaled when frames are available.
// The channel is closed when the buffer is closed.
func (rb *RawRingBuffer) Ready() <-chan struct{} {
	return rb.notify
}

// Drain discards all buffered frames and returns the count of discarded frames.
// Unblocks any producers waiting in Push.
func (rb *RawRingBuffer) Drain() int {
	rb.mu.Lock()
	n := rb.count
	for i := 0; i < rb.cap; i++ {
		rb.buf[i] = nil
	}
	rb.head = 0
	rb.count = 0
	rb.mu.Unlock()

	// Return tokens for drained slots.
	for i := 0; i < n; i++ {
		select {
		case rb.space <- struct{}{}:
		default:
		}
	}
	return n
}

// Len returns the current number of frames in the buffer.
func (rb *RawRingBuffer) Len() int {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return rb.count
}

// Close marks the buffer as closed and closes the notify channel.
// Any blocked Push calls will be unblocked via the space channel.
func (rb *RawRingBuffer) Close() {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	if !rb.closed {
		rb.closed = true
		close(rb.notify)
		// Flood space channel to unblock all waiting Pushes.
		// They will see rb.closed and return errClosed.
		for i := 0; i < rb.cap; i++ {
			select {
			case rb.space <- struct{}{}:
			default:
			}
		}
	}
}

// IsClosed reports whether the buffer has been closed.
func (rb *RawRingBuffer) IsClosed() bool {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return rb.closed
}
