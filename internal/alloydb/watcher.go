// Copyright 2026 Google LLC
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
	"net"
	"sync"
	"time"

	"cloud.google.com/go/alloydbconn/debug"
)

// dnsCheckTimeout bounds each DNS check so a hung lookup cannot stall the
// watcher. It allows one full retry cycle of the default resolver
// configuration (two 5s attempts). A check that times out keeps connections
// open until the next check. On Windows, the system resolver does not honor
// context cancellation, so this bound does not apply there.
const dnsCheckTimeout = 10 * time.Second

// DNSWatcher tracks the connections dialed through a DNS name (e.g. a write
// endpoint) and periodically re-resolves the name's TXT record. Each check
// closes the connections to instances other than the one the name points to,
// so connection pools reconnect to the new instance.
//
// Because every check compares each connection to a fresh lookup, a
// connection dialed with a stale DNS answer, or dialed while the name moved,
// is closed by the next check.
//
// A DNSWatcher is deliberately separate from the connection info cache, which
// is keyed by instance URI. Evicting a cache entry (e.g. after a refresh
// error) neither closes nor stops tracking connections dialed by DNS name.
type DNSWatcher struct {
	// name is the DNS name being watched, e.g. a cluster write endpoint. Its
	// TXT record holds the URI of the instance it currently points to.
	name     string
	resolver InstanceResolver
	logger   debug.ContextLogger

	// stop ends the watch goroutine and cancels any check in progress.
	stop context.CancelFunc

	mu sync.Mutex
	// conns maps each open connection dialed through name to its instance.
	conns map[net.Conn]InstanceURI
}

// NewDNSWatcher returns a watcher that re-resolves name on the given period,
// which must be positive, until it is stopped.
func NewDNSWatcher(
	name string,
	resolver InstanceResolver,
	l debug.ContextLogger,
	period time.Duration,
) *DNSWatcher {
	ctx, cancel := context.WithCancel(context.Background())
	w := &DNSWatcher{
		name:     name,
		resolver: resolver,
		logger:   l,
		stop:     cancel,
		conns:    make(map[net.Conn]InstanceURI),
	}
	go w.watch(ctx, period)
	return w
}

func (w *DNSWatcher) watch(ctx context.Context, period time.Duration) {
	t := time.NewTicker(period)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			checkCtx, cancel := context.WithTimeout(ctx, dnsCheckTimeout)
			w.check(checkCtx)
			cancel()
		case <-ctx.Done():
			return
		}
	}
}

// check re-resolves the DNS name and closes every tracked connection to an
// instance other than the one the name now points to. A failed lookup keeps
// existing connections open: it is more likely a transient DNS error than a
// change of instance. With no tracked connections there is nothing to close,
// so the lookup is skipped.
func (w *DNSWatcher) check(ctx context.Context) {
	w.mu.Lock()
	n := len(w.conns)
	w.mu.Unlock()
	if n == 0 {
		return
	}
	t, err := w.resolver.Resolve(ctx, w.name)
	if err != nil {
		w.logger.Debugf(ctx, "[%v] DNS name check failed, keeping connections: %v", w.name, err)
		return
	}
	w.mu.Lock()
	var stale []net.Conn
	for c, uri := range w.conns {
		if uri != t.URI {
			stale = append(stale, c)
			delete(w.conns, c)
		}
	}
	w.mu.Unlock()

	if len(stale) == 0 {
		return
	}
	w.logger.Debugf(
		ctx, "[%v] DNS name points to %v, closing %d connection(s) to other instances",
		w.name, t.URI.String(), len(stale),
	)
	// Close concurrently: closing a TLS connection may block for a few
	// seconds sending close_notify to an unresponsive instance.
	for _, c := range stale {
		go func() { _ = c.Close() }()
	}
}

// Add tracks conn, which was dialed to uri.
func (w *DNSWatcher) Add(conn net.Conn, uri InstanceURI) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.conns[conn] = uri
}

// Remove stops tracking conn.
func (w *DNSWatcher) Remove(conn net.Conn) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.conns, conn)
}

// Stop ends the periodic DNS check, canceling any check in progress, without
// closing any connections.
func (w *DNSWatcher) Stop() {
	w.stop()
}
