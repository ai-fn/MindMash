package sync

import (
	"context"
	"errors"
	"fmt"
	"strings"
	gosync "sync"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

const notifyPrefix = "sync:notify:"

// errHubDown means the replica's notify subscription isn't live, so a new SSE stream would never be nudged.
var errHubDown = errors.New("sync: notify hub is not running")

// notifyHub fans one PSUBSCRIBE per replica out to its SSE streams, instead of a Redis connection per stream.
type notifyHub struct {
	mu   gosync.Mutex
	up   bool
	subs map[string]map[chan struct{}]struct{}
}

func newNotifyHub() *notifyHub {
	return &notifyHub{subs: make(map[string]map[chan struct{}]struct{})}
}

func notifyChannel(userID string) string { return notifyPrefix + userID }

const (
	// hubRetry spaces attempts to establish the subscription while Redis is unreachable.
	hubRetry = time.Second
	// hubSubscribeTimeout bounds the subscribe handshake: go-redis sets no read deadline on it.
	hubSubscribeTimeout = 5 * time.Second
)

// RunNotifyHub holds the replica's pattern subscription until ctx is cancelled,
// retrying while Redis is down; meanwhile new streams get errHubDown (503).
func (s *Service) RunNotifyHub(ctx context.Context) error {
	down := false
	for {
		err := s.runNotifyHub(ctx, func() {
			if down {
				s.log.Info("sync: notify hub recovered")
				down = false
			}
		})
		if ctx.Err() != nil {
			return nil
		}
		if !down {
			s.log.Warn("sync: notify hub down, retrying", zap.Error(err))
			down = true
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(hubRetry):
		}
	}
}

// runNotifyHub returns when ctx ends or the subscription fails, closing every
// stream's channel so open SSE responses end and shutdown isn't held up.
func (s *Service) runNotifyHub(ctx context.Context, onUp func()) error {
	ps := s.rdb.PSubscribe(ctx, notifyPrefix+"*")
	defer func() { _ = ps.Close() }()
	defer s.hub.stop()
	subCtx, cancel := context.WithTimeout(ctx, hubSubscribeTimeout)
	_, err := ps.Receive(subCtx)
	cancel()
	if err != nil {
		return fmt.Errorf("sync: notify hub subscribe: %w", err)
	}
	s.hub.start()
	onUp()

	msgs := ps.ChannelWithSubscriptions()
	for {
		select {
		case <-ctx.Done():
			return nil
		case m, open := <-msgs:
			if !open {
				return errors.New("sync: notify hub subscription closed")
			}
			switch m := m.(type) {
			case *redis.Message:
				s.hub.nudge(strings.TrimPrefix(m.Channel, notifyPrefix))
			case *redis.Subscription:
				// Re-subscribed after a reconnect: nudges sent meanwhile were lost.
				s.log.Info("sync: notify hub resubscribed")
				s.hub.nudgeAll()
			}
		}
	}
}

func (h *notifyHub) start() {
	h.mu.Lock()
	h.up = true
	h.mu.Unlock()
}

func (h *notifyHub) stop() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.up = false
	for userID, set := range h.subs {
		for ch := range set {
			close(ch)
		}
		delete(h.subs, userID)
	}
}

// subscribe registers a stream for userID; the returned func is idempotent.
func (h *notifyHub) subscribe(userID string) (<-chan struct{}, func(), error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.up {
		return nil, nil, errHubDown
	}
	ch := make(chan struct{}, 1)
	if h.subs[userID] == nil {
		h.subs[userID] = make(map[chan struct{}]struct{})
	}
	h.subs[userID][ch] = struct{}{}

	unsubscribe := func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		set := h.subs[userID]
		if _, ok := set[ch]; !ok {
			return // already removed by an earlier call or by stop
		}
		delete(set, ch)
		if len(set) == 0 {
			delete(h.subs, userID)
		}
		close(ch)
	}
	return ch, unsubscribe, nil
}

// nudge coalesces: a stream that hasn't drained its last nudge needs no second one.
func (h *notifyHub) nudge(userID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	poke(h.subs[userID])
}

func (h *notifyHub) nudgeAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, set := range h.subs {
		poke(set)
	}
}

func poke(set map[chan struct{}]struct{}) {
	for ch := range set {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
