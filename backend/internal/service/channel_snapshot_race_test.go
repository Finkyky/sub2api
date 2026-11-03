//go:build unit

package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/singleflight"
)

// The base matrix suppresses echo to isolate remote notifications. A separate
// case enables the production self-echo ordering and checks no publish loop.
type channelSnapshotNotifications struct {
	callback  func()
	echo      bool
	publishes int
}

func (n *channelSnapshotNotifications) NotifyUpdate(context.Context) error {
	n.publishes++
	if n.echo {
		n.callback()
	}
	return nil
}
func (n *channelSnapshotNotifications) SubscribeUpdates(_ context.Context, fn func()) {
	n.callback = fn
}

type channelSnapshotRaceFixture struct {
	notifications                *channelSnapshotNotifications
	secondEntered, secondRelease chan struct{}
	secondReleaseOnce            sync.Once
	service                      *ChannelService
	mu                           sync.Mutex
	channels                     []Channel
	listCalls, platformCalls     int
	entered                      chan struct{}
	release                      chan struct{}
	releaseOnce                  sync.Once
	notification                 func()
}

func newChannelSnapshotRaceFixture(t *testing.T, stage string, fail bool) *channelSnapshotRaceFixture {
	t.Helper()
	f := &channelSnapshotRaceFixture{entered: make(chan struct{}), release: make(chan struct{})}
	f.setChannel("old")
	block := func(ctx context.Context) error {
		close(f.entered)
		select {
		case <-f.release:
		case <-ctx.Done():
			return ctx.Err()
		}
		if fail {
			return errors.New("obsolete channel read failed")
		}
		return nil
	}
	repo := &mockChannelRepository{
		listAllFn: func(ctx context.Context) ([]Channel, error) {
			f.mu.Lock()
			f.listCalls++
			first := f.listCalls == 1
			second := f.listCalls == 2
			secondEntered, secondRelease := f.secondEntered, f.secondRelease
			channels := append([]Channel(nil), f.channels...)
			f.mu.Unlock()
			if stage == "list" && first {
				if err := block(ctx); err != nil {
					return nil, err
				}
			}
			if second && secondEntered != nil {
				close(secondEntered)
				select {
				case <-secondRelease:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return channels, nil
		},
		getGroupPlatformsFn: func(ctx context.Context, _ []int64) (map[int64]string, error) {
			f.mu.Lock()
			f.platformCalls++
			first := f.platformCalls == 1
			f.mu.Unlock()
			if stage == "platforms" && first {
				if err := block(ctx); err != nil {
					return nil, err
				}
			}
			return map[int64]string{10: PlatformOpenAI}, nil
		},
	}
	notifications := &channelSnapshotNotifications{}
	f.service = NewChannelService(repo, nil, nil, nil, notifications)
	f.notification = notifications.callback
	f.notifications = notifications
	t.Cleanup(func() { f.releaseOnce.Do(func() { close(f.release) }) })
	return f
}

func (f *channelSnapshotRaceFixture) setChannel(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.channels = []Channel{{ID: 1, Name: name, Status: StatusActive, GroupIDs: []int64{10}, BillingModelSource: BillingModelSourceUpstream,
		ModelMapping: map[string]map[string]string{PlatformOpenAI: {"gpt-5.2": name}},
		ModelPricing: []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"gpt-5.2"}, InputPrice: testPtrFloat64(2e-6)}},
	}}
}

func channelSnapshotAwait(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(10 * time.Second):
		t.Fatal("channel snapshot barrier timed out")
	}
}

type channelSnapshotResult struct {
	cache *channelCache
	err   error
}

func channelSnapshotStart(t *testing.T, f *channelSnapshotRaceFixture, ctx context.Context) <-chan channelSnapshotResult {
	t.Helper()
	results := make(chan channelSnapshotResult, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		cache, err := f.service.loadCache(ctx)
		results <- channelSnapshotResult{cache, err}
	}()
	t.Cleanup(func() { f.releaseOnce.Do(func() { close(f.release) }); channelSnapshotAwait(t, done) })
	channelSnapshotAwait(t, f.entered)
	return results
}
func channelSnapshotReceive(t *testing.T, results <-chan channelSnapshotResult) channelSnapshotResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(10 * time.Second):
		t.Fatal("channel snapshot caller did not finish")
		return channelSnapshotResult{}
	}
}

