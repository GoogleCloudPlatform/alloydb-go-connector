// Copyright 2024 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	https://www.apache.org/licenses/LICENSE-2.0
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
	"sync"
	"time"

	alloydbadmin "cloud.google.com/go/alloydb/apiv1alpha"
	"cloud.google.com/go/alloydbconn/debug"
	telv2 "cloud.google.com/go/alloydbconn/internal/tel/v2"
)

// LazyRefreshCache is caches connection info and refreshes the cache only when
// a caller requests connection info and the current certificate is expired.
type LazyRefreshCache struct {
	uri            InstanceURI
	logger         debug.ContextLogger
	r              adminAPIClient
	mu             sync.Mutex
	needsRefresh   bool
	cached         ConnectionInfo
	userAgent      string
	metricRecorder telv2.MetricRecorder
}

// NewLazyRefreshCache initializes a new LazyRefreshCache.
func NewLazyRefreshCache(
	uri InstanceURI,
	l debug.ContextLogger,
	client *alloydbadmin.AlloyDBAdminClient,
	key *rsa.PrivateKey,
	_ time.Duration,
	dialerID string,
	disableMetadataExchange bool,
	userAgent string,
	mr telv2.MetricRecorder,
) *LazyRefreshCache {
	return &LazyRefreshCache{
		uri:            uri,
		logger:         l,
		r:              newAdminAPIClient(client, key, dialerID, disableMetadataExchange),
		userAgent:      userAgent,
		metricRecorder: mr,
	}
}

// ConnectionInfo returns connection info for the associated instance. New
// connection info is retrieved under two conditions:
// - the current connection info's certificate has expired, or
// - a caller has separately called ForceRefresh
func (c *LazyRefreshCache) ConnectionInfo(
	ctx context.Context,
) (ConnectionInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// strip monotonic clock with UTC()
	now := time.Now().UTC()
	// Pad expiration with a buffer to give the client plenty of time to
	// establish a connection to the server with the certificate.
	exp := c.cached.Expiration.UTC().Add(-refreshBuffer)
	if !c.needsRefresh && now.Before(exp) {
		c.logger.Debugf(
			ctx,
			"[%v] Connection info is still valid, using cached info",
			c.uri.String(),
		)
		return c.cached, nil
	}

	c.logger.Debugf(
		ctx,
		"[%v] Connection info refresh operation started",
		c.uri.String(),
	)
	ci, err := c.r.connectionInfo(ctx, c.uri)
	if err != nil {
		c.logger.Debugf(
			ctx,
			"[%v] Connection info refresh operation failed, err = %v",
			c.uri.String(),
			err,
		)
		go c.metricRecorder.RecordRefreshCount(ctx, telv2.Attributes{
			UserAgent:     c.userAgent,
			RefreshType:   telv2.RefreshLazyType,
			RefreshStatus: telv2.RefreshFailure,
		})
		return ConnectionInfo{}, err
	}
	go c.metricRecorder.RecordRefreshCount(ctx, telv2.Attributes{
		UserAgent:     c.userAgent,
		RefreshType:   telv2.RefreshLazyType,
		RefreshStatus: telv2.RefreshSuccess,
	})
	c.logger.Debugf(
		ctx,
		"[%v] Connection info refresh operation complete",
		c.uri.String(),
	)
	c.logger.Debugf(
		ctx,
		"[%v] Current certificate expiration = %v",
		c.uri.String(),
		ci.Expiration.UTC().Format(time.RFC3339),
	)
	c.cached = ci
	c.needsRefresh = false
	return ci, nil
}

// ForceRefresh invalidates the caches and configures the next call to
// ConnectionInfo to retrieve a fresh connection info.
func (c *LazyRefreshCache) ForceRefresh() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.needsRefresh = true
}

// Close is a no-op and provided purely for a consistent interface with other
// caching types.
func (c *LazyRefreshCache) Close() error {
	return nil
}
