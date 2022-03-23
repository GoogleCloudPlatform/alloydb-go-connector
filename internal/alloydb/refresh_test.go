// Copyright 2022 Google LLC
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
	"testing"
	"time"

	"cloud.google.com/go/cloudsqlconn/internal/adminapi"
	"cloud.google.com/go/cloudsqlconn/internal/mockapi"
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

func TestParseConnName(t *testing.T) {
	tcs := []struct {
		desc string
		in   string
		want ConnName
	}{
		{
			desc: "vanilla instance connection name",
			in:   "proj:reg:clust:name",
			want: ConnName{
				Project: "proj",
				Region:  "reg",
				Cluster: "clust",
				Name:    "name",
			},
		},
		{
			desc: "with legacy domain-scoped project",
			in:   "google.com:proj:reg:clust:name",
			want: ConnName{
				Project: "google.com:proj",
				Region:  "reg",
				Cluster: "clust",
				Name:    "name",
			},
		},
	}

	for _, tc := range tcs {
		t.Run(tc.desc, func(t *testing.T) {
			got, err := parseConnName(tc.in)
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
			_, err := parseConnName(tc.in)
			if err == nil {
				t.Fatal("want error, got nil")
			}
		})
	}
}

func TestRefresh(t *testing.T) {
	wantIP := "10.0.0.1"
	wantExpiry := time.Now().Add(time.Hour).UTC().Round(time.Second)
	wantConnName := "my-project:my-region:my-cluster:my-instance"
	cn, err := parseConnName(wantConnName)
	if err != nil {
		t.Fatalf("parseConnName(%s)failed : %v", cn, err)
	}
	inst := mockapi.NewFakeInstance(
		"my-project", "my-region", "my-cluster", "my-instance",
		mockapi.WithIPAddr(wantIP),
		mockapi.WithCertExpiry(wantExpiry),
	)
	mc, url, cleanup := mockapi.HTTPClient(
		mockapi.InstanceGetSuccess(inst, 1),
		mockapi.CreateEphemeralSuccess(inst, 1),
	)
	_ = mc
	defer func() {
		if err := cleanup(); err != nil {
			t.Fatalf("%v", err)
		}
	}()

	cl, err := adminapi.NewClient(
		context.Background(),
		option.WithHTTPClient(mc),
		option.WithEndpoint(url),
	)
	if err != nil {
		t.Fatalf("admin API client error: %v", err)
	}
	r := newRefresher(cl, time.Hour, 30*time.Second, 2, "some-id")
	res, err := r.performRefresh(context.Background(), cn, RSAKey)
	if err != nil {
		t.Fatalf("performRefresh unexpectedly failed with error: %v", err)
	}

	if got := res.instanceIPAddr; wantIP != got {
		t.Fatalf("metadata IP mismatch, want = %v, got = %v", wantIP, got)
	}
	if got := res.expiry; wantExpiry != got {
		t.Fatalf("expiry mismatch, want = %v, got = %v", wantExpiry, got)
	}
	if got := res.conf.ServerName; "client.alloydb" != got {
		t.Fatalf("server name mismatch, want = %v, got = %v", "client.alloydb", got)
	}
}
