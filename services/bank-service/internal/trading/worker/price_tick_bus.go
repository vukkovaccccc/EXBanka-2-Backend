package worker

import "sync"

// PriceTick carries the latest Ask and Bid prices for a single listing,
// published by ListingRefresherWorker after every successful price save.
// Consumed by waitForLimitActivation goroutines to avoid per-goroutine polling.
type PriceTick struct {
	ListingID int64
	Ask       float64
	Bid       float64
}

// PriceTickBus is a lightweight fan-out pub/sub bus keyed by listing ID.
// It satisfies the worker.PriceTickPublisher interface (structural typing)
// and can therefore be injected directly into ListingRefresherWorker.
//
// Concurrency: Subscribe/Unsubscribe hold a write lock; Publish holds a read
// lock and copies the subscriber slice before releasing, so channel sends are
// lock-free.  Sends are non-blocking: a full subscriber buffer drops the tick
// (the per-order fallback poll timer catches up on the next interval).
type PriceTickBus struct {
	mu   sync.RWMutex
	subs map[int64][]chan PriceTick
}

// NewPriceTickBus allocates an empty bus ready for use.
func NewPriceTickBus() *PriceTickBus {
	return &PriceTickBus{subs: make(map[int64][]chan PriceTick)}
}

// Publish satisfies the worker.PriceTickPublisher interface.
// It fans the tick out to every subscriber registered for listingID.
// The call is non-blocking: if a subscriber's buffer is full the tick is
// silently dropped for that subscriber.
func (b *PriceTickBus) Publish(listingID int64, ask, bid float64) {
	tick := PriceTick{ListingID: listingID, Ask: ask, Bid: bid}

	b.mu.RLock()
	chs := make([]chan PriceTick, len(b.subs[listingID]))
	copy(chs, b.subs[listingID])
	b.mu.RUnlock()

	for _, ch := range chs {
		select {
		case ch <- tick:
		default: // subscriber not ready — drop; fallback poll will retry
		}
	}
}

// Subscribe registers a new listener for listingID and returns a buffered
// channel that receives PriceTicks.  The caller must eventually call
// Unsubscribe to remove the entry from the internal map.
func (b *PriceTickBus) Subscribe(listingID int64) chan PriceTick {
	ch := make(chan PriceTick, 4) // buffered to absorb short bursts
	b.mu.Lock()
	b.subs[listingID] = append(b.subs[listingID], ch)
	b.mu.Unlock()
	return ch
}

// Unsubscribe removes ch from the bus.  The channel is intentionally NOT
// closed here: Publish may hold a stale copy of the slice and a concurrent
// send on a closed channel would panic.  The channel is small and will be
// GC'd once the subscriber goroutine exits.
func (b *PriceTickBus) Unsubscribe(listingID int64, ch chan PriceTick) {
	b.mu.Lock()
	defer b.mu.Unlock()
	chs := b.subs[listingID]
	for i, c := range chs {
		if c == ch {
			last := len(chs) - 1
			chs[i] = chs[last]
			b.subs[listingID] = chs[:last]
			break
		}
	}
	if len(b.subs[listingID]) == 0 {
		delete(b.subs, listingID)
	}
}
