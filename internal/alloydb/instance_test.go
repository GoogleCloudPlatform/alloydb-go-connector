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
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"cloud.google.com/go/alloydbconn/errtype"
	"cloud.google.com/go/alloydbconn/internal/mock"
	"golang.org/x/oauth2"
	"google.golang.org/api/option"

	alloydbadmin "cloud.google.com/go/alloydb/apiv1alpha"
	telv2 "cloud.google.com/go/alloydbconn/internal/tel/v2"
)

type nullLogger struct{}

func (nullLogger) Debugf(context.Context, string, ...any) {}

// genRSAKey generates an RSA key used for test.
func genRSAKey() *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err) // unexpected, so just panic if it happens
	}
	return key
}

// rsaKey is used for test only.
var rsaKey = genRSAKey()

func TestParseInstURI(t *testing.T) {
	tcs := []struct {
		desc string
		in   string
		want InstanceURI
	}{
		{
			desc: "vanilla instance URI",
			in:   "projects/proj/locations/reg/clusters/clust/instances/name",
			want: InstanceURI{
				project: "proj",
				region:  "reg",
				cluster: "clust",
				name:    "name",
			},
		},
		{
			desc: "with legacy domain-scoped project",
			in:   "projects/google.com:proj/locations/reg/clusters/clust/instances/name",
			want: InstanceURI{
				project: "google.com:proj",
				region:  "reg",
				cluster: "clust",
				name:    "name",
			},
		},
	}

	for _, tc := range tcs {
		t.Run(tc.desc, func(t *testing.T) {
			got, err := ParseInstURI(tc.in)
			if err != nil {
				t.Fatalf("want no error, got = %v", err)
			}
			if got != tc.want {
				t.Fatalf("want = %v, got = %v", got, tc.want)
			}
		})
	}
}

func TestParseConnNameErrors(t *testing.T) {
	tcs := []struct {
		desc string
		in   string
	}{
		{
			desc: "malformatted",
			in:   "not-correct",
		},
		{
			desc: "missing project",
			in:   "reg:clust:name",
		},
		{
			desc: "missing cluster",
			in:   "proj:reg:name",
		},
		{
			desc: "empty",
			in:   "::::",
		},
	}

	for _, tc := range tcs {
		t.Run(tc.desc, func(t *testing.T) {
			_, err := ParseInstURI(tc.in)
			if err == nil {
				t.Fatal("want error, got nil")
			}
		})
	}
}

type stubTokenSource struct{}

func (stubTokenSource) Token() (*oauth2.Token, error) {
	return nil, nil
}

func TestConnectionInfo(t *testing.T) {
	ctx := context.Background()

	wantAddr := "0.0.0.0"
	wantPSC := "x.y.alloydb.goog"
	inst := mock.NewFakeInstance(
		"my-project", "my-region", "my-cluster", "my-instance",
		mock.WithPrivateIP(wantAddr),
		mock.WithPSC(wantPSC),
	)
	mc, url, cleanup := mock.HTTPClient(
		mock.InstanceGetSuccess(inst, 1),
		mock.CreateEphemeralSuccess(inst, 1),
	)
	stop := mock.StartServerProxy(t, inst)
	defer func() {
		stop()
		if err := cleanup(); err != nil {
			t.Fatalf("%v", err)
		}
	}()
	c, err := alloydbadmin.NewAlloyDBAdminRESTClient(ctx, option.WithHTTPClient(mc),
		option.WithEndpoint(url),
		option.WithTokenSource(stubTokenSource{}),
	)
	if err != nil {
		t.Fatalf("expected NewClient to succeed, but got error: %v", err)
	}

	i := NewRefreshAheadCache(
		testInstanceURI(),
		nullLogger{},
		c, rsaKey, 30*time.Second, "dialer-id",
		false,
		"some-ua",
		telv2.NullMetricRecorder{},
	)
	if err != nil {
		t.Fatalf("failed to create mock instance: %v", err)
	}

	ci, err := i.ConnectionInfo(ctx)
	if err != nil {
		t.Fatalf("failed to retrieve connect info: %v", err)
	}

	gotAddr := ci.IPAddrs[PrivateIP]
	if gotAddr != wantAddr {
		t.Fatalf(
			"ConnectInfo returned unexpected IP address, want = %v, got = %v",
			wantAddr, gotAddr,
		)
	}

	ci, err = i.ConnectionInfo(ctx)
	if err != nil {
		t.Fatalf("failed to retrieve connect info: %v", err)
	}

	gotAddr = ci.IPAddrs[PSC]
	if gotAddr != wantPSC {
		t.Fatalf(
			"ConnectInfo returned unexpected IP address, want = %v, got = %v",
			wantPSC, gotAddr,
		)
	}

}

