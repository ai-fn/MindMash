package sync

import (
	"errors"
	"testing"
)

func mustSubscribe(t *testing.T, h *notifyHub, userID string) <-chan struct{} {
	t.Helper()
	ch, unsub, err := h.subscribe(userID)
	if err != nil {
		t.Fatalf("subscribe %s: %v", userID, err)
	}
	t.Cleanup(unsub)
	return ch
}

func TestNotifyHub_RejectsStreamsWhileDown(t *testing.T) {
	h := newNotifyHub()
	if _, _, err := h.subscribe("u"); !errors.Is(err, errHubDown) {
		t.Fatalf("subscribe before start: err = %v, want errHubDown", err)
	}
	h.start()
	h.stop()
	if _, _, err := h.subscribe("u"); !errors.Is(err, errHubDown) {
		t.Fatalf("subscribe after stop: err = %v, want errHubDown", err)
	}
	h.start()
	mustSubscribe(t, h, "u")
}

func TestNotifyHub_StopClosesStreams(t *testing.T) {
	h := newNotifyHub()
	h.start()
	ch, unsub, err := h.subscribe("u")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	h.stop()
	if _, open := <-ch; open {
		t.Fatal("want channel closed on stop")
	}
	h.start()
	fresh := mustSubscribe(t, h, "u")
	unsub() // a stale unsubscribe must neither double-close nor drop the new stream
	h.nudge("u")
	<-fresh
}

func TestNotifyHub_NudgeCoalescesPerStream(t *testing.T) {
	h := newNotifyHub()
	h.start()
	a1, a2 := mustSubscribe(t, h, "a"), mustSubscribe(t, h, "a")
	b := mustSubscribe(t, h, "b")

	h.nudge("a")
	h.nudge("a")
	for _, ch := range []<-chan struct{}{a1, a2} {
		<-ch
		select {
		case <-ch:
			t.Fatal("two nudges before a drain must coalesce into one")
		default:
		}
	}
	select {
	case <-b:
		t.Fatal("b must not receive a's nudge")
	default:
	}

	h.nudgeAll()
	<-b
}
