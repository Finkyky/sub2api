package handler

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type strictBufferTestBase struct {
	gin.ResponseWriter
	rec                                                          *httptest.ResponseRecorder
	headerWrites, writes, stringWrites, flushes, hijacks, pushes int
	closed                                                       chan bool
	writeErr                                                     error
	shortWrite                                                   bool
}

func newStrictBufferTestBase() *strictBufferTestBase {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	return &strictBufferTestBase{ResponseWriter: c.Writer, rec: rec, closed: make(chan bool, 1)}
}
func (b *strictBufferTestBase) WriteHeader(code int) {
	b.headerWrites++
	b.ResponseWriter.WriteHeader(code)
}
func (b *strictBufferTestBase) WriteHeaderNow() { b.headerWrites++; b.ResponseWriter.WriteHeaderNow() }
func (b *strictBufferTestBase) Write(p []byte) (int, error) {
	b.writes++
	if b.writeErr != nil {
		return 0, b.writeErr
	}
	if b.shortWrite && len(p) > 0 {
		return b.ResponseWriter.Write(p[:len(p)-1])
	}
	return b.ResponseWriter.Write(p)
}
func (b *strictBufferTestBase) WriteString(s string) (int, error) {
	b.stringWrites++
	return b.ResponseWriter.WriteString(s)
}
func (b *strictBufferTestBase) Flush() { b.flushes++; b.ResponseWriter.Flush() }
func (b *strictBufferTestBase) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	b.hijacks++
	return nil, nil, errors.New("base hijack reached")
}
func (b *strictBufferTestBase) CloseNotify() <-chan bool             { return b.closed }
func (b *strictBufferTestBase) Pusher() http.Pusher                  { b.pushes++; return b }
func (b *strictBufferTestBase) Push(string, *http.PushOptions) error { b.pushes++; return nil }

func assertStrictBufferUntouched(t *testing.T, b *strictBufferTestBase, headers http.Header) {
	t.Helper()
	require.Equal(t, headers, b.Header())
	require.False(t, b.Written(), "base headers must remain uncommitted")
	require.Equal(t, -1, b.Size())
	require.Empty(t, b.rec.Body.String())
	require.False(t, b.rec.Flushed)
	require.Zero(t, b.headerWrites+b.writes+b.stringWrites+b.flushes+b.hijacks+b.pushes)
}
func strictBufferWriter(t *testing.T, b *strictPluginBufferBudget, limit int64, ctx context.Context, cancel context.CancelFunc) (*strictPluginResponseWriter, *strictBufferTestBase, *strictPluginBufferReservation) {
	t.Helper()
	r, err := b.acquire(ctx)
	require.NoError(t, err)
	require.NotNil(t, r)
	t.Cleanup(r.release)
	base := newStrictBufferTestBase()
	return newStrictPluginResponseWriter(base, r, limit, ctx, cancel), base, r
}

func TestStrictPluginBufferIsolationAndGinState(t *testing.T) {
	base := newStrictBufferTestBase()
	base.Header().Set("X-Inherited", "before")
	before := base.Header().Clone()
	budget := newStrictPluginBufferBudget(1, 20)
	r, err := budget.acquire(context.Background())
	require.NoError(t, err)
	defer r.release()
	w := newStrictPluginResponseWriter(base, r, 20, context.Background(), nil)
	require.Equal(t, http.StatusOK, w.Status())
	require.Equal(t, -1, w.Size())
	require.False(t, w.Written())
	require.Equal(t, "before", w.Header().Get("X-Inherited"))
	w.Header().Set("Set-Cookie", "secret")
	w.Header()["X-Inherited"][0] = "isolated"
	w.WriteHeader(http.StatusAccepted)
	require.Equal(t, http.StatusAccepted, w.Status())
	require.False(t, w.Written())
	w.WriteHeader(http.StatusCreated)
	w.WriteHeaderNow()
	require.True(t, w.Written())
	require.Zero(t, w.Size())
	w.WriteHeader(http.StatusBadGateway)
	require.Equal(t, http.StatusCreated, w.Status())
	input := []byte("hello")
	n, err := w.Write(input)
	require.NoError(t, err)
	require.Equal(t, 5, n)
	input[0] = 'X'
	n, err = w.WriteString("??")
	require.NoError(t, err)
	require.Equal(t, len("??"), n)
	w.Flush()
	w.Flush()
	require.Equal(t, len("hello??"), w.Size())
	require.Equal(t, []byte("hello??"), w.Bytes())
	copy := w.Bytes()
	copy[0] = 'Y'
	require.Equal(t, []byte("hello??"), w.Bytes())
	require.NoError(t, w.Error())
	assertStrictBufferUntouched(t, base, before)
}

func TestStrictPluginBufferEmptyWritesAndFlush(t *testing.T) {
	for _, operation := range []string{"write", "string", "header", "flush"} {
		t.Run(operation, func(t *testing.T) {
			w, base, _ := strictBufferWriter(t, newStrictPluginBufferBudget(1, 0), 0, context.Background(), nil)
			switch operation {
			case "write":
				n, err := w.Write(nil)
				require.NoError(t, err)
				require.Zero(t, n)
			case "string":
				n, err := w.WriteString("")
				require.NoError(t, err)
				require.Zero(t, n)
			case "header":
				w.WriteHeaderNow()
			case "flush":
				w.Flush()
			}
			require.True(t, w.Written())
			require.Zero(t, w.Size())
			require.Empty(t, w.Bytes())
			assertStrictBufferUntouched(t, base, http.Header{})
		})
	}
}

