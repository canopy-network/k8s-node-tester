package main

// Broadcaster fans out values of type T from a single source channel to multiple subscribers (no buffers).
type Broadcaster[T any] struct {
	subs []chan T
}

// NewBroadcaster creates a broadcaster that relays values from src to all subscribers.
// When src closes, all subscriber channels are closed.
func NewBroadcaster[T any](src <-chan T, subscribers int) *Broadcaster[T] {
	b := &Broadcaster[T]{subs: make([]chan T, subscribers)}
	for i := range subscribers {
		b.subs[i] = make(chan T)
	}
	go func() {
		for v := range src {
			for _, ch := range b.subs {
				select {
				case ch <- v:
					// sent successfully
				default:
					// channel full or not ready, skip
				}
			}
		}
		for _, ch := range b.subs {
			close(ch)
		}
	}()
	return b
}

// Channels returns the subscriber receive-only channels.
func (b *Broadcaster[T]) Channels() []<-chan T {
	outs := make([]<-chan T, len(b.subs))
	for i, ch := range b.subs {
		outs[i] = ch
	}
	return outs
}
