package main

// Broadcaster fans out ints from a single source channel to multiple subscribers (no buffers).
type Broadcaster struct {
	subs []chan int
}

// NewBroadcaster creates a broadcaster that relays values from src to all subscribers.
// When src closes, all subscriber channels are closed.
func NewBroadcaster(src <-chan int, subscribers int) *Broadcaster {
	b := &Broadcaster{subs: make([]chan int, subscribers)}
	for i := range subscribers {
		b.subs[i] = make(chan int) // unbuffered
	}
	go func() {
		for v := range src {
			for _, ch := range b.subs {
				ch <- v // blocking send to ensure strict delivery
			}
		}
		for _, ch := range b.subs {
			close(ch)
		}
	}()
	return b
}

// Channels returns the subscriber receive-only channels.
func (b *Broadcaster) Channels() []<-chan int {
	outs := make([]<-chan int, len(b.subs))
	for i, ch := range b.subs {
		outs[i] = ch
	}
	return outs
}
