// Copyright 2020 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package alloydb

import (
	"context"
	"crypto/rsa"
	"fmt"
	"regexp"
	"sync"
	"time"

	alloydbadmin "cloud.google.com/go/alloydb/apiv1alpha"
	"cloud.google.com/go/alloydbconn/debug"
	"cloud.google.com/go/alloydbconn/errtype"
	telv2 "cloud.google.com/go/alloydbconn/internal/tel/v2"
	"golang.org/x/time/rate"
)

const (
	// the refresh buffer is the amount of time before a refresh cycle's result
	// expires that a new refresh operation begins.
	refreshBuffer = 4 * time.Minute

	// refreshInterval is the amount of time between refresh attempts as
	// enforced by the rate limiter.
	refreshInterval = 30 * time.Second

	// RefreshTimeout is the maximum amount of time to wait for a refresh
	// cycle to complete. This value should be greater than the
	// refreshInterval.
	RefreshTimeout = 60 * time.Second

	// refreshBurst is the initial burst allowed by the rate limiter.
	refreshBurst = 2
)

var (
	// Instance URI is in the format:
	// 'projects/<PROJECT>/locations/<REGION>/clusters/<CLUSTER>/instances/<INSTANCE>'
	// Additionally, we have to support legacy "domain-scoped" projects
	// (e.g. "google.com:PROJECT")
	instURIRegex = regexp.MustCompile("projects/([^:]+(:[^:]+)?)/locations/([^:]+)/clusters/([^:]+)/instances/([^:]+)")
)

// InstanceURI represents an AlloyDB instance.
type InstanceURI struct {
	project string
	region  string
	cluster string
	name    string
}

// Project returns the project ID of the cluster.
func (i InstanceURI) Project() string {
	return i.project
}

// Region returns the region (aka location) of the cluster.
func (i InstanceURI) Region() string {
	return i.region
}

// Cluster returns the name of the cluster.
func (i InstanceURI) Cluster() string {
	return i.cluster
}

// Name returns the name of the instance.
func (i InstanceURI) Name() string {
	return i.name
}

// URI returns the full URI specifying an instance.
func (i *InstanceURI) URI() string {
	return fmt.Sprintf(
		"projects/%s/locations/%s/clusters/%s/instances/%s",
		i.project, i.region, i.cluster, i.name,
	)
}

// String returns a short-hand representation of an instance URI.
func (i *InstanceURI) String() string {
	return fmt.Sprintf("%s.%s.%s.%s", i.project, i.region, i.cluster, i.name)
}

// ParseInstURI initializes a new InstanceURI struct.
func ParseInstURI(cn string) (InstanceURI, error) {
	b := []byte(cn)
	m := instURIRegex.FindSubmatch(b)
	if m == nil {
		err := errtype.NewConfigError(
			"invalid instance URI, expected projects/<PROJECT>/locations/<REGION>/clusters/<CLUSTER>/instances/<INSTANCE>",
			cn,
		)
		return InstanceURI{}, err
	}

	c := InstanceURI{
		project: string(m[1]),
		region:  string(m[3]),
		cluster: string(m[4]),
		name:    string(m[5]),
	}
	return c, nil
}

// result is the outcome of a single refresh cycle. Readers must wait on done
// before reading info or err. The writer populates info/err and then closes
// done exactly once.
type result struct {
	info ConnectionInfo
	err  error
	done chan struct{}
}

// newPendingResult returns a result that has not yet been populated. Callers
// blocked on done will unblock when the result is filled in.
func newPendingResult() *result {
	return &result{done: make(chan struct{})}
}

// newReadyResult returns a result whose done channel is already closed.
func newReadyResult(info ConnectionInfo, err error) *result {
	r := &result{info: info, err: err, done: make(chan struct{})}
	close(r.done)
	return r
}

// isReady reports whether the result has been populated.
func (r *result) isReady() bool {
	select {
	case <-r.done:
		return true
	default:
		return false
	}
}

// RefreshAheadCache manages the information used to connect to the AlloyDB
// instance by periodically calling the AlloyDB Admin API. It automatically
// refreshes the required information approximately 4 minutes before the
// previous certificate expires (every ~56 minutes).
//
// Internally the cache runs a single worker goroutine driven by a simple
// for-select loop. The worker is the only writer of the stored result, which
// makes every state transition linearizable and testable in isolation.
type RefreshAheadCache struct {
	// Immutable after construction.
	instanceURI    InstanceURI
	logger         debug.ContextLogger
	refreshTimeout time.Duration
	limiter        *rate.Limiter
	client         adminAPIClient
	userAgent      string
	metricRecorder telv2.MetricRecorder

	// Lifecycle.
	ctx     context.Context
	cancel  context.CancelFunc
	stopped chan struct{} // closed when run() returns

	// forceCh is a capacity-1 channel used to signal the worker to refresh
	// immediately. Sends are non-blocking: multiple ForceRefresh calls
	// collapse into at most one pending signal.
	forceCh chan struct{}

	// mu guards current. The worker takes the write lock when installing a
	// new result; readers take the read lock.
	mu      sync.RWMutex
	current *result
}

