// Copyright 2020 Google LLC

// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at

//     https://www.apache.org/licenses/LICENSE-2.0

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
	"cloud.google.com/go/alloydbconn/internal/alloydbapi"
	"cloud.google.com/go/alloydbconn/internal/mock"
	"golang.org/x/oauth2"
	"google.golang.org/api/option"
)

// genRSAKey generates an RSA key used for test.
func genRSAKey() *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err) // unexpected, so just panic if it happens
	}
	return key
}

// RSAKey is used for test only.
var RSAKey = genRSAKey()

func TestParseInstURI(t *testing.T) {
	tcs := []struct {
		desc string
		in   string
		want instanceURI
	}{
		{
			desc: "vanilla instance URI",
			in:   "/projects/proj/locations/reg/clusters/clust/instances/name",
			want: instanceURI{
				project: "proj",
				region:  "reg",
				cluster: "clust",
				name:    "name",
			},
		},
		{
			desc: "with legacy domain-scoped project",
			in:   "/projects/google.com:proj/locations/reg/clusters/clust/instances/name",
			want: instanceURI{
				project: "google.com:proj",
				region:  "reg",
				cluster: "clust",
				name:    "name",
			},
		},
	}

	for _, tc := range tcs {
		t.Run(tc.desc, func(t *testing.T) {
			got, err := parseInstURI(tc.in)
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
			_, err := parseInstURI(tc.in)
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

func TestConnectInfo(t *testing.T) {
	ctx := context.Background()

	wantAddr := "0.0.0.0"
	inst := mock.NewFakeInstance(
		"my-project", "my-region", "my-cluster", "my-instance",
		mock.WithIPAddr(wantAddr),
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
	c, err := alloydbapi.NewClient(ctx, option.WithHTTPClient(mc),
		option.WithEndpoint(url),
		option.WithTokenSource(stubTokenSource{}),
	)
	if err != nil {
		t.Fatalf("expected NewClient to succeed, but got error: %v", err)
	}

	i, err := NewInstance(
		"/projects/my-project/locations/my-region/clusters/my-cluster/instances/my-instance",
		c, RSAKey, 30*time.Second, "dialer-id",
	)
	if err != nil {
		t.Fatalf("failed to create mock instance: %v", err)
	}

	gotAddr, gotTLSCfg, err := i.ConnectInfo(ctx)
	if err != nil {
		t.Fatalf("failed to retrieve connect info: %v", err)
	}

	if gotAddr != wantAddr {
		t.Fatalf(
			"ConnectInfo returned unexpected IP address, want = %v, got = %v",
			wantAddr, gotAddr,
		)
	}

	_ = gotTLSCfg
	// TODO: this should be the instance UID
	// wantServerName := "TODO instance UID"
	// if gotTLSCfg.ServerName != wantServerName {
	// 	t.Fatalf(
	// 		"ConnectInfo return unexpected server name in TLS Config, want = %v, got = %v",
	// 		wantServerName, gotTLSCfg.ServerName,
	// 	)
	// }
}

func TestConnectInfoErrors(t *testing.T) {
	ctx := context.Background()
	c, err := alloydbapi.NewClient(ctx, option.WithTokenSource(stubTokenSource{}))
	if err != nil {
		t.Fatalf("expected NewClient to succeed, but got error: %v", err)
	}

	// Use a timeout that should fail instantly
	im, err := NewInstance(
		"/projects/my-project/locations/my-region/clusters/my-cluster/instances/my-instance",
		c, RSAKey, 0, "dialer-id",
	)
	if err != nil {
		t.Fatalf("failed to initialize Instance: %v", err)
	}

	_, _, err = im.ConnectInfo(ctx)
	var wantErr *errtype.DialError
	if !errors.As(err, &wantErr) {
		t.Fatalf("when connect info fails, want = %T, got = %v", wantErr, err)
	}
}

func TestClose(t *testing.T) {
	ctx := context.Background()
	c, err := alloydbapi.NewClient(ctx, option.WithTokenSource(stubTokenSource{}))
	if err != nil {
		t.Fatalf("expected NewClient to succeed, but got error: %v", err)
	}

	// Set up an instance and then close it immediately
	im, err := NewInstance(
		"/projects/my-project/locations/my-region/clusters/my-cluster/instances/my-instance",
		c, RSAKey, 30, "dialer-ider",
	)
	if err != nil {
		t.Fatalf("failed to initialize Instance: %v", err)
	}
	im.Close()

	_, _, err = im.ConnectInfo(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("failed to retrieve connect info: %v", err)
	}
}
