package handler

import (
	"bufio"
	"context"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

// Keep these errors fixed: neither upstream headers nor body belong in an error.
var (
	errStrictPluginBufferFull             = errors.New("strict plugin buffer concurrency limit exceeded")
	errStrictPluginBufferLimit            = errors.New("strict plugin buffer byte limit exceeded")
	errStrictPluginBufferReleased         = errors.New("strict plugin buffer reservation released")
	errStrictPluginBufferCommitted        = errors.New("strict plugin buffer already committed")
	errStrictPluginBufferHijack           = errors.New("strict plugin buffer hijack prohibited")
	errStrictPluginBufferReplacementMedia = errors.New("strict plugin buffer replacement media type invalid")
)

// One budget is shared by the middleware, not constructed per request. Its mutex
// also protects every reservation's byte count and released flag. Lock order is
// writer.mu -> budget.mu; release never acquires the writer lock.
type strictPluginBufferBudget struct {
	mu            sync.Mutex
	maxConcurrent int
	totalBytes    int64
	active        int
	usedBytes     int64
}

type strictPluginBufferReservation struct {
	budget    *strictPluginBufferBudget
	usedBytes int64
	released  bool
}

func newStrictPluginBufferBudget(maxConcurrent int, totalBytes int64) *strictPluginBufferBudget {
	if totalBytes < 0 {
		totalBytes = 0
	}
	return &strictPluginBufferBudget{maxConcurrent: maxConcurrent, totalBytes: totalBytes}
}

// acquire never waits for a slot to be returned. Canceled contexts take priority
// over a full budget, including cancellation while acquiring the accounting lock.
func (b *strictPluginBufferBudget) acquire(ctx context.Context) (*strictPluginBufferReservation, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b == nil {
		return nil, errStrictPluginBufferFull
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b.active >= b.maxConcurrent {
		return nil, errStrictPluginBufferFull
	}
	b.active++
	return &strictPluginBufferReservation{budget: b}, nil
}

// reserve atomically accounts only for bytes about to be cached. A zero-byte
// reservation checks liveness without allocating or changing the global budget.
func (r *strictPluginBufferReservation) reserve(n int64) error {
	if r == nil || r.budget == nil {
		return errStrictPluginBufferReleased
	}
	b := r.budget
	b.mu.Lock()
	defer b.mu.Unlock()
	if r.released {
		return errStrictPluginBufferReleased
	}
	if n < 0 || n > b.totalBytes-b.usedBytes {
		return errStrictPluginBufferLimit
	}
	b.usedBytes += n
	r.usedBytes += n
	return nil
}

// The middleware owns this lifetime and must defer release through Commit.
// Neither a rejection nor a successful Commit makes cached bytes free early.
func (r *strictPluginBufferReservation) release() {
	if r == nil || r.budget == nil {
		return
	}
	b := r.budget
	b.mu.Lock()
	defer b.mu.Unlock()
	if r.released {
		return
	}
	r.released = true
	b.active--
	b.usedBytes -= r.usedBytes
	r.usedBytes = 0
}

// Deliberately do not embed gin.ResponseWriter or expose Unwrap: optional HTTP
// interfaces must never route around the explicit approval boundary at Commit.
// mu serializes output and observations, including keepalive Flush/Write calls.
// As with http.ResponseWriter, callers must serialize direct Header map edits;
// obtaining a mutable http.Header cannot make arbitrary map edits race-safe.
type strictPluginResponseWriter struct {
	mu                        sync.Mutex
	base                      gin.ResponseWriter
	reservation               *strictPluginBufferReservation
	maxBytes                  int64
	ctx                       context.Context
	cancel                    context.CancelFunc
	header                    http.Header
	trustedReplacementHeaders http.Header
	status                    int
	size                      int
	body                      []byte
	err                       error
	committed                 bool
}

var _ gin.ResponseWriter = (*strictPluginResponseWriter)(nil)

func newStrictPluginResponseWriter(base gin.ResponseWriter, reservation *strictPluginBufferReservation, maxBytes int64, ctx context.Context, cancel context.CancelFunc) *strictPluginResponseWriter {
	if ctx == nil {
		ctx = context.Background()
	}
	if maxBytes < 0 {
		maxBytes = 0
	}
	header := base.Header().Clone()
	if header == nil {
		header = make(http.Header)
	}
	return &strictPluginResponseWriter{
		base: base, reservation: reservation, maxBytes: maxBytes, ctx: ctx,
		cancel: cancel, header: header, trustedReplacementHeaders: strictPluginTrustedReplacementHeaders(header), status: http.StatusOK, size: -1,
	}
}

// Freeze only server-owned policy headers at construction, with independent
// value slices. Neither downstream Header edits nor later base mutations can
// acquire policy authority for an approved content replacement. Header names
// are matched case-insensitively and the private snapshot uses canonical keys.
func strictPluginTrustedReplacementHeaders(header http.Header) http.Header {
	trusted := make(http.Header)
	for key, values := range header {
		switch strings.ToLower(key) {
		case "access-control-allow-origin", "access-control-allow-credentials",
			"access-control-expose-headers", "access-control-allow-headers",
			"access-control-allow-methods", "access-control-max-age", "vary",
			"x-content-type-options", "x-frame-options", "x-xss-protection",
			"referrer-policy", "content-security-policy",
			"content-security-policy-report-only", "strict-transport-security",
			"permissions-policy", "cross-origin-opener-policy",
			"cross-origin-embedder-policy", "cross-origin-resource-policy",
			"x-permitted-cross-domain-policies":
			canonical := http.CanonicalHeaderKey(key)
			trusted[canonical] = append(trusted[canonical], values...)
		}
	}
	return trusted
}

func (w *strictPluginResponseWriter) Header() http.Header { return w.header }

// failLocked latches the first failure before canceling upstream work, so a
// cancellation caused by this error cannot replace the useful original error.
func (w *strictPluginResponseWriter) failLocked(err error) error {
	if w.err == nil {
		w.err = err
		if w.cancel != nil {
			w.cancel()
		}
	}
	return w.err
}

func (w *strictPluginResponseWriter) healthyLocked() error {
	if w.err != nil {
		return w.err
	}
	if err := w.ctx.Err(); err != nil {
		return w.failLocked(err)
	}
	if err := w.reservation.reserve(0); err != nil {
		return w.failLocked(err)
	}
	return nil
}

func (w *strictPluginResponseWriter) outputLocked() error {
	if w.err != nil {
		return w.err
	}
	if w.committed {
		return errStrictPluginBufferCommitted
	}
	return w.healthyLocked()
}

func (w *strictPluginResponseWriter) markWrittenLocked() {
	if w.size < 0 {
		w.size = 0
	}
}

func (w *strictPluginResponseWriter) WriteHeader(code int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.outputLocked() != nil {
		return
	}
	if code > 0 && w.size < 0 {
		w.status = code
	}
}

func (w *strictPluginResponseWriter) WriteHeaderNow() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.outputLocked() == nil {
		w.markWrittenLocked()
	}
}

func (w *strictPluginResponseWriter) Flush() {
	// Gin's Written/Size transition is retained, but no base.Flush is reachable.
	w.WriteHeaderNow()
}

func (w *strictPluginResponseWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writeLocked(p, "")
}

func (w *strictPluginResponseWriter) WriteString(s string) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	// Avoid materializing a copy of a rejected, potentially very large string.
	return w.writeLocked(nil, s)
}

