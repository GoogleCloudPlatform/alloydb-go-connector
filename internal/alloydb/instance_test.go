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
		{
			desc: "with subdomain-scoped project and hyphens/digits",
			in:   "projects/sub.example-domain.com:my-proj-123/locations/us-central1/clusters/my-cluster-1/instances/my-inst-2",
			want: InstanceURI{
				project: "sub.example-domain.com:my-proj-123",
				region:  "us-central1",
				cluster: "my-cluster-1",
				name:    "my-inst-2",
			},
		},
		{
			desc: "with numeric project ID",
			in:   "projects/123456789/locations/us-central1/clusters/clust/instances/name",
			want: InstanceURI{
				project: "123456789",
				region:  "us-central1",
				cluster: "clust",
				name:    "name",
			},
		},
		{
			desc: "with uppercase and underscores",
			in:   "projects/PROJECT/locations/REGION/clusters/CLUSTER_NAME/instances/INSTANCE_NAME",
			want: InstanceURI{
				project: "PROJECT",
				region:  "REGION",
				cluster: "CLUSTER_NAME",
				name:    "INSTANCE_NAME",
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
				t.Fatalf("want = %v, got = %v", tc.want, got)
			}
			if gotURI := got.URI(); gotURI != tc.in {
				t.Fatalf("URI() want = %q, got = %q", tc.in, gotURI)
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
		{
			desc: "extra leading segment",
			in:   "extra/projects/proj/locations/reg/clusters/clust/instances/name",
		},
		{
			desc: "trailing slash",
			in:   "projects/proj/locations/reg/clusters/clust/instances/name/",
		},
		{
			desc: "extra trailing segment",
			in:   "projects/proj/locations/reg/clusters/clust/instances/name/extra",
		},
		{
			desc: "trailing relative path segments",
			in:   "projects/proj/locations/reg/clusters/clust/instances/name/../../other",
		},
		{
			desc: "dot-dot in project",
			in:   "projects/../locations/reg/clusters/clust/instances/name",
		},
		{
			desc: "dot-dot in domain-scoped project",
			in:   "projects/google..com:proj/locations/reg/clusters/clust/instances/name",
		},
		{
			desc: "dot-dot in location",
			in:   "projects/proj/locations/../clusters/clust/instances/name",
		},
		{
			desc: "dot-dot in cluster",
			in:   "projects/proj/locations/reg/clusters/../instances/name",
		},
		{
			desc: "dot-dot in instance",
			in:   "projects/proj/locations/reg/clusters/clust/instances/..",
		},
		{
			desc: "slash in project",
			in:   "projects/org/proj/locations/reg/clusters/clust/instances/name",
		},
		{
			desc: "slash in domain-scoped project",
			in:   "projects/google.com:org/proj/locations/reg/clusters/clust/instances/name",
		},
		{
			desc: "query string on instance",
			in:   "projects/proj/locations/reg/clusters/clust/instances/name?alt=json&foo=bar",
		},
		{
			desc: "query string on project",
			in:   "projects/proj?foo=bar/locations/reg/clusters/clust/instances/name",
		},
		{
			desc: "fragment on instance",
			in:   "projects/proj/locations/reg/clusters/clust/instances/name#frag",
		},
		{
			desc: "percent-encoded slash",
			in:   "projects/proj/locations/reg/clusters/clust/instances/name%2fextra",
		},
		{
			desc: "colon in instance",
			in:   "projects/proj/locations/reg/clusters/clust/instances/name:custom",
		},
		{
			desc: "empty project",
			in:   "projects//locations/reg/clusters/clust/instances/name",
		},
		{
			desc: "empty location",
			in:   "projects/proj/locations//clusters/clust/instances/name",
		},
		{
			desc: "empty cluster",
			in:   "projects/proj/locations/reg/clusters//instances/name",
		},
		{
			desc: "leading colon in project",
			in:   "projects/:proj/locations/reg/clusters/clust/instances/name",
		},
		{
			desc: "trailing colon in project",
			in:   "projects/proj:/locations/reg/clusters/clust/instances/name",
		},
		{
			desc: "multiple colons in project",
			in:   "projects/a:b:c/locations/reg/clusters/clust/instances/name",
		},
		{
			desc: "leading dot in domain",
			in:   "projects/.google.com:proj/locations/reg/clusters/clust/instances/name",
		},
		{
			desc: "trailing dot in domain",
			in:   "projects/google.com.:proj/locations/reg/clusters/clust/instances/name",
		},
		{
			desc: "dot in instance",
			in:   "projects/proj/locations/reg/clusters/clust/instances/name.extra",
		},
		{
			desc: "whitespace in URI",
			in:   "projects/proj/locations/reg/clusters/clust/instances/name extra",
		},
		{
			desc: "trailing newline",
			in:   "projects/proj/locations/reg/clusters/clust/instances/name\n",
		},
		{
			desc: "wrong resource segment",
			in:   "projects/proj/regions/reg/clusters/clust/instances/name",
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