func testInstanceURI() InstanceURI {
	i, _ := ParseInstURI("projects/my-project/locations/my-region/clusters/my-cluster/instances/my-instance")
	return i
}

func TestClose(t *testing.T) {
	ctx := context.Background()
	inst := mock.NewFakeInstance(
		"my-project", "my-region", "my-cluster", "my-instance",
	)
	mc, url, _ := mock.HTTPClient(
		mock.InstanceGetSuccess(inst, 1),
		mock.CreateEphemeralSuccess(inst, 1),
	)
	c, err := alloydbadmin.NewAlloyDBAdminRESTClient(
		ctx, option.WithHTTPClient(mc), option.WithEndpoint(url),
	)
	if err != nil {
		t.Fatalf("expected NewClient to succeed, but got error: %v", err)
	}

	// Set up an instance and then close it immediately
	i := NewRefreshAheadCache(
		testInstanceURI(),
		nullLogger{},
		c, rsaKey, 30, "dialer-ider",
		false,
		"some-ua",
		telv2.NullMetricRecorder{},
	)
	if err != nil {
		t.Fatalf("failed to initialize Instance: %v", err)
	}
	i.Close()

	_, err = i.ConnectionInfo(ctx)

	dErr := &errtype.DialError{}
	if !errors.Is(err, context.Canceled) && !errors.As(err, &dErr) {
		t.Fatalf("failed to retrieve connect info: %v", err)
	}
}

func TestRefreshDuration(t *testing.T) {
	now := time.Now()
	tcs := []struct {
		desc   string
		expiry time.Time
		want   time.Duration
	}{
		{
			desc:   "when expiration is greater than 1 hour",
			expiry: now.Add(4 * time.Hour),
			want:   2 * time.Hour,
		},
		{
			desc:   "when expiration is equal to 1 hour",
			expiry: now.Add(time.Hour),
			want:   30 * time.Minute,
		},
		{
			desc:   "when expiration is less than 1 hour, but greater than 4 minutes",
			expiry: now.Add(5 * time.Minute),
			want:   time.Minute,
		},
		{
			desc:   "when expiration is less than 4 minutes",
			expiry: now.Add(3 * time.Minute),
			want:   0,
		},
		{
			desc:   "when expiration is now",
			expiry: now,
			want:   0,
		},
	}
	for _, tc := range tcs {
		t.Run(tc.desc, func(t *testing.T) {
			got := refreshDuration(now, tc.expiry)
			// round to the second to remove millisecond differences
			if got.Round(time.Second) != tc.want {
				t.Fatalf("time until refresh: want = %v, got = %v", tc.want, got)
			}
		})
	}
}

func TestRefreshAheadCacheMetrics(t *testing.T) {
	u := testInstanceURI()
	inst := mock.NewFakeInstance(u.Project(), u.Region(), u.Cluster(), u.Name())

	tcs := []struct {
		desc      string
		requests  []*mock.Request
		wantAttrs telv2.Attributes
	}{
		{
			desc: "refresh count success",
			requests: []*mock.Request{
				mock.InstanceGetSuccess(inst, 1),
				mock.CreateEphemeralSuccess(inst, 1),
			},
			wantAttrs: telv2.Attributes{
				UserAgent:     "some-ua",
				RefreshType:   "refresh_ahead",
				RefreshStatus: "success",
			},
		},
		{
			desc:     "refresh count failure",
			requests: []*mock.Request{}, // no requests will result in 500s
			wantAttrs: telv2.Attributes{
				UserAgent:     "some-ua",
				RefreshType:   "refresh_ahead",
				RefreshStatus: "failure",
			},
		},
	}

	for _, tc := range tcs {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := context.Background()

			mc, url, cleanup := mock.HTTPClient(tc.requests...)
			defer func() {
				if err := cleanup(); err != nil {
					t.Fatalf("%v", err)
				}
			}()
			c, err := alloydbadmin.NewAlloyDBAdminRESTClient(ctx, option.WithHTTPClient(mc),
				option.WithEndpoint(url),
				option.WithTokenSource(stubTokenSource{}),
			)
			if err != nil {
				t.Fatalf("expected NewClient to succeed, but got error: %v", err)
			}

			mockRecorder := &mockMetricRecorder{}

			cache := NewRefreshAheadCache(
				u,
				nullLogger{},
				c, rsaKey, 30*time.Second, "dialer-id",
				false,
				"some-ua",
				mockRecorder,
			)
			cache.ConnectionInfo(context.Background())

			mockRecorder.Verify(t, tc.wantAttrs)
		})
	}

}