func (w *strictPluginResponseWriter) writeLocked(p []byte, s string) (int, error) {
	if err := w.outputLocked(); err != nil {
		return 0, err
	}
	n := len(p) + len(s)
	if int64(n) > w.maxBytes-int64(len(w.body)) {
		return 0, w.failLocked(errStrictPluginBufferLimit)
	}
	if err := w.reservation.reserve(int64(n)); err != nil {
		return 0, w.failLocked(err)
	}
	w.body = append(w.body, p...)
	w.body = append(w.body, s...)
	w.markWrittenLocked()
	w.size += n
	return n, nil
}

func (w *strictPluginResponseWriter) Status() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status
}
func (w *strictPluginResponseWriter) Size() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.size
}
func (w *strictPluginResponseWriter) Written() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.size >= 0
}
func (w *strictPluginResponseWriter) Error() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.healthyLocked()
}
func (w *strictPluginResponseWriter) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]byte(nil), w.body...)
}

func (w *strictPluginResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.outputLocked(); err != nil {
		return nil, nil, err
	}
	return nil, nil, w.failLocked(errStrictPluginBufferHijack)
}
func (w *strictPluginResponseWriter) Pusher() http.Pusher { return nil }
func (w *strictPluginResponseWriter) CloseNotify() <-chan bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.base.CloseNotify()
}

