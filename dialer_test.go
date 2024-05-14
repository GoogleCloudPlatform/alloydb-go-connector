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
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	alloydbadmin "cloud.google.com/go/alloydb/apiv1alpha"
	"cloud.google.com/go/alloydbconn/errtype"
	"cloud.google.com/go/alloydbconn/internal/alloydb"
	"cloud.google.com/go/alloydbconn/internal/mock"
	"golang.org/x/oauth2"
	"google.golang.org/api/option"
)

const testInstanceURI = "projects/my-project/locations/my-region/" +
	"clusters/my-cluster/instances/my-instance"

type stubTokenSource struct{}

func (stubTokenSource) Token() (*oauth2.Token, error) {
	return &oauth2.Token{}, nil
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
	c, err := alloydbadmin.NewAlloyDBAdminRESTClient(
		ctx, option.WithHTTPClient(mc), option.WithEndpoint(url))
	if err != nil {
		t.Fatalf("expected NewClient to succeed, but got error: %v", err)
	}

	d, err := NewDialer(ctx, WithTokenSource(stubTokenSource{}))
	if err != nil {
		t.Fatalf("expected NewDialer to succeed, but got error: %v", err)
	}
	d.client = c

	// Run several tests to ensure the underlying shared buffer is properly
	// reset between connections.
	for i := 0; i < 10; i++ {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			conn, err := d.Dial(ctx, testInstanceURI)
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
		})
	}
}

func writeStaticInfo(t *testing.T, i mock.FakeAlloyDBInstance) io.Reader {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	pub := x509.MarshalPKCS1PublicKey(&key.PublicKey)
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: pub})
	if pubPEM == nil {
		t.Fatal("public key encoding failed")
	}
	priv := x509.MarshalPKCS1PrivateKey(key)
	privPEM := pem.EncodeToMemory(
		&pem.Block{Type: "OPENSSH PRIVATE KEY", Bytes: priv},
	)
	if privPEM == nil {
		t.Fatal("private key encoding failed")
	}

	static := map[string]interface{}{}
	static["publicKey"] = string(pubPEM)
	static["privateKey"] = string(privPEM)
	info := make(map[string]interface{})
	info["ipAddress"] = "127.0.0.1" // "private" IP is localhost in testing
	chain, err := i.GeneratePEMCertificateChain(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	info["pemCertificateChain"] = chain
	info["caCert"] = chain[len(chain)-1] // CA cert is last in chain
	static[i.String()] = info

	data, err := json.Marshal(static)
	if err != nil {
		t.Fatal(err)
	}

	return bytes.NewReader(data)
}

func TestDialerWorksWithStaticConnectionInfo(t *testing.T) {
	ctx := context.Background()
	inst := mock.NewFakeInstance(
		"my-project", "my-region", "my-cluster", "my-instance",
	)
	stop := mock.StartServerProxy(t, inst)
	t.Cleanup(stop)

	staticPath := writeStaticInfo(t, inst)

	d, err := NewDialer(
		ctx,
		WithTokenSource(stubTokenSource{}),
		WithStaticConnectionInfo(staticPath),
	)
	if err != nil {
		t.Fatalf("expected NewDialer to succeed, but got error: %v", err)
	}

	conn, err := d.Dial(ctx, testInstanceURI)
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

	_, err = d.Dial(ctx, testInstanceURI)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("when context is canceled, want = %T, got = %v", context.Canceled, err)
	}

	_, err = d.Dial(context.Background(), testInstanceURI)
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

	_, err = d.Dial(ctx, testInstanceURI)
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
		WithDialFunc(func(_ context.Context, _, _ string) (net.Conn, error) {
			return nil, errors.New("sentinel error")
		}),
		WithTokenSource(stubTokenSource{}),
	)
	if err != nil {
		t.Fatalf("expected NewDialer to succeed, but got error: %v", err)
	}
	d.client = c

	_, err = d.Dial(ctx, testInstanceURI)
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
	d, err := NewDialer(context.Background(),
		WithTokenSource(stubTokenSource{}),
		WithRefreshTimeout(time.Second),
	)
	if err != nil {
		t.Fatalf("expected NewDialer to succeed, but got error: %v", err)
	}
	defer func(d *Dialer) {
		err := d.Close()
		if err != nil {
			t.Log(err)
		}
	}(d)

	// Populate instance map with connection info cache that will always fail.
	// This allows the test to verify the error case path invoking close.
	badInstanceName := "projects/bad/locations/bad/clusters/bad/instances/bad"
	tcs := []struct {
		desc string
		uri  string
		resp connectionInfoResp
		opts []DialOption
	}{
		{
			desc: "dialing a bad instance URI",
			uri:  badInstanceName,
			resp: connectionInfoResp{
				err: errors.New("connect info failed"),
			},
		},
		{
			desc: "specifying an invalid IP type",
			uri:  testInstanceURI,
			resp: connectionInfoResp{
				info: alloydb.ConnectionInfo{
					IPAddrs: map[string]string{
						// no public IP
						alloydb.PrivateIP: "10.0.0.1",
					},
					Expiration: time.Now().Add(time.Hour),
				},
			},
			opts: []DialOption{WithPublicIP()},
		},
	}

	for _, tc := range tcs {
		t.Run(tc.desc, func(t *testing.T) {
			// Manually populate the internal cache with a spy
			inst, _ := alloydb.ParseInstURI(tc.uri)
			spy := &spyConnectionInfoCache{
				connectInfoCalls: []connectionInfoResp{tc.resp},
			}
			d.cache[inst] = monitoredCache{
				connectionInfoCache: spy,
			}

			_, err = d.Dial(context.Background(), tc.uri, tc.opts...)
			if err == nil {
				t.Fatal("expected Dial to return error")
			}
			// Verify that the connection info cache was closed (to prevent
			// further failed refresh operations)
			if got, want := spy.CloseWasCalled(), true; got != want {
				t.Fatal("Close was not called")
			}

			// Now verify that bad connection name has been deleted from map.
			d.lock.RLock()
			_, ok := d.cache[inst]
			d.lock.RUnlock()
			if ok {
				t.Fatal("connection info was not removed from cache")
			}
		})
	}

}