// NewRefreshAheadCache initializes a new cache that proactively refreshes the
// cached connection info.
func NewRefreshAheadCache(
	instance InstanceURI,
	l debug.ContextLogger,
	client *alloydbadmin.AlloyDBAdminClient,
	key *rsa.PrivateKey,
	refreshTimeout time.Duration,
	dialerID string,
	disableMetadataExchange bool,
	userAgent string,
	mr telv2.MetricRecorder,
) *RefreshAheadCache {
	ctx, cancel := context.WithCancel(context.Background())
	c := &RefreshAheadCache{
		instanceURI:    instance,
		logger:         l,
		refreshTimeout: refreshTimeout,
		limiter:        rate.NewLimiter(rate.Every(refreshInterval), refreshBurst),
		client:         newAdminAPIClient(client, key, dialerID, disableMetadataExchange),
		userAgent:      userAgent,
		metricRecorder: mr,
		ctx:            ctx,
		cancel:         cancel,
		stopped:        make(chan struct{}),
		forceCh:        make(chan struct{}, 1),
		// Start with a pending placeholder so the first ConnectionInfo call
		// blocks until the first refresh completes.
		current: newPendingResult(),
	}
	go c.run()
	return c
}

// Close stops the refresh loop and prevents it from making additional calls
// to the AlloyDB Admin API. It blocks until the worker has exited.
func (c *RefreshAheadCache) Close() error {
	c.cancel()
	<-c.stopped
	return nil
}

// ConnectionInfo returns connection info for the associated instance. The
// first call blocks until the initial refresh has completed (or failed);
// subsequent calls return the most recently cached result without blocking.
func (c *RefreshAheadCache) ConnectionInfo(ctx context.Context) (ConnectionInfo, error) {
	c.mu.RLock()
	r := c.current
	c.mu.RUnlock()

	select {
	case <-r.done:
		return r.info, r.err
	case <-ctx.Done():
		return ConnectionInfo{}, ctx.Err()
	case <-c.ctx.Done():
		return ConnectionInfo{}, c.ctx.Err()
	}
}

// ForceRefresh triggers an immediate refresh cycle. If the currently cached
// result is no longer usable (expired or errored), subsequent ConnectionInfo
// calls will block until the forced refresh completes.
func (c *RefreshAheadCache) ForceRefresh() {
	c.mu.Lock()
	if c.current.isReady() && !c.currentUsableLocked() {
		// Install a pending placeholder so that future callers block
		// on the result of the forced refresh rather than observing
		// the stale or errored result.
		c.current = newPendingResult()
	}
	c.mu.Unlock()

	select {
	case c.forceCh <- struct{}{}:
	default:
	}
}

// currentUsableLocked reports whether c.current is a successful result whose
// certificate has not yet expired. The caller must hold c.mu and must ensure
// c.current is ready before calling.
func (c *RefreshAheadCache) currentUsableLocked() bool {
	r := c.current
	if r.err != nil {
		return false
	}
	return time.Now().Before(r.info.Expiration)
}

// run is the single writer of c.current. It alternates between waiting for
// the next scheduled refresh (or a ForceRefresh signal) and performing a
// refresh cycle.
func (c *RefreshAheadCache) run() {
	defer close(c.stopped)

	// First iteration refreshes immediately so that ConnectionInfo callers
	// waiting on the initial pending placeholder unblock as soon as
	// possible.
	var wait time.Duration

	for {
		timer := time.NewTimer(wait)
		select {
		case <-c.ctx.Done():
			timer.Stop()
			c.shutdown()
			return
		case <-c.forceCh:
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
		}

		info, err := c.doRefresh()
		c.recordMetric(err)
		c.store(info, err)

		if err != nil {
			// Retry immediately; the rate limiter paces successive
			// attempts so this won't hot-loop.
			wait = 0
			continue
		}
		wait = refreshDuration(time.Now(), info.Expiration)
		c.logger.Debugf(
			c.ctx,
			"[%v] Connection info refresh operation scheduled at %v (now + %v)",
			c.instanceURI.String(),
			time.Now().Add(wait).UTC().Format(time.RFC3339),
			wait.Round(time.Minute),
		)
	}
}