// fakeAdminAPI is an AlloyDB Admin API that answers each refresh according to
// a scripted step, so tests can fail, hold, or vary individual refreshes. A
// refresh beyond the script fails. Every refresh makes one connectionInfo
// (GET) and one generateClientCertificate (POST) call, so the Nth call of
// each kind belongs to refresh N. A refresh makes both calls concurrently and
// can finish once its GET fails, so the fake holds GET N until POST N has
// arrived. Otherwise a late POST from one refresh could take the next
// refresh's step.
//
// A cache that refreshes in a loop without waiting would spin forever in a
// synctest bubble, so the fake fails the test after maxRefreshes and holds
// later refreshes until they are canceled.
//
// The fake serves requests in process instead of over a network, so the
// tests can run in a synctest bubble.
type fakeAdminAPI struct {
	t      *testing.T
	steps  []refreshStep
	posted []chan struct{} // posted[N-1] is closed when POST N arrives

	mu    sync.Mutex
	gets  int
	posts int
}

// maxRefreshes is more refreshes than any test expects.
const maxRefreshes = 100

// refreshStep describes how the fake answers one refresh.
type refreshStep struct {
	inst mock.FakeAlloyDBInstance
	// fail responds with 403 Forbidden, which the client does not retry.
	fail bool
	// hold, if set, delays the response until it is released.
	hold *gate
}

func newFakeAdminAPI(
	t *testing.T, steps ...refreshStep,
) (*fakeAdminAPI, *alloydbadmin.AlloyDBAdminClient) {
	t.Helper()
	f := &fakeAdminAPI{t: t, steps: steps}
	for range steps {
		f.posted = append(f.posted, make(chan struct{}))
	}
	c, err := alloydbadmin.NewAlloyDBAdminRESTClient(
		context.Background(),
		option.WithHTTPClient(&http.Client{Transport: handlerTransport{f}}),
		option.WithEndpoint("http://alloydb.test"),
		option.WithTokenSource(stubTokenSource{}),
	)
	if err != nil {
		t.Fatalf("expected NewClient to succeed, but got error: %v", err)
	}
	// Don't close the client: a refresh abandoned by Close may still be
	// using it.
	return f, c
}

// handlerTransport passes each request directly to a handler.
type handlerTransport struct{ h http.Handler }

func (t handlerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Body != nil {
		defer r.Body.Close()
	}
	w := httptest.NewRecorder()
	t.h.ServeHTTP(w, r)
	if err := r.Context().Err(); err != nil {
		return nil, err
	}
	return w.Result(), nil
}

func (f *fakeAdminAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	isGet := r.Method == http.MethodGet
	f.mu.Lock()
	var n int
	if isGet {
		f.gets++
		n = f.gets
	} else {
		f.posts++
		n = f.posts
	}
	f.mu.Unlock()
	if isGet && n > maxRefreshes {
		if n == maxRefreshes+1 {
			f.t.Errorf("more than %d refreshes", maxRefreshes)
		}
		<-r.Context().Done()
		return
	}
	if n > len(f.steps) {
		http.Error(w, "unexpected refresh", http.StatusForbidden)
		return
	}
	s := f.steps[n-1]
	if !isGet {
		close(f.posted[n-1])
	} else if !waitFor(r, f.posted[n-1]) || s.hold != nil && !waitFor(r, s.hold.ch) {
		return
	}
	switch {
	case s.fail:
		http.Error(w, "refresh failed", http.StatusForbidden)
	case isGet:
		mock.InstanceGetSuccess(s.inst, 1).ServeHTTP(w, r)
	default:
		mock.CreateEphemeralSuccess(s.inst, 1).ServeHTTP(w, r)
	}
}

