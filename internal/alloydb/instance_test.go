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

func TestConnectInfoErrors(t *testing.T) {
	ctx := context.Background()
	c, err := alloydbadmin.NewAlloyDBAdminRESTClient(
		ctx, option.WithTokenSource(stubTokenSource{}),
	)
	if err != nil {
		t.Fatalf("expected NewClient to succeed, but got error: %v", err)
	}

	// Use a timeout that should fail instantly
	i := NewRefreshAheadCache(
		testInstanceURI(),
		nullLogger{},
		c, rsaKey, 0, "dialer-id",
		false,
		"some-ua",
		telv2.NullMetricRecorder{},
	)
	if err != nil {
		t.Fatalf("failed to initialize Instance: %v", err)
	}

	_, err = i.ConnectionInfo(ctx)
	var wantErr *errtype.DialError
	if !errors.As(err, &wantErr) {
		t.Fatalf("when connect info fails, want = %T, got = %v", wantErr, err)
	}
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
// each kind belongs to refresh N.
type fakeAdminAPI struct {
	steps   []refreshStep
	started chan int // receives each refresh's number as it starts

	mu    sync.Mutex
	gets  int
	posts int
}

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
	f := &fakeAdminAPI{steps: steps, started: make(chan int, 100)}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c, err := alloydbadmin.NewAlloyDBAdminRESTClient(
		context.Background(),
		option.WithHTTPClient(srv.Client()),
		option.WithEndpoint(srv.URL),
		option.WithTokenSource(stubTokenSource{}),
	)
	if err != nil {
		t.Fatalf("expected NewClient to succeed, but got error: %v", err)
	}
	// Don't close the client: a refresh abandoned by Close may still be
	// using it.
	return f, c
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
	if isGet {
		select {
		case f.started <- n:
		default:
		}
	}
	if n > len(f.steps) {
		http.Error(w, "unexpected refresh", http.StatusForbidden)
		return
	}
	s := f.steps[n-1]
	if isGet && s.hold != nil {
		select {
		case <-s.hold.ch:
		case <-r.Context().Done():
			return
		}
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

// refreshes reports how many refreshes have started.
func (f *fakeAdminAPI) refreshes() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gets
}

// waitForRefresh waits until refresh n has started.
func (f *fakeAdminAPI) waitForRefresh(t *testing.T, n int) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case got := <-f.started:
			if got >= n {
				return
			}
		case <-timeout:
			t.Fatalf("refresh %d did not start, refreshes = %d", n, f.refreshes())
		}
	}
}