func TestStrictPluginBufferCommitOriginalReplacementAndEmpty(t *testing.T) {
	for _, body := range []string{"upstream", "approved replacement", ""} {
		t.Run(strconv.Itoa(len(body)), func(t *testing.T) {
			budget := newStrictPluginBufferBudget(1, 32)
			base := newStrictBufferTestBase()
			base.Header().Set("X-Remove", "old")
			r, err := budget.acquire(context.Background())
			require.NoError(t, err)
			defer r.release()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			w := newStrictPluginResponseWriter(base, r, 32, ctx, cancel)
			w.Header().Del("X-Remove")
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Length", "9999")
			w.Header().Set("Transfer-Encoding", "chunked")
			w.Header().Add("Set-Cookie", "allowed=1")
			w.Header().Add("Set-Cookie", "allowed=2")
			w.WriteHeader(http.StatusAccepted)
			_, err = w.WriteString("upstream")
			require.NoError(t, err)
			approved := []byte(body)
			if body == "upstream" {
				approved = w.Bytes()
			}
			require.NoError(t, w.Commit(approved))
			require.Equal(t, http.StatusAccepted, base.rec.Code)
			require.Equal(t, body, base.rec.Body.String())
			require.Equal(t, strconv.Itoa(len(body)), base.Header().Get("Content-Length"))
			require.Empty(t, base.Header().Get("Transfer-Encoding"))
			require.Empty(t, base.Header().Get("X-Remove"))
			require.Equal(t, []string{"allowed=1", "allowed=2"}, base.Header().Values("Set-Cookie"))
			require.Zero(t, base.flushes)
			require.Equal(t, []byte("upstream"), w.Bytes(), "replacement must not rewrite the captured upstream")
			require.Equal(t, len("upstream"), w.Size())
			require.ErrorIs(t, w.Commit([]byte("second")), errStrictPluginBufferCommitted)
			n, err := w.WriteString("late")
			require.Zero(t, n)
			require.ErrorIs(t, err, errStrictPluginBufferCommitted)
			w.WriteHeader(http.StatusBadGateway)
			w.WriteHeaderNow()
			w.Flush()
			require.Equal(t, body, base.rec.Body.String())
			require.Equal(t, http.StatusAccepted, w.Status())
			require.Zero(t, base.flushes)
			require.NoError(t, ctx.Err())
			require.NoError(t, w.Error())
			_, err = budget.acquire(context.Background())
			require.ErrorIs(t, err, errStrictPluginBufferFull)
			w.Header().Set("Set-Cookie", "late-secret")
			require.Equal(t, []string{"allowed=1", "allowed=2"}, base.Header().Values("Set-Cookie"))
		})
	}
}

func TestStrictPluginBufferCommitWithoutWrites(t *testing.T) {
	w, base, _ := strictBufferWriter(t, newStrictPluginBufferBudget(1, 8), 8, context.Background(), nil)
	w.WriteHeader(http.StatusCreated)
	require.NoError(t, w.Commit(nil))
	require.Equal(t, http.StatusCreated, base.rec.Code)
	require.Equal(t, "0", base.Header().Get("Content-Length"))
	require.True(t, w.Written())
	require.Zero(t, w.Size())
	require.Equal(t, 2, base.headerWrites, "Gin stages status before WriteHeaderNow emits empty response headers")
}

func TestStrictPluginBufferPerRequestLimitIsAtomicAndSticky(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w, base, _ := strictBufferWriter(t, newStrictPluginBufferBudget(1, 30), 5, ctx, cancel)
	w.Header().Set("Authorization", "upstream-secret")
	n, err := w.WriteString("123")
	require.NoError(t, err)
	require.Equal(t, 3, n)
	n, err = w.Write([]byte("456"))
	require.Zero(t, n)
	require.ErrorIs(t, err, errStrictPluginBufferLimit)
	require.ErrorIs(t, w.Error(), errStrictPluginBufferLimit)
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.Equal(t, []byte("123"), w.Bytes())
	require.Equal(t, 3, w.Size())
	n, err = w.WriteString("4")
	require.Zero(t, n)
	require.ErrorIs(t, err, errStrictPluginBufferLimit)
	require.ErrorIs(t, w.Commit([]byte("allowed")), errStrictPluginBufferLimit)
	w.WriteHeaderNow()
	w.Flush()
	assertStrictBufferUntouched(t, base, http.Header{})
}

