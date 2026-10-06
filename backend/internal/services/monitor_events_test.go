package services

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// fakeEventsStep scripts one subscription. err fails it at once (the daemon
// is unreachable); otherwise msgs are delivered, then tailErr ends the stream
// (a daemon restart) or, when nil, the stream stays open until ctx is done.
type fakeEventsStep struct {
	err     error
	msgs    []events.Message
	tailErr error
}

// fakeEventsSource mimics client.Events: the error is sent on a 1-buffered
// channel that is then closed, and the message channel is never closed.
type fakeEventsSource struct {
	mu         sync.Mutex
	steps      []fakeEventsStep
	pings      []error
	subscribes int
}

func (f *fakeEventsSource) Events(ctx context.Context, _ events.ListOptions) (<-chan events.Message, <-chan error) {
	msgs := make(chan events.Message)
	errs := make(chan error, 1)

	f.mu.Lock()
	f.subscribes++
	var step fakeEventsStep
	if len(f.steps) > 0 {
		step = f.steps[0]
		f.steps = f.steps[1:]
	}
	f.mu.Unlock()

	go func() {
		defer close(errs)
		if step.err != nil {
			errs <- step.err
			return
		}
		for _, m := range step.msgs {
			select {
			case msgs <- m:
			case <-ctx.Done():
				errs <- ctx.Err()
				return
			}
		}
		if step.tailErr != nil {
			errs <- step.tailErr
			return
		}
		<-ctx.Done()
		errs <- ctx.Err()
	}()

	return msgs, errs
}

func (f *fakeEventsSource) Ping(context.Context) (types.Ping, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.pings) == 0 {
		return types.Ping{}, nil
	}
	err := f.pings[0]
	f.pings = f.pings[1:]
	return types.Ping{}, err
}

func (f *fakeEventsSource) subscribeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.subscribes
}

// recordingWait replaces the backoff sleep: it returns at once and records
// every delay it was asked for.
type recordingWait struct {
	mu     sync.Mutex
	delays []time.Duration
}

func (w *recordingWait) wait(ctx context.Context, d time.Duration) bool {
	w.mu.Lock()
	w.delays = append(w.delays, d)
	w.mu.Unlock()
	return ctx.Err() == nil
}

func (w *recordingWait) recorded() []time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]time.Duration(nil), w.delays...)
}

func startEvent(id string) events.Message {
	return events.Message{
		Type:   events.ContainerEventType,
		Action: events.ActionStart,
		Actor:  events.Actor{ID: id, Attributes: map[string]string{}},
		Time:   time.Now().Unix(),
	}
}

// nextEvent reads one event, failing if the channel closes or nothing arrives.
func nextEvent(t *testing.T, ch <-chan models.StackEvent) models.StackEvent {
	t.Helper()
	select {
	case ev, ok := <-ch:
		require.True(t, ok, "event channel closed: the listener gave up instead of reconnecting")
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("no event within 5s")
		return models.StackEvent{}
	}
}

func TestListenEvents_ReconnectsAfterStreamError(t *testing.T) {
	t.Parallel()

	src := &fakeEventsSource{steps: []fakeEventsStep{
		{err: io.EOF}, // the daemon restarted under the first stream
		{msgs: []events.Message{startEvent("aaaaaaaaaaaa0001")}},
	}}
	w := &recordingWait{}
	svc := &MonitorService{events: src, wait: w.wait}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := svc.ListenEvents(ctx)
	require.NoError(t, err)

	// The resync hint comes first: events missed during the gap are gone.
	ev := nextEvent(t, ch)
	assert.Equal(t, EventsResyncType, ev.Type)

	ev = nextEvent(t, ch)
	assert.Equal(t, "container_event", ev.Type)
	assert.Equal(t, "aaaaaaaaaaaa0001", ev.ContainerID)
	assert.Equal(t, 2, src.subscribeCount())
}

func TestListenEvents_BacksOffWhileDaemonDownAndResetsAfterRestore(t *testing.T) {
	t.Parallel()

	refused := errors.New("Cannot connect to the Docker daemon: connection refused")
	src := &fakeEventsSource{
		steps: []fakeEventsStep{
			{tailErr: io.EOF},
			{msgs: []events.Message{startEvent("bbbbbbbbbbbb0001")}, tailErr: io.ErrUnexpectedEOF},
			{msgs: []events.Message{startEvent("bbbbbbbbbbbb0002")}},
		},
		// Down for 8 probes during the first outage, then up; the second
		// outage recovers on its first probe.
		pings: []error{refused, refused, refused, refused, refused, refused, refused, refused, nil, nil},
	}
	w := &recordingWait{}
	svc := &MonitorService{events: src, wait: w.wait}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := svc.ListenEvents(ctx)
	require.NoError(t, err)

	var got []models.StackEvent
	for len(got) < 4 {
		got = append(got, nextEvent(t, ch))
	}
	assert.Equal(t, EventsResyncType, got[0].Type)
	assert.Equal(t, "bbbbbbbbbbbb0001", got[1].ContainerID)
	assert.Equal(t, EventsResyncType, got[2].Type)
	assert.Equal(t, "bbbbbbbbbbbb0002", got[3].ContainerID)
	assert.Equal(t, 3, src.subscribeCount())

	// Nominal 1s doubling to the 60s cap across nine waits, then back to 1s
	// for the second outage: the successful reconnect reset it.
	nominal := []time.Duration{1, 2, 4, 8, 16, 32, 60, 60, 60, 1}
	delays := w.recorded()
	require.Len(t, delays, len(nominal))
	for i, n := range nominal {
		d := n * time.Second
		assert.GreaterOrEqual(t, delays[i], d/2, "wait %d below its jitter floor", i)
		assert.LessOrEqual(t, delays[i], d, "wait %d above its nominal delay", i)
	}
}

func TestListenEvents_ClosesOnlyWhenContextEnds(t *testing.T) {
	t.Parallel()

	// The daemon never comes back. The listener keeps probing and must still
	// stop, and close its channel, once the context is cancelled.
	refused := errors.New("connection refused")
	pings := make([]error, 1000)
	for i := range pings {
		pings[i] = refused
	}
	src := &fakeEventsSource{steps: []fakeEventsStep{{err: refused}}, pings: pings}

	ctx, cancel := context.WithCancel(context.Background())
	waited := make(chan struct{}, 1)
	svc := &MonitorService{events: src, wait: func(ctx context.Context, _ time.Duration) bool {
		select {
		case waited <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return false
	}}

	ch, err := svc.ListenEvents(ctx)
	require.NoError(t, err)

	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("listener never entered backoff")
	}
	select {
	case _, ok := <-ch:
		t.Fatalf("channel yielded (ok=%v) while the daemon is down and ctx is live", ok)
	default:
	}

	cancel()
	select {
	case _, ok := <-ch:
		assert.False(t, ok, "expected the channel to close after cancel")
	case <-time.After(5 * time.Second):
		t.Fatal("channel not closed after ctx cancel")
	}
}