// shutdown is called from run when c.ctx is canceled. It unblocks any
// callers waiting on a pending placeholder so they observe the cancellation
// instead of hanging.
func (c *RefreshAheadCache) shutdown() {
	c.logger.Debugf(
		context.Background(),
		"[%v] Instance is closed, stopping refresh operations",
		c.instanceURI.String(),
	)
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.current.isReady() {
		c.current.err = c.ctx.Err()
		close(c.current.done)
	}
}

// doRefresh performs a single refresh cycle: it waits for the rate limiter
// and then issues the Admin API call. It is a pure function of c's immutable
// fields and returns its outcome rather than mutating state.
func (c *RefreshAheadCache) doRefresh() (ConnectionInfo, error) {
	c.logger.Debugf(
		context.Background(),
		"[%v] Connection info refresh operation started (type = refresh ahead)",
		c.instanceURI.String(),
	)

	// The refresh timeout bounds how long we are willing to wait on the
	// rate limiter and on the Admin API call itself.
	waitCtx, cancel := context.WithTimeout(c.ctx, c.refreshTimeout)
	defer cancel()

	if err := c.limiter.Wait(waitCtx); err != nil {
		dErr := errtype.NewDialError(
			"context was canceled or expired before refresh completed",
			c.instanceURI.String(),
			nil,
		)
		c.logger.Debugf(
			waitCtx,
			"[%v] Connection info refresh operation failed, err = %v",
			c.instanceURI.String(),
			dErr,
		)
		return ConnectionInfo{}, dErr
	}

	apiCtx, apiCancel := context.WithTimeout(c.ctx, 30*time.Second)
	defer apiCancel()
	info, err := c.client.connectionInfo(apiCtx, c.instanceURI)
	if err != nil {
		c.logger.Debugf(
			c.ctx,
			"[%v] Connection info refresh operation failed, err = %v",
			c.instanceURI.String(),
			err,
		)
		return ConnectionInfo{}, err
	}

	c.logger.Debugf(
		c.ctx,
		"[%v] Connection info refresh operation complete (type = refresh ahead)",
		c.instanceURI.String(),
	)
	c.logger.Debugf(
		c.ctx,
		"[%v] Current certificate expiration = %v",
		c.instanceURI.String(),
		info.Expiration.UTC().Format(time.RFC3339),
	)
	return info, nil
}

// store installs the outcome of a refresh cycle. Its behavior depends on the
// state of c.current:
//
//  1. If c.current is a pending placeholder (first load, or created by
//     ForceRefresh while the prior result was unusable), the placeholder is
//     filled in so that waiting callers unblock.
//  2. If the new result is a success, it always replaces c.current.
//  3. If the new result is a failure and c.current is still a usable,
//     unexpired result, the error is suppressed and c.current is left
//     alone. This is intentional: transient Admin API blips should not
//     invalidate a certificate that is still good.
//  4. Otherwise, the failing result replaces c.current.
func (c *RefreshAheadCache) store(info ConnectionInfo, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.current.isReady() {
		c.current.info = info
		c.current.err = err
		close(c.current.done)
		return
	}

	if err == nil {
		c.current = newReadyResult(info, nil)
		return
	}

	if c.currentUsableLocked() {
		// Preserve the still-valid current result.
		return
	}
	c.current = newReadyResult(ConnectionInfo{}, err)
}

// recordMetric reports the outcome of a refresh cycle to the metric
// recorder. It is invoked asynchronously to match the behavior of prior
// versions.
func (c *RefreshAheadCache) recordMetric(err error) {
	status := telv2.RefreshSuccess
	if err != nil {
		status = telv2.RefreshFailure
	}
	go c.metricRecorder.RecordRefreshCount(context.Background(), telv2.Attributes{
		UserAgent:     c.userAgent,
		RefreshType:   telv2.RefreshAheadType,
		RefreshStatus: status,
	})
}

// refreshDuration returns the duration to wait before starting the next
// refresh. Usually that duration will be half of the time until certificate
// expiration.
func refreshDuration(now, certExpiry time.Time) time.Duration {
	d := certExpiry.Sub(now)
	if d < time.Hour {
		// Something is wrong with the certification, refresh now.
		if d < refreshBuffer {
			return 0
		}
		// Otherwise wait until 4 minutes before expiration for next refresh cycle.
		return d - refreshBuffer
	}
	return d / 2
}