func TestStrictPluginBufferExactLimitsAndGlobalRelease(t *testing.T) {
	budget := newStrictPluginBufferBudget(3, 5)
	w1, base1, r1 := strictBufferWriter(t, budget, 5, context.Background(), nil)
	w2, base2, r2 := strictBufferWriter(t, budget, 5, context.Background(), nil)
	n, err := w1.WriteString("123")
	require.NoError(t, err)
	require.Equal(t, 3, n)
	n, err = w2.Write([]byte("45"))
	require.NoError(t, err)
	require.Equal(t, 2, n)
	n, err = w2.WriteString("6")
	require.Zero(t, n)
	require.ErrorIs(t, err, errStrictPluginBufferLimit)
	require.Equal(t, []byte("45"), w2.Bytes())
	require.NoError(t, w1.Commit(w1.Bytes()))
	w3, base3, r3 := strictBufferWriter(t, budget, 5, context.Background(), nil)
	n, err = w3.WriteString("x")
	require.Zero(t, n)
	require.ErrorIs(t, err, errStrictPluginBufferLimit)
	r3.release()
	r1.release()
	r1.release()
	w4, _, _ := strictBufferWriter(t, budget, 5, context.Background(), nil)
	n, err = w4.WriteString("abc")
	require.NoError(t, err)
	require.Equal(t, 3, n)
	// A failed writer still owns its previously cached bytes until release.
	w5, base5, _ := strictBufferWriter(t, budget, 5, context.Background(), nil)
	n, err = w5.WriteString("x")
	require.Zero(t, n)
	require.ErrorIs(t, err, errStrictPluginBufferLimit)
	r2.release()
	r2.release()
	require.Equal(t, "123", base1.rec.Body.String())
	assertStrictBufferUntouched(t, base2, http.Header{})
	assertStrictBufferUntouched(t, base3, http.Header{})
	assertStrictBufferUntouched(t, base5, http.Header{})
}

func TestStrictPluginBufferAcquireNonBlockingAndCanceled(t *testing.T) {
	budget := newStrictPluginBufferBudget(1, 8)
	r, err := budget.acquire(context.Background())
	require.NoError(t, err)
	defer r.release()
	done := make(chan error, 1)
	go func() {
		got, err := budget.acquire(context.Background())
		if got != nil {
			got.release()
		}
		done <- err
	}()
	select {
	case err := <-done:
		require.ErrorIs(t, err, errStrictPluginBufferFull)
	case <-time.After(time.Second):
		t.Fatal("full budget acquire blocked")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := budget.acquire(ctx)
	require.Nil(t, got)
	require.ErrorIs(t, err, context.Canceled)
	ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	got, err = budget.acquire(ctx)
	require.Nil(t, got)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	r.release()
	r.release()
	got, err = budget.acquire(context.Background())
	require.NoError(t, err)
	require.NotNil(t, got)
	got.release()
}

func TestStrictPluginBufferDisabledLimitsFailClosed(t *testing.T) {
	for _, slots := range []int{0, -1} {
		b := newStrictPluginBufferBudget(slots, 8)
		r, err := b.acquire(context.Background())
		require.Nil(t, r)
		require.ErrorIs(t, err, errStrictPluginBufferFull)
	}
	for _, bytes := range []int64{0, -1} {
		w, base, _ := strictBufferWriter(t, newStrictPluginBufferBudget(1, bytes), 8, context.Background(), nil)
		n, err := w.WriteString("x")
		require.Zero(t, n)
		require.ErrorIs(t, err, errStrictPluginBufferLimit)
		assertStrictBufferUntouched(t, base, http.Header{})
		w, base, _ = strictBufferWriter(t, newStrictPluginBufferBudget(1, 8), bytes, context.Background(), nil)
		n, err = w.WriteString("x")
		require.Zero(t, n)
		require.ErrorIs(t, err, errStrictPluginBufferLimit)
		assertStrictBufferUntouched(t, base, http.Header{})
	}
}

func TestStrictPluginBufferContextRejectsEveryOutputPath(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		for _, operation := range []string{"write", "string", "flush", "header-now", "header", "commit", "error"} {
			t.Run(strconv.FormatBool(deadline)+"/"+operation, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				w, base, _ := strictBufferWriter(t, newStrictPluginBufferBudget(1, 16), 16, ctx, cancel)
				w.Header().Set("Set-Cookie", "secret")
				_, err := w.WriteString("private")
				require.NoError(t, err)
				expected := error(context.Canceled)
				if deadline {
					expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
					defer stop()
					// The writer receives an already acquired reservation, but an expired output context.
					r, err := newStrictPluginBufferBudget(1, 16).acquire(context.Background())
					require.NoError(t, err)
					defer r.release()
					w = newStrictPluginResponseWriter(base, r, 16, expired, stop)
					w.Header().Set("Set-Cookie", "secret")
					expected = context.DeadlineExceeded
				} else {
					cancel()
				}
				switch operation {
				case "write":
					n, err := w.Write([]byte("leak"))
					require.Zero(t, n)
					require.ErrorIs(t, err, expected)
				case "string":
					n, err := w.WriteString("leak")
					require.Zero(t, n)
					require.ErrorIs(t, err, expected)
				case "flush":
					w.Flush()
				case "header-now":
					w.WriteHeaderNow()
				case "header":
					w.WriteHeader(http.StatusCreated)
				case "commit":
					require.ErrorIs(t, w.Commit([]byte("approved")), expected)
				case "error":
					require.ErrorIs(t, w.Error(), expected)
				}
				require.ErrorIs(t, w.Error(), expected)
				require.ErrorIs(t, w.Commit([]byte("approved")), expected)
				assertStrictBufferUntouched(t, base, http.Header{})
			})
		}
	}
}

func TestStrictPluginBufferHijackPushAndCloseNotify(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w, base, _ := strictBufferWriter(t, newStrictPluginBufferBudget(1, 8), 8, ctx, cancel)
	require.Nil(t, w.Pusher())
	require.Equal(t, (<-chan bool)(base.closed), w.CloseNotify())
	base.closed <- true
	require.True(t, <-w.CloseNotify())
	conn, rw, err := w.Hijack()
	require.Nil(t, conn)
	require.Nil(t, rw)
	require.ErrorIs(t, err, errStrictPluginBufferHijack)
	require.ErrorIs(t, w.Error(), errStrictPluginBufferHijack)
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.ErrorIs(t, w.Commit([]byte("leak")), errStrictPluginBufferHijack)
	assertStrictBufferUntouched(t, base, http.Header{})
}

