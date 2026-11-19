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

	// RefreshTimeout is the maximum amount of time to wait for the AlloyDB
	// Admin API calls of a refresh to complete. It does not include time
	// spent waiting on the rate limiter.
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

// errCacheClosed is returned by ConnectionInfo once the cache is closed.
var errCacheClosed = fmt.Errorf("refresh ahead cache is closed: %w", context.Canceled)

// RefreshAheadCache manages the information used to connect to the AlloyDB
// instance by periodically calling the AlloyDB Admin API. A single goroutine
// refreshes the information in the background, halfway to the client
// certificate's expiration, or refreshBuffer before it expires if the
// certificate is short-lived.
type RefreshAheadCache struct {
	instanceURI InstanceURI
	logger      debug.ContextLogger
	// refreshTimeout bounds each call to the AlloyDB Admin API.
	refreshTimeout time.Duration
	// limiter controls the rate at which refreshes run.
	limiter        *rate.Limiter
	client         adminAPIClient
	userAgent      string
	metricRecorder telv2.MetricRecorder

	mu sync.Mutex
	// info and err hold the result callers should use: info from the latest
	// successful refresh, or the error from the latest failed refresh if
	// there was no valid info to keep.
	info ConnectionInfo
	err  error
	// pending, when non-nil, is closed by the next publish. Callers wait on
	// it instead of using info and err.
	pending chan struct{}

	// force asks run to refresh now. It holds at most one request.
	force  chan struct{}
	cancel context.CancelFunc
	// done is closed when run returns.
	done chan struct{}
}

// NewRefreshAheadCache initializes a new cache that proactively refreshes the
// cache's connection info.
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
		// Callers wait for the first refresh.
		pending: make(chan struct{}),
		force:   make(chan struct{}, 1),
		cancel:  cancel,
		done:    make(chan struct{}),
	}
	go c.run(ctx)
	return c
}

// ConnectionInfo returns the cached connection info. Before the first refresh
// finishes, and after ForceRefresh when the cached info is not valid, it
// waits for the next refresh. The info may be expired; callers should check
// its expiration and call ForceRefresh if needed.
func (c *RefreshAheadCache) ConnectionInfo(ctx context.Context) (ConnectionInfo, error) {
	c.mu.Lock()
	pending, info, err := c.pending, c.info, c.err
	c.mu.Unlock()
	if pending != nil {
		select {
		case <-pending:
		case <-ctx.Done():
			return ConnectionInfo{}, ctx.Err()
		case <-c.done:
		}
		c.mu.Lock()
		info, err = c.info, c.err
		c.mu.Unlock()
	}
	select {
	case <-c.done:
		return ConnectionInfo{}, errCacheClosed
	default:
	}
	return info, err
}

// ForceRefresh starts a refresh now, or joins one that is already running. If
// the cached info is not valid, ConnectionInfo waits for that refresh.
func (c *RefreshAheadCache) ForceRefresh() {
	c.mu.Lock()
	if c.pending == nil && !c.validLocked() {
		c.pending = make(chan struct{})
	}
	c.mu.Unlock()
	select {
	case c.force <- struct{}{}:
	default: // a refresh is already requested
	}
}

// Close stops the refresh cycle and waits for it to exit, so the cache makes
// no further calls to the AlloyDB Admin API. It is safe to call more than
// once.
func (c *RefreshAheadCache) Close() error {
	c.cancel()
	<-c.done
	return nil
}

// run is the refresh cycle. It is the only code that refreshes or schedules
// refreshes, and it returns when ctx is canceled.
func (c *RefreshAheadCache) run(ctx context.Context) {
	defer close(c.done)
	defer c.logger.Debugf(
		context.Background(),
		"[%v] Instance is closed, stopping refresh operations",
		c.instanceURI.String(),
	)
	t := time.NewTimer(0) // the first refresh runs immediately
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-c.force:
		}
		info, err := c.refresh(ctx)
		if ctx.Err() != nil {
			return
		}
		// This refresh satisfies any ForceRefresh made while it ran.
		select {
		case <-c.force:
		default:
		}
		d := c.publish(info, err)
		c.logger.Debugf(
			ctx,
			"[%v] Connection info refresh operation scheduled at %v (now + %v)",
			c.instanceURI.String(),
			time.Now().Add(d).UTC().Format(time.RFC3339),
			d.Round(time.Minute),
		)
		t.Reset(d)
	}
}

// refresh waits for the rate limiter, then fetches new connection info from
// the AlloyDB Admin API.
func (c *RefreshAheadCache) refresh(ctx context.Context) (ConnectionInfo, error) {
	c.logger.Debugf(
		ctx,
		"[%v] Connection info refresh operation started (type = refresh ahead)",
		c.instanceURI.String(),
	)
	// Wait without the refresh timeout: the limiter may need longer, and
	// a wait that fails immediately would be retried immediately.
	if err := c.limiter.Wait(ctx); err != nil {
		return ConnectionInfo{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, c.refreshTimeout)
	defer cancel()
	info, err := c.client.connectionInfo(ctx, c.instanceURI)

	status := telv2.RefreshSuccess
	if err != nil {
		status = telv2.RefreshFailure
		c.logger.Debugf(
			ctx,
			"[%v] Connection info refresh operation failed, err = %v",
			c.instanceURI.String(),
			err,
		)
	} else {
		c.logger.Debugf(
			ctx,
			"[%v] Connection info refresh operation complete (type = refresh ahead)",
			c.instanceURI.String(),
		)
		c.logger.Debugf(
			ctx,
			"[%v] Current certificate expiration = %v",
			c.instanceURI.String(),
			info.Expiration.UTC().Format(time.RFC3339),
		)
	}
	go c.metricRecorder.RecordRefreshCount(context.Background(), telv2.Attributes{
		UserAgent:     c.userAgent,
		RefreshType:   telv2.RefreshAheadType,
		RefreshStatus: status,
	})
	return info, err
}

// publish records a refresh result, wakes callers waiting for it, and returns
// how long to wait before the next refresh.
func (c *RefreshAheadCache) publish(info ConnectionInfo, err error) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Keep valid info rather than replace it with an error.
	if err == nil || !c.validLocked() {
		c.info, c.err = info, err
	}
	if c.pending != nil {
		close(c.pending)
		c.pending = nil
	}
	if err != nil {
		// Retry now. The limiter spaces out repeated failures.
		return 0
	}
	return refreshDuration(time.Now(), info.Expiration)
}

// validLocked reports whether the cached info came from a successful refresh
// and has not expired. c.mu must be held.
func (c *RefreshAheadCache) validLocked() bool {
	return c.err == nil && !time.Now().After(c.info.Expiration)
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