func TestDialRefreshesExpiredCertificates(t *testing.T) {
	d, err := NewDialer(
		context.Background(),
		WithTokenSource(stubTokenSource{}),
	)
	if err != nil {
		t.Fatalf("expected NewDialer to succeed, but got error: %v", err)
	}

	sentinel := errors.New("connect info failed")
	inst := testInstanceURI
	cn, _ := alloydb.ParseInstURI(inst)
	spy := &spyConnectionInfoCache{
		connectInfoCalls: []connectionInfoResp{
			// First call returns expired certificate
			{
				info: alloydb.ConnectionInfo{
					Expiration: time.Now().Add(-10 * time.Hour),
				},
			},
			// Second call errors to validate error path
			{
				err: sentinel,
			},
		},
	}
	d.cache[cn] = monitoredCache{
		connectionInfoCache: spy,
	}

	_, err = d.Dial(context.Background(), inst)
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected Dial to return sentinel error, instead got = %v", err)
	}

	// Verify that the cache was refreshed
	if got, want := spy.ForceRefreshWasCalled(), true; got != want {
		t.Fatal("ForceRefresh was not called")
	}

	// Verify that the connection info cache was closed (to prevent
	// further failed refresh operations)
	if got, want := spy.CloseWasCalled(), true; got != want {
		t.Fatal("Close was not called")
	}

	// Now verify that bad connection name has been deleted from map.
	d.lock.RLock()
	_, ok := d.cache[cn]
	d.lock.RUnlock()
	if ok {
		t.Fatal("bad instance was not removed from the cache")
	}
}

type connectionInfoResp struct {
	info alloydb.ConnectionInfo
	err  error
}

type spyConnectionInfoCache struct {
	mu                    sync.Mutex
	connectInfoIndex      int
	connectInfoCalls      []connectionInfoResp
	closed                bool
	forceRefreshWasCalled bool
	// embed interface to avoid having to implement irrelevant methods
	connectionInfoCache
}

func (s *spyConnectionInfoCache) ConnectionInfo(context.Context) (alloydb.ConnectionInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res := s.connectInfoCalls[s.connectInfoIndex]
	s.connectInfoIndex++
	return res.info, res.err
}

func (s *spyConnectionInfoCache) ForceRefresh() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.forceRefreshWasCalled = true
}

func (s *spyConnectionInfoCache) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *spyConnectionInfoCache) CloseWasCalled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *spyConnectionInfoCache) ForceRefreshWasCalled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.forceRefreshWasCalled
}

func TestDialerSupportsOneOffDialFunction(t *testing.T) {
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
		WithDialFunc(func(_ context.Context, _, _ string) (net.Conn, error) {
			return nil, errors.New("sentinel error")
		}),
		WithTokenSource(stubTokenSource{}),
	)
	if err != nil {
		t.Fatalf("expected NewDialer to succeed, but got error: %v", err)
	}
	d.client = c
	defer func() {
		if err := d.Close(); err != nil {
			t.Log(err)
		}
		_ = cleanup()
	}()

	sentinelErr := errors.New("dial func was called")
	f := func(context.Context, string, string) (net.Conn, error) {
		return nil, sentinelErr
	}

	_, err = d.Dial(ctx, testInstanceURI, WithOneOffDialFunc(f))
	if !errors.Is(err, sentinelErr) {
		t.Fatal("one-off dial func was not called")
	}
}

func TestDialerCloseReportsFriendlyError(t *testing.T) {
	d, err := NewDialer(
		context.Background(),
		WithTokenSource(stubTokenSource{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	_ = d.Close()

	_, err = d.Dial(context.Background(), testInstanceURI)
	if !errors.Is(err, ErrDialerClosed) {
		t.Fatalf("want = %v, got = %v", ErrDialerClosed, err)
	}

	// Ensure multiple calls to close don't panic
	_ = d.Close()

	_, err = d.Dial(context.Background(), testInstanceURI)
	if !errors.Is(err, ErrDialerClosed) {
		t.Fatalf("want = %v, got = %v", ErrDialerClosed, err)
	}
}
