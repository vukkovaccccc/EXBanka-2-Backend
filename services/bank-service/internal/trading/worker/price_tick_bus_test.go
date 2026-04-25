package worker

import (
	"context"
	"testing"
	"time"
)

func TestNewPriceTickBus_NotNil(t *testing.T) {
	bus := NewPriceTickBus()
	if bus == nil {
		t.Error("expected non-nil bus")
	}
}

func TestPriceTickBus_Subscribe_ReceivePublish(t *testing.T) {
	bus := NewPriceTickBus()
	ch := bus.Subscribe(1)
	bus.Publish(1, 10.5, 10.0)

	select {
	case tick := <-ch:
		if tick.ListingID != 1 {
			t.Errorf("expected listingID 1, got %d", tick.ListingID)
		}
		if tick.Ask != 10.5 {
			t.Errorf("expected Ask 10.5, got %f", tick.Ask)
		}
		if tick.Bid != 10.0 {
			t.Errorf("expected Bid 10.0, got %f", tick.Bid)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timed out waiting for tick")
	}
}

func TestPriceTickBus_Unsubscribe_NoMoreTicks(t *testing.T) {
	bus := NewPriceTickBus()
	ch := bus.Subscribe(2)
	bus.Unsubscribe(2, ch)
	bus.Publish(2, 5.0, 4.5)

	select {
	case tick := <-ch:
		t.Errorf("should not receive tick after unsubscribe, got %v", tick)
	case <-time.After(50 * time.Millisecond):
		// Expected: no tick received
	}
}

func TestPriceTickBus_MultipleSubscribers(t *testing.T) {
	bus := NewPriceTickBus()
	ch1 := bus.Subscribe(3)
	ch2 := bus.Subscribe(3)
	bus.Publish(3, 20.0, 19.5)

	for i, ch := range []chan PriceTick{ch1, ch2} {
		select {
		case tick := <-ch:
			if tick.Ask != 20.0 {
				t.Errorf("subscriber %d: expected Ask 20.0, got %f", i+1, tick.Ask)
			}
		case <-time.After(100 * time.Millisecond):
			t.Errorf("subscriber %d timed out", i+1)
		}
	}
}

func TestPriceTickBus_PublishDifferentListings(t *testing.T) {
	bus := NewPriceTickBus()
	ch1 := bus.Subscribe(10)
	ch2 := bus.Subscribe(20)

	bus.Publish(10, 100.0, 99.0)
	bus.Publish(20, 200.0, 199.0)

	select {
	case tick := <-ch1:
		if tick.Ask != 100.0 {
			t.Errorf("ch1 expected Ask 100, got %f", tick.Ask)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("ch1 timed out")
	}

	select {
	case tick := <-ch2:
		if tick.Ask != 200.0 {
			t.Errorf("ch2 expected Ask 200, got %f", tick.Ask)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("ch2 timed out")
	}
}

func TestPriceTickBus_PublishNoSubscribers_NoPanic(t *testing.T) {
	bus := NewPriceTickBus()
	// Should not panic even if no subscribers
	bus.Publish(999, 1.0, 0.9)
}

func TestPriceTickBus_Unsubscribe_LastSubscriber_DeletesKey(t *testing.T) {
	bus := NewPriceTickBus()
	ch := bus.Subscribe(5)
	bus.Unsubscribe(5, ch)

	// Subscribe again and make sure it still works
	ch2 := bus.Subscribe(5)
	bus.Publish(5, 3.0, 2.9)

	select {
	case tick := <-ch2:
		if tick.Ask != 3.0 {
			t.Errorf("expected Ask 3.0, got %f", tick.Ask)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timed out")
	}
}

// ─── WithIsClient / IsClientFromCtx ──────────────────────────────────────────

func TestWithIsClient_True(t *testing.T) {
	ctx := WithIsClient(context.Background(), true)
	if !IsClientFromCtx(ctx) {
		t.Error("expected IsClientFromCtx to return true")
	}
}

func TestWithIsClient_False(t *testing.T) {
	ctx := WithIsClient(context.Background(), false)
	if IsClientFromCtx(ctx) {
		t.Error("expected IsClientFromCtx to return false")
	}
}

func TestIsClientFromCtx_NotSet_ReturnsFalse(t *testing.T) {
	if IsClientFromCtx(context.Background()) {
		t.Error("unset context should return false")
	}
}
