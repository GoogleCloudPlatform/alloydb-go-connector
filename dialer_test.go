// Copyright 2021 Google LLC
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

package alloydbconn

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	alloydbadmin "cloud.google.com/go/alloydb/apiv1beta"
	"cloud.google.com/go/alloydbconn/errtype"
	"cloud.google.com/go/alloydbconn/internal/alloydb"
	"cloud.google.com/go/alloydbconn/internal/mock"
	"golang.org/x/oauth2"
	"google.golang.org/api/option"
)

type stubTokenSource struct{}

func (stubTokenSource) Token() (*oauth2.Token, error) {
	return nil, nil
}

func TestDialerCanConnectToInstance(t *testing.T) {
	ctx := context.Background()
	inst := mock.NewFakeInstance(
		"my-project", "my-region", "my-cluster", "my-instance",
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
	c, err := alloydbadmin.NewAlloyDBAdminRESTClient(ctx, option.WithHTTPClient(mc), option.WithEndpoint(url))
	if err != nil {
		t.Fatalf("expected NewClient to succeed, but got error: %v", err)
	}

	d, err := NewDialer(ctx, WithTokenSource(stubTokenSource{}))
	if err != nil {
		t.Fatalf("expected NewDialer to succeed, but got error: %v", err)
	}
	d.client = c

	conn, err := d.Dial(ctx, "/projects/my-project/locations/my-region/clusters/my-cluster/instances/my-instance")
	if err != nil {
		t.Fatalf("expected Dial to succeed, but got error: %v", err)
	}
	defer conn.Close()

	data, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("expected ReadAll to succeed, got error %v", err)
	}
	if string(data) != "my-instance" {
		t.Fatalf("expected known response from the server, but got %v", string(data))
	}
}

func TestDialWithAdminAPIErrors(t *testing.T) {
	ctx := context.Background()
	mc, url, cleanup := mock.HTTPClient()
	defer func() {
		if err := cleanup(); err != nil {
			t.Fatalf("%v", err)
		}
	}()
	c, err := alloydbadmin.NewAlloyDBAdminRESTClient(ctx, option.WithHTTPClient(mc), option.WithEndpoint(url))
	if err != nil {
		t.Fatalf("expected NewClient to succeed, but got error: %v", err)
	}
	d, err := NewDialer(ctx, WithTokenSource(stubTokenSource{}))
	if err != nil {
		t.Fatalf("expected NewDialer to succeed, but got error: %v", err)
	}
	d.client = c

	_, err = d.Dial(ctx, "bad-instance-name")
	var wantErr1 *errtype.ConfigError
	if !errors.As(err, &wantErr1) {
		t.Fatalf("when instance name is invalid, want = %T, got = %v", wantErr1, err)
	}

	ctx, cancel := context.WithCancel(ctx)
	cancel()

	_, err = d.Dial(ctx, "/projects/my-project/locations/my-region/clusters/my-cluster/instances/my-instance")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("when context is canceled, want = %T, got = %v", context.Canceled, err)
	}

	_, err = d.Dial(context.Background(), "/projects/my-project/locations/my-region/clusters/my-cluster/instances/my-instance")
	var wantErr2 *errtype.RefreshError
	if !errors.As(err, &wantErr2) {
		t.Fatalf("when API call fails, want = %T, got = %v", wantErr2, err)
	}
}

func TestDialWithUnavailableServerErrors(t *testing.T) {
	ctx := context.Background()
	inst := mock.NewFakeInstance(
		"my-project", "my-region", "my-cluster", "my-instance",
	)
	// Don't use the cleanup function. Because this test is about error
	// cases, API requests (started in two separate goroutines) will
	// sometimes succeed and clear the mock, and sometimes not.
	// This test is about error return values from Dial, not API interaction.
	mc, url, _ := mock.HTTPClient(
		mock.InstanceGetSuccess(inst, 2),
		mock.CreateEphemeralSuccess(inst, 2),
	)
	c, err := alloydbadmin.NewAlloyDBAdminRESTClient(ctx, option.WithHTTPClient(mc), option.WithEndpoint(url))
	if err != nil {
		t.Fatalf("expected NewClient to succeed, but got error: %v", err)
	}

	d, err := NewDialer(ctx, WithTokenSource(stubTokenSource{}))
	if err != nil {
		t.Fatalf("expected NewDialer to succeed, but got error: %v", err)
	}
	d.client = c

	_, err = d.Dial(ctx, "/projects/my-project/locations/my-region/clusters/my-cluster/instances/my-instance")
	var wantErr2 *errtype.DialError
	if !errors.As(err, &wantErr2) {
		t.Fatalf("when server proxy socket is unavailable, want = %T, got = %v", wantErr2, err)
	}
}

func TestDialerWithCustomDialFunc(t *testing.T) {
	ctx := context.Background()
	inst := mock.NewFakeInstance(
		"my-project", "my-region", "my-cluster", "my-instance",
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
	c, err := alloydbadmin.NewAlloyDBAdminRESTClient(ctx, option.WithHTTPClient(mc), option.WithEndpoint(url))
	if err != nil {
		t.Fatalf("expected NewClient to succeed, but got error: %v", err)
	}

	d, err := NewDialer(ctx,
		WithDialFunc(func(ctx context.Context, network, addr string) (net.Conn, error) {
			return nil, errors.New("sentinel error")
		}),
		WithTokenSource(stubTokenSource{}),
	)
	if err != nil {
		t.Fatalf("expected NewDialer to succeed, but got error: %v", err)
	}
	d.client = c

	_, err = d.Dial(ctx, "/projects/my-project/locations/my-region/clusters/my-cluster/instances/my-instance")
	if !strings.Contains(err.Error(), "sentinel error") {
		t.Fatalf("want = sentinel error, got = %v", err)
	}
}

func TestDialerUserAgent(t *testing.T) {
	data, err := os.ReadFile("version.txt")
	if err != nil {
		t.Fatalf("failed to read version.txt: %v", err)
	}
	ver := strings.TrimSpace(string(data))
	want := "alloydb-go-connector/" + ver
	if want != userAgent {
		t.Errorf("embed version mismatched: want %q, got %q", want, userAgent)
	}
}

func TestDialerRemovesInvalidInstancesFromCache(t *testing.T) {
	// When a dialer attempts to retrieve connection info for a
	// non-existent instance, it should delete the instance from
	// the cache and ensure no background refresh happens (which would be
	// wasted cycles).
	ctx := context.Background()
	mc, url, cleanup := mock.HTTPClient()
	defer func() { _ = cleanup() }()
	c, err := alloydbadmin.NewAlloyDBAdminRESTClient(ctx,
		option.WithHTTPClient(mc),
		option.WithEndpoint(url),
	)

	d, err := NewDialer(context.Background(),
		WithTokenSource(stubTokenSource{}),
		WithRefreshTimeout(time.Second),
	)
	if err != nil {
		t.Fatalf("expected NewDialer to succeed, but got error: %v", err)
	}
	d.client = c
	defer func(d *Dialer) {
		err := d.Close()
		if err != nil {
			t.Log(err)
		}
	}(d)

	badInstanceName := "/projects/bad/locations/bad/clusters/bad/instances/bad"
	_, _ = d.Dial(context.Background(), badInstanceName)

	// The internal cache is not revealed publicly, so check the internal cache
	// to confirm the instance is no longer present.
	badCN, _ := alloydb.ParseInstURI(badInstanceName)
	d.lock.RLock()
	_, ok := d.instances[badCN]
	d.lock.RUnlock()
	if ok {
		t.Fatal("bad instance was not removed from the cache")
	}
}