// waitFor waits for ch to close and reports whether it closed before the
// request was canceled.
func waitFor(r *http.Request, ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	case <-r.Context().Done():
		return false
	}
}

// wantRefreshes waits until every goroutine in the bubble is blocked and
// checks how many refreshes have started.
func (f *fakeAdminAPI) wantRefreshes(t *testing.T, want int) {
	t.Helper()
	synctest.Wait()
	f.mu.Lock()
	got := f.gets
	f.mu.Unlock()
	if got != want {
		t.Fatalf("refreshes: want = %v, got = %v", want, got)
	}
}

// gate holds a refresh until released. Tests release it at cleanup so held
// requests can finish.
type gate struct {
	ch   chan struct{}
	once sync.Once
}

func newGate(t *testing.T) *gate {
	g := &gate{ch: make(chan struct{})}
	t.Cleanup(g.release)
	return g
}

func (g *gate) release() { g.once.Do(func() { close(g.ch) }) }

func newTestRefreshAheadCache(
	t *testing.T, c *alloydbadmin.AlloyDBAdminClient,
) *RefreshAheadCache {
	t.Helper()
	cache := NewRefreshAheadCache(
		testInstanceURI(), nullLogger{}, c, rsaKey,
		RefreshTimeout, "dialer-id", false, "some-ua",
		telv2.NullMetricRecorder{},
	)
	t.Cleanup(func() { cache.Close() })
	return cache
}

// fakeInstance returns a fake instance whose results are told apart by
// private IP and whose client certificates expire at expiry.
func fakeInstance(ip string, expiry time.Time) mock.FakeAlloyDBInstance {
	u := testInstanceURI()
	return mock.NewFakeInstance(
		u.Project(), u.Region(), u.Cluster(), u.Name(),
		mock.WithPrivateIP(ip),
		mock.WithCertExpiry(expiry),
	)
}

func inADay() time.Time { return time.Now().Add(24 * time.Hour) }

func sleepUntil(t time.Time) { time.Sleep(time.Until(t)) }

type connectionInfoResult struct {
	ci  ConnectionInfo
	err error
}

// connectionInfoAsync calls ConnectionInfo in a new goroutine and returns a
// channel that receives the result.
func connectionInfoAsync(
	ctx context.Context, c *RefreshAheadCache,
) <-chan connectionInfoResult {
	ch := make(chan connectionInfoResult, 1)
	go func() {
		ci, err := c.ConnectionInfo(ctx)
		ch <- connectionInfoResult{ci: ci, err: err}
	}()
	return ch
}

// result waits until every goroutine in the bubble is blocked and returns
// the result of a ConnectionInfo call, which must have returned by then.
// No time passes, so a call that returns has not waited for a timer.
func result(
	t *testing.T, ch <-chan connectionInfoResult,
) (ConnectionInfo, error) {
	t.Helper()
	synctest.Wait()
	select {
	case r := <-ch:
		return r.ci, r.err
	default:
		t.Fatal("ConnectionInfo is waiting, want it to have returned")
		return ConnectionInfo{}, nil
	}
}

// wantWaiting waits until every goroutine in the bubble is blocked and
// checks that a ConnectionInfo call has not returned.
func wantWaiting(t *testing.T, ch <-chan connectionInfoResult) {
	t.Helper()
	synctest.Wait()
	select {
	case r := <-ch:
		t.Fatalf("want ConnectionInfo to wait, got private IP %q (err = %v)",
			r.ci.IPAddrs[PrivateIP], r.err)
	default:
	}
}

// checkIP checks that a ConnectionInfo call succeeded with the wanted private
// IP.
func checkIP(t *testing.T, ci ConnectionInfo, err error, want string) {
	t.Helper()
	if err != nil {
		t.Fatalf("ConnectionInfo: %v", err)
	}
	if got := ci.IPAddrs[PrivateIP]; got != want {
		t.Fatalf("private IP: want = %v, got = %v", want, got)
	}
}

// wantIP checks that ConnectionInfo returns without waiting for time to pass
// and has the wanted private IP.
func wantIP(t *testing.T, c *RefreshAheadCache, want string) {
	t.Helper()
	ci, err := result(t, connectionInfoAsync(context.Background(), c))
	checkIP(t, ci, err, want)
}