func TestChannelSnapshotInvalidationRejectsObsoleteBuild(t *testing.T) {
	for _, notification := range []string{"local", "remote"} {
		t.Run(notification, func(t *testing.T) {
			for _, stage := range []string{"list", "platforms"} {
				t.Run(stage, func(t *testing.T) {
					for _, fail := range []bool{false, true} {
						name := "success"
						if fail {
							name = "error"
						}
						t.Run(name, func(t *testing.T) {
							f := newChannelSnapshotRaceFixture(t, stage, fail)
							results := channelSnapshotStart(t, f, context.Background())
							var latest *channelCache
							// Two consecutive invalidations must not be undone by the oldest load.
							for _, name := range []string{"new-1", "new-2"} {
								f.setChannel(name)
								if notification == "local" {
									f.service.InvalidateCache()
								} else {
									require.NotNil(t, f.notification)
									f.notification()
								}
								var err error
								latest, err = f.service.loadCache(context.Background())
								require.NoError(t, err)
								require.Equal(t, name, latest.byID[1].Name)
							}
							f.releaseOnce.Do(func() { close(f.release) })
							oldCaller := channelSnapshotReceive(t, results)
							require.NoError(t, oldCaller.err, "obsolete errors must not discard the newer successful snapshot")
							require.Same(t, latest, oldCaller.cache, "obsolete singleflight callers must retry the current generation")
							current, err := f.service.loadCache(context.Background())
							require.NoError(t, err)
							require.Same(t, latest, current, "obsolete success/error publication must not replace the newer snapshot")
							require.Equal(t, "new-2", f.service.ResolveChannelMapping(context.Background(), 10, "gpt-5.2").MappedModel)
							channel, err := f.service.GetChannelForGroup(context.Background(), 10)
							require.NoError(t, err)
							require.Equal(t, "new-2", channel.Name)
							require.Equal(t, 2e-6, *f.service.GetChannelModelPricing(context.Background(), 10, "gpt-5.2").InputPrice)
							f.mu.Lock()
							calls := f.listCalls
							f.mu.Unlock()
							require.Equal(t, 3, calls, "reuse the valid new snapshot rather than fetching a fourth copy")
							t.Logf("CHANNEL_SNAPSHOT notification=%s stage=%s obsolete=%s invalidations=2 latest=new-2 old_publication_rejected=true old_caller_retried=true reads=3", notification, stage, name)
						})
					}
				})
			}
		})
	}
}

func TestChannelSnapshotInvalidationCanceledObsoleteCaller(t *testing.T) {
	f := newChannelSnapshotRaceFixture(t, "list", false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := channelSnapshotStart(t, f, ctx)
	f.setChannel("new-1")
	f.service.InvalidateCache()
	cancel()
	f.releaseOnce.Do(func() { close(f.release) })
	result := channelSnapshotReceive(t, results)
	require.ErrorIs(t, result.err, context.Canceled)
	current, err := f.service.GetChannelForGroup(context.Background(), 10)
	require.NoError(t, err)
	require.Equal(t, "new-1", current.Name)
}

func channelSnapshotFlightResult(t *testing.T, ch <-chan singleflight.Result) singleflight.Result {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(10 * time.Second):
		t.Fatal("channel singleflight result timed out")
		return singleflight.Result{}
	}
}