func TestStrictPluginBufferResponseControllerCannotBypass(t *testing.T) {
	w, base, _ := strictBufferWriter(t, newStrictPluginBufferBudget(1, 8), 8, context.Background(), nil)
	controller := http.NewResponseController(w)
	require.NoError(t, controller.Flush())
	conn, rw, err := controller.Hijack()
	require.Nil(t, conn)
	require.Nil(t, rw)
	require.ErrorIs(t, err, errStrictPluginBufferHijack)
	require.ErrorIs(t, controller.SetWriteDeadline(time.Now()), http.ErrNotSupported)
	assertStrictBufferUntouched(t, base, http.Header{})
}

func TestStrictPluginBufferReleaseRejectsReuse(t *testing.T) {
	budget := newStrictPluginBufferBudget(1, 8)
	w, base, r := strictBufferWriter(t, budget, 8, context.Background(), nil)
	_, err := w.WriteString("12345678")
	require.NoError(t, err)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); r.release() }()
	}
	wg.Wait()
	n, err := w.WriteString("x")
	require.Zero(t, n)
	require.ErrorIs(t, err, errStrictPluginBufferReleased)
	require.ErrorIs(t, w.Commit(nil), errStrictPluginBufferReleased)
	assertStrictBufferUntouched(t, base, http.Header{})
	fresh, _, _ := strictBufferWriter(t, budget, 8, context.Background(), nil)
	n, err = fresh.WriteString("12345678")
	require.NoError(t, err)
	require.Equal(t, 8, n)
}

func TestStrictPluginBufferNilReservationFailsClosed(t *testing.T) {
	base := newStrictBufferTestBase()
	w := newStrictPluginResponseWriter(base, nil, 8, context.Background(), nil)
	n, err := w.WriteString("leak")
	require.Zero(t, n)
	require.ErrorIs(t, err, errStrictPluginBufferReleased)
	require.ErrorIs(t, w.Commit(nil), errStrictPluginBufferReleased)
	assertStrictBufferUntouched(t, base, http.Header{})
}

func TestStrictPluginBufferCommitReplacementLimit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w, base, _ := strictBufferWriter(t, newStrictPluginBufferBudget(1, 8), 3, ctx, cancel)
	_, err := w.WriteString("raw")
	require.NoError(t, err)
	w.Header().Set("Set-Cookie", "secret")
	require.ErrorIs(t, w.Commit([]byte("too-long")), errStrictPluginBufferLimit)
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.Equal(t, []byte("raw"), w.Bytes())
	assertStrictBufferUntouched(t, base, http.Header{})
}

func TestStrictPluginBufferCommitIOFailureIsNotRetried(t *testing.T) {
	for _, short := range []bool{false, true} {
		t.Run(strconv.FormatBool(short), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			w, base, _ := strictBufferWriter(t, newStrictPluginBufferBudget(1, 8), 8, ctx, cancel)
			expected := io.ErrClosedPipe
			if short {
				base.shortWrite = true
				expected = io.ErrShortWrite
			} else {
				base.writeErr = expected
			}
			require.ErrorIs(t, w.Commit([]byte("body")), expected)
			require.ErrorIs(t, w.Error(), expected)
			require.ErrorIs(t, ctx.Err(), context.Canceled)
			require.Error(t, w.Commit([]byte("retry")))
			require.Equal(t, 1, base.writes)
		})
	}
}

func TestStrictPluginBufferConcurrentKeepaliveAndWrites(t *testing.T) {
	const workers, iterations = 8, 100
	w, base, _ := strictBufferWriter(t, newStrictPluginBufferBudget(1, workers*iterations), workers*iterations, context.Background(), nil)
	failures := make(chan error, workers*iterations)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(stringWrite bool) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				var n int
				var err error
				if stringWrite {
					n, err = w.WriteString("x")
				} else {
					n, err = w.Write([]byte("x"))
				}
				if err != nil {
					failures <- err
				} else if n != 1 {
					failures <- errors.New("incorrect byte count")
				}
				w.Flush()
				w.WriteHeaderNow()
				w.WriteHeader(http.StatusAccepted)
				_ = w.Status()
				_ = w.Size()
				_ = w.Written()
				_ = w.Bytes()
				_ = w.Error()
			}
		}(i%2 == 0)
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	require.Equal(t, workers*iterations, w.Size())
	require.Equal(t, strings.Repeat("x", workers*iterations), string(w.Bytes()))
	require.Equal(t, http.StatusOK, w.Status())
	assertStrictBufferUntouched(t, base, http.Header{})
	require.NoError(t, w.Commit(w.Bytes()))
	require.Equal(t, strings.Repeat("x", workers*iterations), base.rec.Body.String())
}