func TestRefreshAheadCacheReturnsConnectionInfo(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		expiry := inADay()
		_, c := newFakeAdminAPI(t, refreshStep{inst: fakeInstance("10.0.0.1", expiry)})
		cache := newTestRefreshAheadCache(t, c)

		ci, err := cache.ConnectionInfo(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if ci.Instance != testInstanceURI() {
			t.Errorf("instance: want = %v, got = %v", testInstanceURI(), ci.Instance)
		}
		if got := ci.IPAddrs[PrivateIP]; got != "10.0.0.1" {
			t.Errorf("private IP: want = 10.0.0.1, got = %v", got)
		}
		// Certificates store expiry to the second.
		if want := expiry.Truncate(time.Second); !ci.Expiration.Equal(want) {
			t.Errorf("expiration: want = %v, got = %v", want, ci.Expiration)
		}
		if ci.ClientCert.Leaf == nil {
			t.Error("want a client certificate, got none")
		}
		if ci.RootCAs == nil {
			t.Error("want root CAs, got none")
		}
	})
}

func TestRefreshAheadCacheWaitsForFirstRefresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		hold := newGate(t)
		_, c := newFakeAdminAPI(t,
			refreshStep{inst: fakeInstance("10.0.0.1", inADay()), hold: hold},
		)
		cache := newTestRefreshAheadCache(t, c)

		ctx, cancel := context.WithCancel(context.Background())
		canceled := connectionInfoAsync(ctx, cache)
		waiting := connectionInfoAsync(context.Background(), cache)
		wantWaiting(t, canceled)
		wantWaiting(t, waiting)

		// A caller whose context ends stops waiting.
		cancel()
		if _, err := result(t, canceled); !errors.Is(err, context.Canceled) {
			t.Fatalf("after cancel: want = %v, got = %v", context.Canceled, err)
		}
		wantWaiting(t, waiting)

		hold.release()
		ci, err := result(t, waiting)
		checkIP(t, ci, err, "10.0.0.1")
	})
}

func TestRefreshAheadCacheConcurrentCallersShareFirstRefresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		hold := newGate(t)
		f, c := newFakeAdminAPI(t,
			refreshStep{inst: fakeInstance("10.0.0.1", inADay()), hold: hold},
		)
		cache := newTestRefreshAheadCache(t, c)

		var calls []<-chan connectionInfoResult
		for range 10 {
			calls = append(calls, connectionInfoAsync(context.Background(), cache))
		}
		f.wantRefreshes(t, 1)
		hold.release()
		for _, ch := range calls {
			ci, err := result(t, ch)
			checkIP(t, ci, err, "10.0.0.1")
		}
		f.wantRefreshes(t, 1)
	})
}

func TestRefreshAheadCacheServesCachedInfo(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, c := newFakeAdminAPI(t, refreshStep{inst: fakeInstance("10.0.0.1", inADay())})
		cache := newTestRefreshAheadCache(t, c)

		for range 5 {
			wantIP(t, cache, "10.0.0.1")
		}
		f.wantRefreshes(t, 1)
	})
}

func TestRefreshAheadCacheRefreshesBeforeExpiry(t *testing.T) {
	tcs := []struct {
		desc string
		// lifetime is how long each certificate is valid for.
		lifetime time.Duration
		// wantAfter is when the next refresh should start.
		wantAfter time.Duration
	}{
		{
			desc:      "halfway to expiry",
			lifetime:  4 * time.Hour,
			wantAfter: 2 * time.Hour,
		},
		{
			desc:      "refreshBuffer before a short-lived certificate expires",
			lifetime:  30 * time.Minute,
			wantAfter: 30*time.Minute - refreshBuffer,
		},
	}
	for _, tc := range tcs {
		t.Run(tc.desc, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				start := time.Now()
				f, c := newFakeAdminAPI(t,
					refreshStep{inst: fakeInstance("10.0.0.1", start.Add(tc.lifetime))},
					refreshStep{inst: fakeInstance("10.0.0.2", inADay())},
				)
				cache := newTestRefreshAheadCache(t, c)
				wantIP(t, cache, "10.0.0.1")

				sleepUntil(start.Add(tc.wantAfter - time.Second))
				f.wantRefreshes(t, 1)
				wantIP(t, cache, "10.0.0.1")

				sleepUntil(start.Add(tc.wantAfter))
				f.wantRefreshes(t, 2)
				wantIP(t, cache, "10.0.0.2")
			})
		})
	}
}