// forceRefresh calls ForceRefresh and waits for refresh n to start.
//
// RefreshAheadCache drops a ForceRefresh that arrives after a refresh's result
// is ready but before the next refresh is scheduled, so this retries until
// the refresh starts.
func (f *fakeAdminAPI) forceRefresh(t *testing.T, c *RefreshAheadCache, n int) {
	t.Helper()
	for range 50 {
		c.ForceRefresh()
		select {
		case got := <-f.started:
			if got >= n {
				return
			}
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Fatalf("refresh %d did not start, refreshes = %d", n, f.refreshes())
}

// gate holds a refresh until released. Tests release it at cleanup so the
// fake's server can shut down.
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

// settle gives the cache time to act on a refresh the test can't observe
// directly, such as recording a failed refresh or skipping a refresh.
func settle() { time.Sleep(500 * time.Millisecond) }

func newTestRefreshAheadCache(
	t *testing.T, c *alloydbadmin.AlloyDBAdminClient,
) *RefreshAheadCache {
	t.Helper()
	cache := NewRefreshAheadCache(
		testInstanceURI(), nullLogger{}, c, rsaKey,
		30*time.Second, "dialer-id", false, "some-ua",
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

// wantIP fetches connection info and checks it has the wanted private IP.
func wantIP(t *testing.T, c *RefreshAheadCache, want string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ci, err := c.ConnectionInfo(ctx)
	if err != nil {
		t.Fatalf("ConnectionInfo: %v", err)
	}
	if got := ci.IPAddrs[PrivateIP]; got != want {
		t.Fatalf("private IP: want = %v, got = %v", want, got)
	}
}

// eventuallyIP waits for the cache to serve connection info with the wanted
// private IP.
func eventuallyIP(t *testing.T, c *RefreshAheadCache, want string) {
	t.Helper()
	var (
		got string
		err error
	)
	for range 50 {
		var ci ConnectionInfo
		ci, err = c.ConnectionInfo(context.Background())
		got = ci.IPAddrs[PrivateIP]
		if err == nil && got == want {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("private IP: want = %v, got = %v (err = %v)", want, got, err)
}

func TestRefreshAheadCacheReturnsConnectionInfo(t *testing.T) {
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
}

func TestRefreshAheadCacheWaitsForFirstRefresh(t *testing.T) {
	hold := newGate(t)
	f, c := newFakeAdminAPI(t,
		refreshStep{inst: fakeInstance("10.0.0.1", inADay()), hold: hold},
	)
	cache := newTestRefreshAheadCache(t, c)
	f.waitForRefresh(t, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := cache.ConnectionInfo(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("before first refresh: want = %v, got = %v", context.DeadlineExceeded, err)
	}

	hold.release()
	wantIP(t, cache, "10.0.0.1")
}

func TestRefreshAheadCacheConcurrentCallersShareFirstRefresh(t *testing.T) {
	hold := newGate(t)
	f, c := newFakeAdminAPI(t,
		refreshStep{inst: fakeInstance("10.0.0.1", inADay()), hold: hold},
	)
	cache := newTestRefreshAheadCache(t, c)

	const callers = 10
	errs := make(chan error, callers)
	for range callers {
		go func() {
			ci, err := cache.ConnectionInfo(context.Background())
			if err == nil && ci.IPAddrs[PrivateIP] != "10.0.0.1" {
				err = errors.New("unexpected private IP " + ci.IPAddrs[PrivateIP])
			}
			errs <- err
		}()
	}
	f.waitForRefresh(t, 1)
	hold.release()
	for range callers {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if got := f.refreshes(); got != 1 {
		t.Fatalf("refreshes: want = 1, got = %v", got)
	}
}

func TestRefreshAheadCacheServesCachedInfo(t *testing.T) {
	f, c := newFakeAdminAPI(t, refreshStep{inst: fakeInstance("10.0.0.1", inADay())})
	cache := newTestRefreshAheadCache(t, c)

	for range 5 {
		wantIP(t, cache, "10.0.0.1")
	}
	settle()
	if got := f.refreshes(); got != 1 {
		t.Fatalf("refreshes: want = 1, got = %v", got)
	}
}

func TestRefreshAheadCacheRefreshesBeforeExpiry(t *testing.T) {
	// Refreshes are scheduled refreshBuffer before a short-lived
	// certificate expires, here in about a second.
	soon := time.Now().Add(refreshBuffer + 2*time.Second)
	f, c := newFakeAdminAPI(t,
		refreshStep{inst: fakeInstance("10.0.0.1", soon)},
		refreshStep{inst: fakeInstance("10.0.0.2", inADay())},
	)
	cache := newTestRefreshAheadCache(t, c)
	wantIP(t, cache, "10.0.0.1")

	f.waitForRefresh(t, 2)
	eventuallyIP(t, cache, "10.0.0.2")
}

func TestRefreshAheadCacheServesValidInfoDuringRefresh(t *testing.T) {
	hold := newGate(t)
	f, c := newFakeAdminAPI(t,
		refreshStep{inst: fakeInstance("10.0.0.1", inADay())},
		refreshStep{inst: fakeInstance("10.0.0.2", inADay()), hold: hold},
	)
	cache := newTestRefreshAheadCache(t, c)
	wantIP(t, cache, "10.0.0.1")

	f.forceRefresh(t, cache, 2)
	// The forced refresh is held, but the current info is still valid.
	wantIP(t, cache, "10.0.0.1")

	hold.release()
	eventuallyIP(t, cache, "10.0.0.2")
}

func TestRefreshAheadCacheKeepsValidInfoAfterFailedRefresh(t *testing.T) {
	f, c := newFakeAdminAPI(t,
		refreshStep{inst: fakeInstance("10.0.0.1", inADay())},
		refreshStep{fail: true},
	)
	cache := newTestRefreshAheadCache(t, c)
	wantIP(t, cache, "10.0.0.1")

	f.forceRefresh(t, cache, 2)
	settle()
	wantIP(t, cache, "10.0.0.1")
}

func TestRefreshAheadCacheReturnsErrorWithoutValidInfo(t *testing.T) {
	hold := newGate(t)
	f, c := newFakeAdminAPI(t,
		refreshStep{fail: true},
		refreshStep{inst: fakeInstance("10.0.0.1", inADay()), hold: hold},
	)
	cache := newTestRefreshAheadCache(t, c)
	// The failed refresh is retried right away. Wait for the retry so the
	// failure is known to be recorded.
	f.waitForRefresh(t, 2)

	// The caller gets the failure without waiting for the retry.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := cache.ConnectionInfo(ctx)
	var refreshErr *errtype.RefreshError
	if !errors.As(err, &refreshErr) {
		t.Fatalf("want = %T, got = %v", refreshErr, err)
	}
}

func TestRefreshAheadCacheRetriesFailedRefresh(t *testing.T) {
	f, c := newFakeAdminAPI(t,
		refreshStep{fail: true},
		refreshStep{inst: fakeInstance("10.0.0.1", inADay())},
	)
	cache := newTestRefreshAheadCache(t, c)

	f.waitForRefresh(t, 2)
	eventuallyIP(t, cache, "10.0.0.1")
}

func TestRefreshAheadCacheForceRefreshWaitsWhenInfoFailed(t *testing.T) {
	hold := newGate(t)
	f, c := newFakeAdminAPI(t,
		refreshStep{fail: true},
		refreshStep{inst: fakeInstance("10.0.0.1", inADay()), hold: hold},
	)
	cache := newTestRefreshAheadCache(t, c)
	f.waitForRefresh(t, 2)
	if _, err := cache.ConnectionInfo(context.Background()); err == nil {
		t.Fatal("want error from failed refresh, got nil")
	}

	// The retry is already running, so ForceRefresh joins it, and callers
	// wait for its result instead of getting the earlier failure.
	cache.ForceRefresh()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := cache.ConnectionInfo(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("during forced refresh: want = %v, got = %v", context.DeadlineExceeded, err)
	}

	hold.release()
	wantIP(t, cache, "10.0.0.1")
	settle()
	if got := f.refreshes(); got != 2 {
		t.Fatalf("refreshes: want = 2, got = %v", got)
	}
}

func TestRefreshAheadCacheForceRefreshWaitsWhenInfoExpired(t *testing.T) {
	hold := newGate(t)
	f, c := newFakeAdminAPI(t,
		refreshStep{inst: fakeInstance("10.0.0.1", time.Now().Add(-time.Minute))},
		refreshStep{inst: fakeInstance("10.0.0.2", inADay()), hold: hold},
	)
	cache := newTestRefreshAheadCache(t, c)
	// An expired certificate is refreshed right away.
	f.waitForRefresh(t, 2)

	// ConnectionInfo returns expired info. Callers are expected to check
	// the expiration and force a refresh, as Dial does.
	wantIP(t, cache, "10.0.0.1")

	cache.ForceRefresh()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := cache.ConnectionInfo(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("during forced refresh: want = %v, got = %v", context.DeadlineExceeded, err)
	}

	hold.release()
	wantIP(t, cache, "10.0.0.2")
}

func TestRefreshAheadCacheRateLimitsRefreshes(t *testing.T) {
	f, c := newFakeAdminAPI(t,
		refreshStep{inst: fakeInstance("10.0.0.1", inADay())},
		refreshStep{inst: fakeInstance("10.0.0.2", inADay())},
		refreshStep{inst: fakeInstance("10.0.0.3", inADay())},
	)
	cache := newTestRefreshAheadCache(t, c)
	wantIP(t, cache, "10.0.0.1")

	// The initial refresh and one forced refresh use the burst.
	f.forceRefresh(t, cache, 2)
	eventuallyIP(t, cache, "10.0.0.2")

	// The next refresh waits about refreshInterval for the limiter. Let the
	// cache schedule its next refresh first so ForceRefresh isn't dropped
	// (see forceRefresh), which would make this check pass vacuously.
	settle()
	cache.ForceRefresh()
	settle()
	if got := f.refreshes(); got != 2 {
		t.Fatalf("refreshes: want = 2, got = %v", got)
	}
	wantIP(t, cache, "10.0.0.2")
}

func TestRefreshAheadCacheCloseStopsRefreshes(t *testing.T) {
	soon := time.Now().Add(refreshBuffer + 2*time.Second)
	f, c := newFakeAdminAPI(t,
		refreshStep{inst: fakeInstance("10.0.0.1", soon)},
		refreshStep{inst: fakeInstance("10.0.0.2", inADay())},
	)
	cache := newTestRefreshAheadCache(t, c)
	wantIP(t, cache, "10.0.0.1")

	if err := cache.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// The second refresh was due in about a second.
	time.Sleep(3 * time.Second)
	if got := f.refreshes(); got != 1 {
		t.Fatalf("refreshes: want = 1, got = %v", got)
	}
	cache.ForceRefresh()
	settle()
	if got := f.refreshes(); got != 1 {
		t.Fatalf("refreshes after ForceRefresh: want = 1, got = %v", got)
	}
	if err := cache.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestRefreshAheadCacheCloseUnblocksWaitingCallers(t *testing.T) {
	hold := newGate(t)
	f, c := newFakeAdminAPI(t,
		refreshStep{inst: fakeInstance("10.0.0.1", inADay()), hold: hold},
	)
	cache := newTestRefreshAheadCache(t, c)

	errCh := make(chan error, 1)
	go func() {
		_, err := cache.ConnectionInfo(context.Background())
		errCh <- err
	}()
	f.waitForRefresh(t, 1)
	cache.Close()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("want error after Close, got nil")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ConnectionInfo still waiting after Close")
	}
}