func TestStrictPluginBufferConcurrentGlobalBytes(t *testing.T) {
	const workers, available = 20, 7
	budget := newStrictPluginBufferBudget(workers, available)
	writers := make([]*strictPluginResponseWriter, workers)
	reservations := make([]*strictPluginBufferReservation, workers)
	for i := range writers {
		writers[i], _, reservations[i] = strictBufferWriter(t, budget, 1, context.Background(), nil)
	}
	var successful atomic.Int64
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for _, w := range writers {
		wg.Add(1)
		go func(w *strictPluginResponseWriter) {
			defer wg.Done()
			n, err := w.WriteString("x")
			if err == nil && n == 1 {
				successful.Add(1)
			} else if !errors.Is(err, errStrictPluginBufferLimit) || n != 0 {
				errs <- errors.New("unexpected global budget result")
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, int64(available), successful.Load())
	for _, r := range reservations {
		wg.Add(1)
		go func(r *strictPluginBufferReservation) { defer wg.Done(); r.release(); r.release() }(r)
	}
	wg.Wait()
	w, _, _ := strictBufferWriter(t, budget, available, context.Background(), nil)
	n, err := w.WriteString(strings.Repeat("x", available))
	require.NoError(t, err)
	require.Equal(t, available, n)
}

func TestStrictPluginBufferConcurrentSlotsAndRelease(t *testing.T) {
	const workers, slots = 30, 5
	budget := newStrictPluginBufferBudget(slots, 0)
	var acquired atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	release := make(chan struct{})
	results := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			r, err := budget.acquire(context.Background())
			if err != nil {
				results <- err
				return
			}
			acquired.Add(1)
			results <- nil
			<-release
			r.release()
			r.release()
		}()
	}
	close(start)
	for i := 0; i < workers; i++ {
		select {
		case err := <-results:
			if err != nil {
				require.ErrorIs(t, err, errStrictPluginBufferFull)
			}
		case <-time.After(time.Second):
			close(release)
			wg.Wait()
			t.Fatal("concurrent acquire blocked")
		}
	}
	close(release)
	wg.Wait()
	require.Equal(t, int64(slots), acquired.Load())
	r, err := budget.acquire(context.Background())
	require.NoError(t, err)
	r.release()
}

func TestStrictPluginBufferConcurrentReleaseAndWrite(t *testing.T) {
	budget := newStrictPluginBufferBudget(1, 64)
	w, base, r := strictBufferWriter(t, budget, 64, context.Background(), nil)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 64; i++ {
			_, _ = w.WriteString("x")
			w.Flush()
		}
	}()
	go func() { defer wg.Done(); r.release(); r.release() }()
	wg.Wait()
	n, err := w.WriteString("late")
	require.Zero(t, n)
	require.ErrorIs(t, err, errStrictPluginBufferReleased)
	assertStrictBufferUntouched(t, base, http.Header{})
	fresh, _, _ := strictBufferWriter(t, budget, 64, context.Background(), nil)
	n, err = fresh.WriteString(strings.Repeat("x", 64))
	require.NoError(t, err)
	require.Equal(t, 64, n)
}

func TestStrictPluginBufferCancelCalledOnce(t *testing.T) {
	var canceled atomic.Int64
	w, base, _ := strictBufferWriter(t, newStrictPluginBufferBudget(1, 0), 0, context.Background(), func() { canceled.Add(1) })
	n, err := w.WriteString("x")
	require.Zero(t, n)
	require.ErrorIs(t, err, errStrictPluginBufferLimit)
	_, _ = w.Write(nil)
	_, _ = w.WriteString("")
	w.Flush()
	w.WriteHeaderNow()
	_, _, _ = w.Hijack()
	_ = w.Commit(nil)
	_ = w.Error()
	require.Equal(t, int64(1), canceled.Load())
	assertStrictBufferUntouched(t, base, http.Header{})
}

func TestStrictPluginBufferRejectedFirstWriteRetainsNoBody(t *testing.T) {
	for _, stringWrite := range []bool{false, true} {
		w, base, _ := strictBufferWriter(t, newStrictPluginBufferBudget(1, 64), 2, context.Background(), nil)
		var n int
		var err error
		if stringWrite {
			n, err = w.WriteString("secret")
		} else {
			n, err = w.Write([]byte("secret"))
		}
		require.Zero(t, n)
		require.ErrorIs(t, err, errStrictPluginBufferLimit)
		require.Nil(t, w.Bytes())
		require.Equal(t, -1, w.Size())
		require.False(t, w.Written())
		assertStrictBufferUntouched(t, base, http.Header{})
	}
}

func TestStrictPluginBufferLazyReservationAndGlobalCancel(t *testing.T) {
	budget := newStrictPluginBufferBudget(2, 3)
	w1, _, r1 := strictBufferWriter(t, budget, 1000, context.Background(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w2, base2, _ := strictBufferWriter(t, budget, 1000, ctx, cancel)
	n, err := w1.WriteString("123")
	require.NoError(t, err)
	require.Equal(t, 3, n)
	require.NoError(t, w2.Error(), "a full byte budget still allows an empty live writer")
	n, err = w2.WriteString("x")
	require.Zero(t, n)
	require.ErrorIs(t, err, errStrictPluginBufferLimit)
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.Nil(t, w2.Bytes())
	assertStrictBufferUntouched(t, base2, http.Header{})
	r1.release()
	fresh, _, _ := strictBufferWriter(t, budget, 3, context.Background(), nil)
	n, err = fresh.WriteString("123")
	require.NoError(t, err)
	require.Equal(t, 3, n)
}

func TestStrictPluginBufferConcurrentCommitOnce(t *testing.T) {
	const workers = 16
	w, base, _ := strictBufferWriter(t, newStrictPluginBufferBudget(1, 8), 8, context.Background(), nil)
	_, err := w.WriteString("body")
	require.NoError(t, err)
	body := w.Bytes()
	var wg sync.WaitGroup
	results := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- w.Commit(body) }()
	}
	wg.Wait()
	close(results)
	passed := 0
	for err := range results {
		if err == nil {
			passed++
		} else {
			require.ErrorIs(t, err, errStrictPluginBufferCommitted)
		}
	}
	require.Equal(t, 1, passed)
	require.Equal(t, 1, base.writes)
	require.Equal(t, "body", base.rec.Body.String())
}