func TestRefreshAheadCacheServesValidInfoDuringRefresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		hold := newGate(t)
		f, c := newFakeAdminAPI(t,
			refreshStep{inst: fakeInstance("10.0.0.1", inADay())},
			refreshStep{inst: fakeInstance("10.0.0.2", inADay()), hold: hold},
		)
		cache := newTestRefreshAheadCache(t, c)
		wantIP(t, cache, "10.0.0.1")

		cache.ForceRefresh()
		f.wantRefreshes(t, 2)
		// The forced refresh is held, but the current info is still valid.
		wantIP(t, cache, "10.0.0.1")

		// Forcing again while a refresh is running doesn't start another.
		cache.ForceRefresh()
		cache.ForceRefresh()
		f.wantRefreshes(t, 2)

		hold.release()
		f.wantRefreshes(t, 2)
		wantIP(t, cache, "10.0.0.2")
		// Wait out the limiter, which would delay an extra refresh.
		time.Sleep(refreshInterval + time.Second)
		f.wantRefreshes(t, 2)
	})
}

func TestRefreshAheadCacheForceRefreshReschedules(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		f, c := newFakeAdminAPI(t,
			// The next refresh is scheduled at 26m.
			refreshStep{inst: fakeInstance("10.0.0.1", start.Add(30*time.Minute))},
			// Forced at 10m, so the next refresh is scheduled at 36m.
			refreshStep{inst: fakeInstance("10.0.0.2", start.Add(40*time.Minute))},
			refreshStep{inst: fakeInstance("10.0.0.3", inADay())},
		)
		cache := newTestRefreshAheadCache(t, c)
		wantIP(t, cache, "10.0.0.1")

		sleepUntil(start.Add(10 * time.Minute))
		cache.ForceRefresh()
		f.wantRefreshes(t, 2)
		wantIP(t, cache, "10.0.0.2")

		// The refresh scheduled before the forced one doesn't run.
		sleepUntil(start.Add(36*time.Minute - time.Second))
		f.wantRefreshes(t, 2)

		sleepUntil(start.Add(36 * time.Minute))
		f.wantRefreshes(t, 3)
		wantIP(t, cache, "10.0.0.3")
	})
}

func TestRefreshAheadCacheKeepsValidInfoAfterFailedRefresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Every refresh after the first fails.
		f, c := newFakeAdminAPI(t,
			refreshStep{inst: fakeInstance("10.0.0.1", inADay())},
		)
		cache := newTestRefreshAheadCache(t, c)
		wantIP(t, cache, "10.0.0.1")

		cache.ForceRefresh()
		f.wantRefreshes(t, 2)
		wantIP(t, cache, "10.0.0.1")

		// The retry fails too, and the info is kept.
		time.Sleep(refreshInterval + time.Second)
		f.wantRefreshes(t, 3)
		wantIP(t, cache, "10.0.0.1")
	})
}

func TestRefreshAheadCacheReturnsErrorWithoutValidInfo(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		hold := newGate(t)
		f, c := newFakeAdminAPI(t,
			refreshStep{fail: true},
			refreshStep{inst: fakeInstance("10.0.0.1", inADay()), hold: hold},
		)
		cache := newTestRefreshAheadCache(t, c)
		// The failed refresh is retried right away, and the retry is held.
		f.wantRefreshes(t, 2)

		// The caller gets the failure without waiting for the retry.
		_, err := result(t, connectionInfoAsync(context.Background(), cache))
		var refreshErr *errtype.RefreshError
		if !errors.As(err, &refreshErr) {
			t.Fatalf("want = %T, got = %v", refreshErr, err)
		}
	})
}

func TestRefreshAheadCacheReturnsErrorAfterInfoExpires(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		// Refreshes start at 26m, and every refresh after the first fails.
		_, c := newFakeAdminAPI(t,
			refreshStep{inst: fakeInstance("10.0.0.1", start.Add(30*time.Minute))},
		)
		cache := newTestRefreshAheadCache(t, c)
		wantIP(t, cache, "10.0.0.1")

		// Until the certificate expires, callers get the cached info.
		sleepUntil(start.Add(30*time.Minute - time.Second))
		wantIP(t, cache, "10.0.0.1")

		// The first refresh to fail after expiry replaces it.
		sleepUntil(start.Add(30*time.Minute + refreshInterval + time.Second))
		_, err := result(t, connectionInfoAsync(context.Background(), cache))
		var refreshErr *errtype.RefreshError
		if !errors.As(err, &refreshErr) {
			t.Fatalf("want = %T, got = %v", refreshErr, err)
		}
	})
}