func TestChannelSnapshotInvalidationCancelBlockedSharedLoad(t *testing.T) {
	f := newChannelSnapshotRaceFixture(t, "list", false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	caller := channelSnapshotStart(t, f, ctx)
	// DoChan registers this healthy waiter synchronously while the DB read is
	// blocked. The canceled caller must leave before the shared read is released.
	healthy := f.service.cacheSF.DoChan("channel_cache", func() (any, error) { return nil, errors.New("unexpected duplicate build") })
	cancel()
	result := channelSnapshotReceive(t, caller)
	require.ErrorIs(t, result.err, context.Canceled)
	f.releaseOnce.Do(func() { close(f.release) })
	shared := channelSnapshotFlightResult(t, healthy)
	require.True(t, shared.Shared)
	require.NoError(t, shared.Err)
	current, err := f.service.loadCache(context.Background())
	require.NoError(t, err)
	require.Same(t, shared.Val, current)
}

func TestChannelSnapshotInvalidationCancelDuringCurrentRetry(t *testing.T) {
	f := newChannelSnapshotRaceFixture(t, "list", false)
	f.secondEntered = make(chan struct{})
	f.secondRelease = make(chan struct{})
	t.Cleanup(func() { f.secondReleaseOnce.Do(func() { close(f.secondRelease) }) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	caller := channelSnapshotStart(t, f, ctx)
	f.setChannel("new-1")
	f.notification()
	f.releaseOnce.Do(func() { close(f.release) })
	channelSnapshotAwait(t, f.secondEntered)
	healthy := f.service.cacheSF.DoChan("channel_cache", func() (any, error) { return nil, errors.New("unexpected duplicate retry") })
	cancel()
	result := channelSnapshotReceive(t, caller)
	require.ErrorIs(t, result.err, context.Canceled)
	f.secondReleaseOnce.Do(func() { close(f.secondRelease) })
	shared := channelSnapshotFlightResult(t, healthy)
	require.True(t, shared.Shared)
	require.NoError(t, shared.Err)
	current, err := f.service.loadCache(context.Background())
	require.NoError(t, err)
	require.Same(t, shared.Val, current)
	require.Equal(t, "new-1", current.byID[1].Name)
}

func TestChannelSnapshotInvalidationAlreadyPublishedFlight(t *testing.T) {
	for _, failed := range []bool{false, true} {
		name := "success"
		if failed {
			name = "error"
		}
		t.Run(name, func(t *testing.T) {
			oldRead := true
			reads := 0
			originalErr := errors.New("obsolete published DB error")
			repo := &mockChannelRepository{listAllFn: func(context.Context) ([]Channel, error) {
				reads++
				if oldRead && failed {
					return nil, originalErr
				}
				label := "new"
				if oldRead {
					label = "old"
				}
				return []Channel{{ID: 1, Name: label, Status: StatusActive}}, nil
			}}
			s := NewChannelService(repo, nil, nil, nil, nil)
			published := make(chan struct{})
			release := make(chan struct{})
			var once sync.Once
			t.Cleanup(func() { once.Do(func() { close(release) }) })
			creator := s.cacheSF.DoChan("channel_cache", func() (any, error) {
				cache, err := s.buildCache(context.Background())
				close(published)
				<-release
				return cache, err
			})
			channelSnapshotAwait(t, published)
			// Unlike the old-build rejection matrix, publication has ALREADY succeeded.
			// This synchronously registered waiter will receive exactly that old result.
			waiter := s.cacheSF.DoChan("channel_cache", func() (any, error) { return nil, errors.New("unexpected duplicate published flight") })
			oldRead = false
			s.InvalidateCache()
			latest, err := s.loadCache(context.Background())
			require.NoError(t, err)
			require.Equal(t, "new", latest.byID[1].Name)
			once.Do(func() { close(release) })
			for _, ch := range []<-chan singleflight.Result{creator, waiter} {
				result := channelSnapshotFlightResult(t, ch)
				require.True(t, result.Shared)
				if failed {
					require.ErrorIs(t, result.Err, originalErr)
				} else {
					require.NoError(t, result.Err)
				}
				cache, retry, err := s.consumeChannelCacheResult(context.Background(), result)
				require.NoError(t, err)
				require.True(t, retry)
				require.Nil(t, cache)
				current, err := s.loadCache(context.Background())
				require.NoError(t, err)
				require.Same(t, latest, current)
				canceled, cancel := context.WithCancel(context.Background())
				cancel()
				_, retry, err = s.consumeChannelCacheResult(canceled, result)
				require.ErrorIs(t, err, context.Canceled)
				require.False(t, retry)
			}
			require.Equal(t, 2, reads, "creator/waiter retry should reuse current snapshot")
			t.Logf("CHANNEL_DELIVERY obsolete=%s creator_and_waiter_shared=true old_publication_succeeded=true stale_delivery_retried=true reads=2", name)
		})
	}
}

func TestChannelSnapshotInvalidationPublisherSelfEcho(t *testing.T) {
	f := newChannelSnapshotRaceFixture(t, "list", false)
	f.notifications.echo = true
	caller := channelSnapshotStart(t, f, context.Background())
	f.setChannel("new-1")
	f.service.InvalidateCache()
	require.Equal(t, 1, f.notifications.publishes, "notification callback must not republish")
	require.Nil(t, f.service.currentCachedSnapshot(), "self echo clears the CRUD-prewarmed snapshot")
	latest, err := f.service.loadCache(context.Background())
	require.NoError(t, err)
	f.releaseOnce.Do(func() { close(f.release) })
	result := channelSnapshotReceive(t, caller)
	require.NoError(t, result.err)
	require.Same(t, latest, result.cache)
	require.Equal(t, "new-1", latest.byID[1].Name)
	require.Equal(t, 1, f.notifications.publishes)
	f.mu.Lock()
	reads := f.listCalls
	f.mu.Unlock()
	require.Equal(t, 3, reads)
}