func TestStrictPluginBufferCommitNormalizesAllLengthHeaderKeys(t *testing.T) {
	w, base, _ := strictBufferWriter(t, newStrictPluginBufferBudget(1, 8), 8, context.Background(), nil)
	// Direct map assignments are valid Header usage even with noncanonical keys.
	w.Header()["content-length"] = []string{"999"}
	w.Header()["CONTENT-LENGTH"] = []string{"888"}
	w.Header()["transfer-encoding"] = []string{"chunked"}
	require.NoError(t, w.Commit([]byte("body")))
	require.Equal(t, "4", base.Header().Get("Content-Length"))
	require.NotContains(t, base.Header(), "content-length")
	require.NotContains(t, base.Header(), "CONTENT-LENGTH")
	require.NotContains(t, base.Header(), "transfer-encoding")
	require.Equal(t, "4", base.rec.Result().Header.Get("Content-Length"))
}

func strictBufferReplacementHeaders() http.Header {
	return http.Header{
		"Content-Type":   {"application/json; token=upstream-secret"},
		"Content-Length": {"999"}, "content-length": {"888"},
		"Transfer-Encoding": {"chunked"}, "transfer-encoding": {"chunked"},
		"Authorization": {"Bearer private"}, "Proxy-Authenticate": {"private"},
		"Www-Authenticate": {"private"}, "Set-Cookie": {"private=1", "private=2"},
		"Cookie": {"private=3"}, "X-Api-Key": {"private"},
		"X-Custom-Prompt": {"private inspected text"}, "X-Request-Id": {"private"},
		"Request-Id": {"private"}, "Server": {"private"}, "Location": {"private"},
		"Etag": {"old-etag"}, "etag": {"old-etag-lowercase"},
		"Last-Modified": {"old"}, "Content-Encoding": {"gzip"},
		"Content-Md5": {"old"}, "Digest": {"old"}, "Content-Digest": {"old"},
		"Repr-Digest": {"old"}, "Cache-Control": {"private"}, "Vary": {"private"},
		"Trailer": {"X-Secret"}, "Trailer:X-Secret": {"private"},
	}
}

func TestStrictPluginBufferCommitReplacementOnlyTrustedProtocolHeaders(t *testing.T) {
	cases := []struct{ media, canonical, body string }{
		{"application/json", "application/json", `{"approved":true}`},
		{"application/json; charset=UTF-8", "application/json", `{"approved":true}`},
		{"text/event-stream", "text/event-stream", "data: approved\n\n"},
		{"text/event-stream; charset=utf8", "text/event-stream", "data: approved\n\n"},
	}
	for _, tc := range cases {
		t.Run(tc.media, func(t *testing.T) {
			base := newStrictBufferTestBase()
			base.Header().Set("X-Existing-Base", "private")
			budget := newStrictPluginBufferBudget(1, 64)
			r, err := budget.acquire(context.Background())
			require.NoError(t, err)
			defer r.release()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			w := newStrictPluginResponseWriter(base, r, 64, ctx, cancel)
			for key, values := range strictBufferReplacementHeaders() {
				w.Header()[key] = values
			}
			w.WriteHeader(http.StatusAccepted)
			_, err = w.WriteString("original-private")
			require.NoError(t, err)
			require.NoError(t, w.CommitReplacement([]byte(tc.body), tc.media))
			require.Equal(t, http.Header{"Content-Type": {tc.canonical}, "Content-Length": {strconv.Itoa(len(tc.body))}}, base.Header())
			require.Equal(t, base.Header(), base.rec.Result().Header)
			require.Equal(t, tc.body, base.rec.Body.String())
			require.Equal(t, http.StatusAccepted, base.rec.Code)
			require.Zero(t, base.flushes)
			require.Equal(t, []byte("original-private"), w.Bytes())
			require.Equal(t, len("original-private"), w.Size())
			require.NoError(t, ctx.Err())
			require.NoError(t, w.Error())
			_, err = budget.acquire(context.Background())
			require.ErrorIs(t, err, errStrictPluginBufferFull)
			require.ErrorIs(t, w.Commit(w.Bytes()), errStrictPluginBufferCommitted)
			require.ErrorIs(t, w.CommitReplacement([]byte("again"), tc.media), errStrictPluginBufferCommitted)
			w.Header().Set("Content-Type", "private/late")
			require.Equal(t, tc.canonical, base.Header().Get("Content-Type"))
		})
	}
}

