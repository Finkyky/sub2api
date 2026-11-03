package service

import (
	"context"
	"sync"
	"time"
)

// PluginTaskRetryKeyRepository is optional to preserve the frozen repository
// interface. The production PostgreSQL repository implements this SQL query.
type PluginTaskRetryKeyRepository interface {
	RetryKeys(context.Context, int64, int) ([]string, error)
}

const pluginTaskSchedulerWorkers = 4

// A scheduler is single-use and node-local. Database Claim, not this pending
// map, arbitrates execution across nodes. Stop cancels/drains scheduler and
// manual calls; the owner can then close the shared instance service.
type PluginTaskScheduler struct {
	tasks   *PluginTaskService
	mu      sync.Mutex
	started bool
	stopped bool
	cancel  context.CancelFunc
	done    chan struct{}
	pending map[int64]bool
}

func NewPluginTaskScheduler(tasks *PluginTaskService) *PluginTaskScheduler {
	return &PluginTaskScheduler{tasks: tasks, done: make(chan struct{}), pending: make(map[int64]bool)}
}
func (s *PluginTaskScheduler) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started || s.stopped {
		return
	}
	s.started = true
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	go s.loop(ctx)
}
func (s *PluginTaskScheduler) poll(ctx context.Context, jobs chan<- int64) {
	if s.tasks == nil || s.tasks.repo == nil {
		return
	}
	poll, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ids, e := s.tasks.repo.Due(poll, PluginTaskPollLimit)
	if e != nil {
		return
	}
	for _, id := range ids {
		s.mu.Lock()
		already := s.pending[id]
		if !already {
			s.pending[id] = true
		}
		s.mu.Unlock()
		if already {
			continue
		}
		select {
		case jobs <- id:
		case <-ctx.Done():
			s.complete(id)
			return
		default:
			s.complete(id)
		}
	}
}
func (s *PluginTaskScheduler) complete(id int64) { s.mu.Lock(); delete(s.pending, id); s.mu.Unlock() }
func (s *PluginTaskScheduler) dispatch(ctx context.Context, id int64) {
	task, e := s.tasks.Get(ctx, id)
	if e != nil || !task.Enabled {
		return
	}
	if retryRepo, ok := s.tasks.repo.(PluginTaskRetryKeyRepository); ok {
		// Production retry discovery must not depend on a recent-history window.
		discover, cancel := context.WithTimeout(ctx, 5*time.Second)
		keys, err := retryRepo.RetryKeys(discover, id, PluginTaskPollLimit)
		cancel()
		if err != nil {
			return
		}
		for _, key := range keys {
			if result, err := s.tasks.execute(ctx, id, "retry", key); err == nil && result != nil {
				return
			}
			if ctx.Err() != nil {
				return
			}
		}
	} else {
		// Compatibility for small in-memory repositories, never PostgreSQL.
		// Retry uses the original logical key, with Claim enforcing DB retry_at and
		// the attempt cap. Failed scheduled retries do not consume another cron slot.
		runs, e := s.tasks.Runs(ctx, id, 100)
		if e != nil {
			return
		}
		seen := map[string]bool{}
		for _, run := range runs {
			if seen[run.IdempotencyKey] {
				continue
			}
			seen[run.IdempotencyKey] = true
			if (run.Status == "failed" || run.Status == "lost") && run.Attempt <= task.MaxRetries && run.TaskGeneration == task.Generation {
				result, err := s.tasks.execute(ctx, id, "retry", run.IdempotencyKey)
				if err == nil && result != nil && (result.Attempt > run.Attempt || result.Status == "running" || result.Status == "succeeded") {
					return
				}
				// A terminal replay from an old instance generation/revision is not
				// a new retry and must not starve the next scheduled occurrence.
			}
		}

	}
	key := PluginTaskScheduledKey(*task)
	if key != "" {
		_, _ = s.tasks.execute(ctx, id, "scheduled", key)
	}
}
func (s *PluginTaskScheduler) loop(ctx context.Context) {
	defer close(s.done)
	jobs := make(chan int64, PluginTaskPollLimit)
	var workers sync.WaitGroup
	for i := 0; i < pluginTaskSchedulerWorkers; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case id := <-jobs:
					if ctx.Err() == nil {
						s.dispatch(ctx, id)
					}
					s.complete(id)
				}
			}
		}()
	}
	s.poll(ctx, jobs)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			workers.Wait()
			return
		case <-ticker.C:
			s.poll(ctx, jobs)
		}
	}
}
func (s *PluginTaskScheduler) Stop() {
	s.mu.Lock()
	s.stopped = true
	started := s.started
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	if started {
		<-s.done
	}
	if s.tasks != nil {
		s.tasks.Close()
	}
}