// Commit retains the original isolated headers for an approved pass-through.
// Use CommitReplacement, not Commit, for any output-content replacement.
// The middleware calls either method only after all output checks pass.
func (w *strictPluginResponseWriter) Commit(body []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.outputLocked(); err != nil {
		return err
	}
	return w.commitLocked(body, w.header.Clone())
}

// CommitReplacement atomically submits an approved replacement with no upstream
// header authority. It restores only CORS/security policy from the independent
// constructor-time base snapshot, plus a canonical JSON/SSE Content-Type supplied
// by the protocol adapter and generated Content-Length. Downstream or late base
// policy edits never survive. No Cache-Control, Request-ID, cookies, custom
// headers, validators, content encodings, digests or trailers are carried over.
// Only optional UTF-8 charset parameters are accepted and none are reflected.
func (w *strictPluginResponseWriter) CommitReplacement(body []byte, mediaType string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.outputLocked(); err != nil {
		return err
	}
	media, params, err := mime.ParseMediaType(mediaType)
	if err != nil || (media != "application/json" && media != "text/event-stream") {
		return w.failLocked(errStrictPluginBufferReplacementMedia)
	}
	for key, value := range params {
		if key != "charset" || (!strings.EqualFold(value, "utf-8") && !strings.EqualFold(value, "utf8")) {
			return w.failLocked(errStrictPluginBufferReplacementMedia)
		}
	}
	header := w.trustedReplacementHeaders.Clone()
	header.Set("Content-Type", media)
	return w.commitLocked(body, header)
}

// Both public commits hold mu and share the once-only approval boundary. The
// supplied body is not cached and consumes no extra global byte reservation;
// it still obeys maxBytes and remains owned by the caller until return. Neither
// commit releases the reservation or changes captured upstream Bytes/Size.
// Network I/O after the healthy-context check is irreversible. Any subsequent
// disconnect or short write is reported, latched and never retried here.
func (w *strictPluginResponseWriter) commitLocked(body []byte, header http.Header) error {
	if int64(len(body)) > w.maxBytes {
		return w.failLocked(errStrictPluginBufferLimit)
	}
	// Header permits direct, noncanonical map keys. Remove all casing variants
	// before setting one authoritative length, rather than leaving duplicates.
	for key := range header {
		if strings.EqualFold(key, "Content-Length") || strings.EqualFold(key, "Transfer-Encoding") {
			delete(header, key)
		}
	}
	header.Set("Content-Length", strconv.Itoa(len(body)))
	// Recheck immediately before the first observable base mutation.
	if err := w.healthyLocked(); err != nil {
		return err
	}
	w.committed = true
	w.markWrittenLocked()
	target := w.base.Header()
	for key := range target {
		delete(target, key)
	}
	for key, values := range header {
		target[key] = values
	}
	w.base.WriteHeader(w.status)
	if len(body) == 0 {
		w.base.WriteHeaderNow()
		return nil
	}
	n, err := w.base.Write(body)
	if err == nil && n != len(body) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return w.failLocked(err)
	}
	return nil
}