func TestStrictPluginBufferCommitPassKeepsOriginalHeaders(t *testing.T) {
	w, base, _ := strictBufferWriter(t, newStrictPluginBufferBudget(1, 64), 64, context.Background(), nil)
	for key, values := range strictBufferReplacementHeaders() {
		w.Header()[key] = values
	}
	_, err := w.WriteString("original")
	require.NoError(t, err)
	require.NoError(t, w.Commit(w.Bytes()))
	for _, key := range []string{"Authorization", "Set-Cookie", "X-Custom-Prompt", "X-Request-Id", "Etag", "Content-Encoding", "Content-Md5", "Digest", "Cache-Control"} {
		require.Equal(t, w.Header().Values(key), base.Header().Values(key), key+" must remain compatible on pass")
	}
	require.Equal(t, "original", base.rec.Body.String())
	require.Equal(t, "8", base.Header().Get("Content-Length"))
	require.Empty(t, base.Header().Get("Transfer-Encoding"))
}

func TestStrictPluginBufferCommitReplacementRejectsUntrustedMedia(t *testing.T) {
	for _, media := range []string{"", "text/plain", "application/octet-stream", "application/json; charset=iso-8859-1", "text/event-stream; charset=latin1", "application/json; token=private", "text/event-stream; boundary=private", "application/json\r\nX-Secret: private", "application/json, text/event-stream"} {
		t.Run(media, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			w, base, _ := strictBufferWriter(t, newStrictPluginBufferBudget(1, 64), 64, ctx, cancel)
			for key, values := range strictBufferReplacementHeaders() {
				w.Header()[key] = values
			}
			_, err := w.WriteString("original")
			require.NoError(t, err)
			require.ErrorIs(t, w.CommitReplacement([]byte("approved"), media), errStrictPluginBufferReplacementMedia)
			require.ErrorIs(t, w.Error(), errStrictPluginBufferReplacementMedia)
			require.ErrorIs(t, ctx.Err(), context.Canceled)
			require.ErrorIs(t, w.Commit(w.Bytes()), errStrictPluginBufferReplacementMedia)
			require.Equal(t, []byte("original"), w.Bytes())
			assertStrictBufferUntouched(t, base, http.Header{})
		})
	}
}

func TestStrictPluginBufferCommitReplacementRejectsUnhealthyState(t *testing.T) {
	for _, reason := range []string{"canceled", "deadline", "limit", "global", "released", "hijack"} {
		t.Run(reason, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			w, base, r := strictBufferWriter(t, newStrictPluginBufferBudget(1, 8), 8, ctx, cancel)
			for key, values := range strictBufferReplacementHeaders() {
				w.Header()[key] = values
			}
			_, err := w.WriteString("original")
			require.NoError(t, err)
			var expected error
			switch reason {
			case "canceled":
				cancel()
				expected = context.Canceled
			case "deadline":
				expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				defer stop()
				w = newStrictPluginResponseWriter(base, r, 8, expired, stop)
				w.Header().Set("X-Secret", "private")
				expected = context.DeadlineExceeded
			case "limit":
				expected = errStrictPluginBufferLimit
			case "global":
				_, err = w.WriteString("x")
				require.ErrorIs(t, err, errStrictPluginBufferLimit)
				expected = errStrictPluginBufferLimit
			case "released":
				r.release()
				expected = errStrictPluginBufferReleased
			case "hijack":
				_, _, err = w.Hijack()
				require.ErrorIs(t, err, errStrictPluginBufferHijack)
				expected = errStrictPluginBufferHijack
			}
			body := []byte("approved")
			if reason == "limit" {
				body = []byte("too-large")
			}
			require.ErrorIs(t, w.CommitReplacement(body, "application/json"), expected)
			require.ErrorIs(t, w.Error(), expected)
			assertStrictBufferUntouched(t, base, http.Header{})
		})
	}
}

func TestStrictPluginBufferCommitReplacementEmptyBody(t *testing.T) {
	w, base, _ := strictBufferWriter(t, newStrictPluginBufferBudget(1, 0), 0, context.Background(), nil)
	for key, values := range strictBufferReplacementHeaders() {
		w.Header()[key] = values
	}
	require.NoError(t, w.CommitReplacement(nil, "application/json"))
	require.Equal(t, http.Header{"Content-Type": {"application/json"}, "Content-Length": {"0"}}, base.Header())
	require.True(t, base.Written())
	require.Empty(t, base.rec.Body.String())
	require.Zero(t, w.Size())
}

func TestStrictPluginBufferConcurrentOriginalAndReplacementCommit(t *testing.T) {
	const workers = 16
	w, base, _ := strictBufferWriter(t, newStrictPluginBufferBudget(1, 64), 64, context.Background(), nil)
	w.Header().Set("X-Secret", "private")
	_, err := w.WriteString("original")
	require.NoError(t, err)
	var wg sync.WaitGroup
	results := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(replace bool) {
			defer wg.Done()
			if replace {
				results <- w.CommitReplacement([]byte("approved"), "application/json")
			} else {
				results <- w.Commit([]byte("original"))
			}
		}(i%2 == 0)
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else {
			require.ErrorIs(t, err, errStrictPluginBufferCommitted)
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, base.writes)
	switch base.rec.Body.String() {
	case "approved":
		require.Equal(t, http.Header{"Content-Type": {"application/json"}, "Content-Length": {"8"}}, base.Header())
	case "original":
		require.Equal(t, "private", base.Header().Get("X-Secret"))
	default:
		t.Fatalf("unexpected body: %q", base.rec.Body.String())
	}
	require.Equal(t, []byte("original"), w.Bytes())
	require.NoError(t, w.Error())
}