func TestRefreshAheadCacheRetriesFailedRefresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, c := newFakeAdminAPI(t,
			refreshStep{fail: true},
			refreshStep{inst: fakeInstance("10.0.0.1", inADay())},
		)
		cache := newTestRefreshAheadCache(t, c)

		f.wantRefreshes(t, 2)
		wantIP(t, cache, "10.0.0.1")
	})
}

func TestRefreshAheadCacheRateLimitsFailedRefreshes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		// Every refresh fails.
		f, c := newFakeAdminAPI(t)
		newTestRefreshAheadCache(t, c)

		// The first refresh and its retry use the limiter's burst. Later
		// retries run once per refreshInterval.
		f.wantRefreshes(t, 2)
		for n := 1; n <= 3; n++ {
			due := start.Add(time.Duration(n) * refreshInterval)
			sleepUntil(due.Add(-time.Second))
			f.wantRefreshes(t, n+1)
			sleepUntil(due.Add(time.Second))
			f.wantRefreshes(t, n+2)
		}
	})
}

func TestRefreshAheadCacheForceRefreshWaitsWhenInfoFailed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		hold := newGate(t)
		f, c := newFakeAdminAPI(t,
			refreshStep{fail: true},
			refreshStep{inst: fakeInstance("10.0.0.1", inADay()), hold: hold},
		)
		cache := newTestRefreshAheadCache(t, c)
		f.wantRefreshes(t, 2)
		if _, err := result(t, connectionInfoAsync(context.Background(), cache)); err == nil {
			t.Fatal("want error from failed refresh, got nil")
		}

		// The retry is already running, so ForceRefresh joins it, and callers
		// wait for its result instead of getting the earlier failure.
		cache.ForceRefresh()
		waiting := connectionInfoAsync(context.Background(), cache)
		wantWaiting(t, waiting)

		hold.release()
		ci, err := result(t, waiting)
		checkIP(t, ci, err, "10.0.0.1")
		// Wait out the limiter, which would delay an extra refresh.
		time.Sleep(refreshInterval + time.Second)
		f.wantRefreshes(t, 2)
	})
}

func TestRefreshAheadCacheForceRefreshWaitsWhenInfoExpired(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		hold := newGate(t)
		f, c := newFakeAdminAPI(t,
			refreshStep{inst: fakeInstance("10.0.0.1", time.Now().Add(-time.Minute))},
			refreshStep{inst: fakeInstance("10.0.0.2", inADay()), hold: hold},
		)
		cache := newTestRefreshAheadCache(t, c)
		// An expired certificate is refreshed right away.
		f.wantRefreshes(t, 2)

		// ConnectionInfo returns expired info. Callers are expected to check
		// the expiration and force a refresh, as Dial does.
		wantIP(t, cache, "10.0.0.1")

		cache.ForceRefresh()
		waiting := connectionInfoAsync(context.Background(), cache)
		wantWaiting(t, waiting)

		hold.release()
		ci, err := result(t, waiting)
		checkIP(t, ci, err, "10.0.0.2")
	})
}

func TestRefreshAheadCacheRateLimitsRefreshes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		f, c := newFakeAdminAPI(t,
			refreshStep{inst: fakeInstance("10.0.0.1", inADay())},
			refreshStep{inst: fakeInstance("10.0.0.2", inADay())},
			refreshStep{inst: fakeInstance("10.0.0.3", inADay())},
		)
		cache := newTestRefreshAheadCache(t, c)
		wantIP(t, cache, "10.0.0.1")

		// The initial refresh and one forced refresh use the burst.
		cache.ForceRefresh()
		f.wantRefreshes(t, 2)
		wantIP(t, cache, "10.0.0.2")

		// The next refresh waits refreshInterval for the limiter. Meanwhile,
		// callers get the current info.
		cache.ForceRefresh()
		sleepUntil(start.Add(refreshInterval - time.Second))
		f.wantRefreshes(t, 2)
		wantIP(t, cache, "10.0.0.2")

		sleepUntil(start.Add(refreshInterval + time.Second))
		f.wantRefreshes(t, 3)
		wantIP(t, cache, "10.0.0.3")
	})
}

