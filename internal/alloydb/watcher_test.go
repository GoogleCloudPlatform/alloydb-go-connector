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
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testURI2 = "projects/my-proj/locations/us-east1/clusters/my-cluster-2/instances/my-inst-2"

// resolverFunc adapts a function to InstanceResolver.
type resolverFunc func(context.Context, string) (DialTarget, error)

func (f resolverFunc) Resolve(ctx context.Context, name string) (DialTarget, error) {
	return f(ctx, name)
}

// fakeInstanceResolver resolves every name to a canned target or error.
type fakeInstanceResolver struct {
	mu     sync.Mutex
	target DialTarget
	err    error
}

func (f *fakeInstanceResolver) Resolve(context.Context, string) (DialTarget, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.target, f.err
}

func (f *fakeInstanceResolver) set(t DialTarget, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.target, f.err = t, err
}

// fakeConn records whether it has been closed. Only Close is implemented.
type fakeConn struct {
	net.Conn
	closed atomic.Bool
}

func (c *fakeConn) Close() error {
	c.closed.Store(true)
	return nil
}

func dnsTarget(t *testing.T, uri string) DialTarget {
	return DialTarget{URI: mustParse(t, uri), DomainName: testEndpoint}
}

// newTestDNSWatcher returns a watcher for testEndpoint, the fake resolver it
// uses (pointing to testURI), and a conn to testURI it tracks.
func newTestDNSWatcher(t *testing.T, period time.Duration) (*DNSWatcher, *fakeInstanceResolver, *fakeConn) {
	t.Helper()
	r := &fakeInstanceResolver{target: dnsTarget(t, testURI)}
	w := NewDNSWatcher(testEndpoint, r, nullLogger{}, period)
	t.Cleanup(w.Stop)
	conn := &fakeConn{}
	w.Add(conn, mustParse(t, testURI))
	return w, r, conn
}

// eventuallyClosed reports whether conn is closed within a few seconds.
// Watchers close connections in the background.
func eventuallyClosed(conn *fakeConn) bool {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if conn.closed.Load() {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

func TestDNSWatcherCheck(t *testing.T) {
	tcs := []struct {
		desc       string
		uri        string
		err        error
		wantClosed bool
	}{
		{
			desc: "same instance keeps connections",
			uri:  testURI,
		},
		{
			// A failed lookup is likely transient.
			desc: "failed lookup keeps connections",
			err:  errors.New("DNS unavailable"),
		},
		{
			desc:       "different instance closes connections",
			uri:        testURI2,
			wantClosed: true,
		},
	}
	for _, tc := range tcs {
		t.Run(tc.desc, func(t *testing.T) {
			// The period is long enough that only the explicit check runs.
			w, r, conn := newTestDNSWatcher(t, time.Hour)
			var tgt DialTarget
			if tc.uri != "" {
				tgt = dnsTarget(t, tc.uri)
			}
			r.set(tgt, tc.err)
			w.check(context.Background())

			if tc.wantClosed {
				if !eventuallyClosed(conn) {
					t.Error("expected conn to be closed")
				}
				return
			}
			if conn.closed.Load() {
				t.Error("expected conn to stay open")
			}
		})
	}
}

func TestDNSWatcherCheckClosesOnlyStaleConns(t *testing.T) {
	// During a switchover the name can have conns to both the old and the
	// new instance. Only conns to the old instance are closed.
	w, r, oldConn := newTestDNSWatcher(t, time.Hour)
	removed := &fakeConn{}
	w.Add(removed, mustParse(t, testURI))
	w.Remove(removed)
	newConn := &fakeConn{}
	w.Add(newConn, mustParse(t, testURI2))

	r.set(dnsTarget(t, testURI2), nil)
	w.check(context.Background())

	if !eventuallyClosed(oldConn) {
		t.Error("expected conn to the old instance to be closed")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.conns[newConn]; !ok || len(w.conns) != 1 {
		t.Errorf("tracked conns = %v, want only the conn to the new instance", w.conns)
	}
}

func TestDNSWatcherCheckSkipsLookupWithoutConns(t *testing.T) {
	var lookups atomic.Int32
	r := resolverFunc(func(context.Context, string) (DialTarget, error) {
		lookups.Add(1)
		return dnsTarget(t, testURI), nil
	})
	// The period is long enough that only the explicit checks run.
	w := NewDNSWatcher(testEndpoint, r, nullLogger{}, time.Hour)
	t.Cleanup(w.Stop)

	w.check(context.Background())
	if got := lookups.Load(); got != 0 {
		t.Fatalf("lookups with no conns = %d, want 0", got)
	}

	conn := &fakeConn{}
	w.Add(conn, mustParse(t, testURI))
	w.check(context.Background())
	if got := lookups.Load(); got != 1 {
		t.Errorf("lookups with a conn = %d, want 1", got)
	}
}

func TestDNSWatcherChecksPeriodically(t *testing.T) {
	_, r, conn := newTestDNSWatcher(t, time.Millisecond)
	r.set(dnsTarget(t, testURI2), nil)

	if !eventuallyClosed(conn) {
		t.Fatal("expected conn to be closed after the record changed")
	}
}

func TestDNSWatcherStopCancelsCheck(t *testing.T) {
	var once sync.Once
	started := make(chan struct{})
	returned := make(chan error, 1)
	// The resolver would report a different instance, but blocks until the
	// check is canceled.
	r := resolverFunc(func(ctx context.Context, _ string) (DialTarget, error) {
		once.Do(func() { close(started) })
		<-ctx.Done()
		select {
		case returned <- ctx.Err():
		default:
		}
		return dnsTarget(t, testURI2), ctx.Err()
	})
	w := NewDNSWatcher(testEndpoint, r, nullLogger{}, time.Millisecond)
	conn := &fakeConn{}
	w.Add(conn, mustParse(t, testURI))

	<-started
	w.Stop()
	if err := <-returned; !errors.Is(err, context.Canceled) {
		t.Fatalf("check ctx err = %v, want context.Canceled", err)
	}
	if conn.closed.Load() {
		t.Error("expected Stop to leave connections open")
	}
}