func TestStrictPluginBufferCommitReplacementIOFailureIsNotRetried(t *testing.T) {
	w, base, _ := strictBufferWriter(t, newStrictPluginBufferBudget(1, 64), 64, context.Background(), nil)
	for key, values := range strictBufferReplacementHeaders() {
		w.Header()[key] = values
	}
	base.writeErr = io.ErrClosedPipe
	require.ErrorIs(t, w.CommitReplacement([]byte("approved"), "application/json"), io.ErrClosedPipe)
	require.Equal(t, http.Header{"Content-Type": {"application/json"}, "Content-Length": {"8"}}, base.Header())
	require.ErrorIs(t, w.Error(), io.ErrClosedPipe)
	require.Error(t, w.Commit([]byte("original")))
	require.Error(t, w.CommitReplacement([]byte("again"), "text/event-stream"))
	require.Equal(t, 1, base.writes)
}

// Replacement may retain server-owned CORS/security policy only from the
// constructor snapshot, never from the mutable downstream or live base map.
func strictBufferTrustedServerHeaders() http.Header {
	return http.Header{
		"Access-Control-Allow-Origin":         {"https://app.example"},
		"Access-Control-Allow-Credentials":    {"true"},
		"Access-Control-Expose-Headers":       {"ETag, Server-Timing"},
		"Access-Control-Allow-Headers":        {"Authorization, Content-Type"},
		"Access-Control-Allow-Methods":        {"POST, OPTIONS"},
		"Access-Control-Max-Age":              {"86400"},
		"Vary":                                {"Origin", "Access-Control-Request-Headers"},
		"X-Content-Type-Options":              {"nosniff"},
		"X-Frame-Options":                     {"DENY"},
		"X-Xss-Protection":                    {"0"},
		"Referrer-Policy":                     {"strict-origin-when-cross-origin"},
		"Content-Security-Policy":             {"default-src 'self'; script-src 'nonce-server'"},
		"Content-Security-Policy-Report-Only": {"default-src 'self'"},
		"Strict-Transport-Security":           {"max-age=31536000; includeSubDomains"},
		"Permissions-Policy":                  {"camera=(), microphone=()"},
		"Cross-Origin-Opener-Policy":          {"same-origin"},
		"Cross-Origin-Embedder-Policy":        {"require-corp"},
		"Cross-Origin-Resource-Policy":        {"same-origin"},
		"X-Permitted-Cross-Domain-Policies":   {"none"},
	}
}

func TestStrictPluginBufferReplacementTrustedServerSnapshot(t *testing.T) {
	for _, media := range []string{"application/json", "text/event-stream"} {
		for _, lower := range []bool{false, true} {
			name := media + "/canonical"
			if lower {
				name = media + "/lowercase"
			}
			t.Run(name, func(t *testing.T) {
				base := newStrictBufferTestBase()
				trusted := strictBufferTrustedServerHeaders()
				for key, values := range trusted {
					if lower {
						key = strings.ToLower(key)
					}
					base.Header()[key] = append([]string(nil), values...)
				}
				for key, values := range strictBufferReplacementHeaders() {
					if strings.EqualFold(key, "Vary") {
						continue
					}
					base.Header()[key] = append([]string(nil), values...)
				}
				budget := newStrictPluginBufferBudget(1, 64)
				r, err := budget.acquire(context.Background())
				require.NoError(t, err)
				defer r.release()
				w := newStrictPluginResponseWriter(base, r, 64, context.Background(), nil)
				assertStrictBufferUntouched(t, base, base.Header().Clone())
				for key := range trusted {
					storedKey := key
					if lower {
						storedKey = strings.ToLower(key)
					}
					// Mutating both value slices detects a shallow constructor snapshot.
					w.Header()[storedKey][0] = "downstream-private"
					base.Header()[storedKey][0] = "late-base-private"
					w.Header().Set(key, "downstream-private")
					w.Header()[strings.ToUpper(key)] = []string{"spoof-private"}
					base.Header().Set(key, "late-base-private")
				}
				w.Header().Set("X-Private-Replacement", "downstream-private")
				base.Header().Set("X-Late-Base", "late-base-private")
				_, err = w.WriteString("original")
				require.NoError(t, err)
				body := []byte("approved")
				require.NoError(t, w.CommitReplacement(body, media))
				expected := trusted.Clone()
				expected.Set("Content-Type", media)
				expected.Set("Content-Length", strconv.Itoa(len(body)))
				require.Equal(t, expected, base.Header())
				require.Equal(t, expected, base.rec.Result().Header)
				require.Equal(t, "approved", base.rec.Body.String())
				require.ErrorIs(t, w.CommitReplacement(body, media), errStrictPluginBufferCommitted)
			})
		}
	}
}

func TestStrictPluginBufferReplacementRejectsDownstreamPolicyAuthority(t *testing.T) {
	for _, media := range []string{"application/json", "text/event-stream"} {
		t.Run(media, func(t *testing.T) {
			w, base, _ := strictBufferWriter(t, newStrictPluginBufferBudget(1, 64), 64, context.Background(), nil)
			for key, values := range strictBufferTrustedServerHeaders() {
				w.Header()[key] = append([]string(nil), values...)
				base.Header()[key] = append([]string(nil), values...)
			}
			require.NoError(t, w.CommitReplacement([]byte("approved"), media))
			require.Equal(t, http.Header{"Content-Type": {media}, "Content-Length": {"8"}}, base.Header())
		})
	}
}