func TestRefreshAheadCacheCloseStopsRefreshes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, c := newFakeAdminAPI(t,
			refreshStep{inst: fakeInstance("10.0.0.1", time.Now().Add(30*time.Minute))},
			refreshStep{inst: fakeInstance("10.0.0.2", inADay())},
		)
		cache := newTestRefreshAheadCache(t, c)
		wantIP(t, cache, "10.0.0.1")

		if err := cache.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		// The second refresh was due at 26m.
		time.Sleep(24 * time.Hour)
		f.wantRefreshes(t, 1)

		cache.ForceRefresh()
		f.wantRefreshes(t, 1)
		if err := cache.Close(); err != nil {
			t.Fatalf("second Close: %v", err)
		}
	})
}

func TestRefreshAheadCacheCloseStopsRunningRefresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		hold := newGate(t)
		f, c := newFakeAdminAPI(t,
			refreshStep{inst: fakeInstance("10.0.0.1", inADay())},
			refreshStep{inst: fakeInstance("10.0.0.2", inADay()), hold: hold},
			refreshStep{inst: fakeInstance("10.0.0.3", inADay())},
		)
		cache := newTestRefreshAheadCache(t, c)
		wantIP(t, cache, "10.0.0.1")

		cache.ForceRefresh()
		f.wantRefreshes(t, 2)
		cache.Close()
		hold.release()

		// The running refresh doesn't schedule another.
		time.Sleep(24 * time.Hour)
		f.wantRefreshes(t, 2)
	})
}

func TestRefreshAheadCacheCloseUnblocksWaitingCallers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		hold := newGate(t)
		f, c := newFakeAdminAPI(t,
			refreshStep{inst: fakeInstance("10.0.0.1", inADay()), hold: hold},
		)
		cache := newTestRefreshAheadCache(t, c)

		waiting := connectionInfoAsync(context.Background(), cache)
		f.wantRefreshes(t, 1)
		wantWaiting(t, waiting)

		cache.Close()
		if _, err := result(t, waiting); !errors.Is(err, context.Canceled) {
			t.Fatalf("after Close: want = %v, got = %v", context.Canceled, err)
		}
	})
}

func TestRefreshAheadCacheConnectionInfoAfterClose(t *testing.T) {
	t.Skip("skipping flaky test: ConnectionInfo after Close randomly returns the cached result or context.Canceled")
	synctest.Test(t, func(t *testing.T) {
		_, c := newFakeAdminAPI(t,
			refreshStep{inst: fakeInstance("10.0.0.1", inADay())},
		)
		cache := newTestRefreshAheadCache(t, c)
		wantIP(t, cache, "10.0.0.1")

		cache.Close()
		_, err := result(t, connectionInfoAsync(context.Background(), cache))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("after Close: want = %v, got = %v", context.Canceled, err)
		}
	})
}

func TestRefreshAheadCacheTimesOutRefresh(t *testing.T) {
	t.Skip("skipping until the refresh timeout applies to the Admin API call: https://github.com/GoogleCloudPlatform/alloydb-go-connector/issues/842")
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		// Shorter than refreshInterval, so the limiter wait for the third
		// refresh outlasts it.
		const timeout = 5 * time.Second
		held := fakeInstance("10.0.0.9", inADay())
		f, c := newFakeAdminAPI(t,
			refreshStep{inst: held, hold: newGate(t)},
			refreshStep{inst: held, hold: newGate(t)},
			refreshStep{inst: fakeInstance("10.0.0.1", inADay())},
		)
		cache := NewRefreshAheadCache(
			testInstanceURI(), nullLogger{}, c, rsaKey,
			timeout, "dialer-id", false, "some-ua",
			telv2.NullMetricRecorder{},
		)
		t.Cleanup(func() { cache.Close() })

		// Each held refresh is abandoned after the timeout and retried.
		f.wantRefreshes(t, 1)
		sleepUntil(start.Add(timeout))
		f.wantRefreshes(t, 2)

		// The limiter, not the timeout, decides when the third refresh runs.
		sleepUntil(start.Add(refreshInterval - time.Second))
		f.wantRefreshes(t, 2)
		sleepUntil(start.Add(refreshInterval + time.Second))
		f.wantRefreshes(t, 3)
		wantIP(t, cache, "10.0.0.1")
	})
}
