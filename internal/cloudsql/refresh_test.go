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

package cloudsql

import (
	"context"
	"errors"
	"testing"
	"time"

	errtype "cloud.google.com/go/cloudsqlconn/errtype"
	"cloud.google.com/go/cloudsqlconn/internal/alloydb"
	"cloud.google.com/go/cloudsqlconn/internal/mock"
	"google.golang.org/api/option"
)

func TestRefresh(t *testing.T) {
	wantIP := "10.0.0.1"
	wantExpiry := time.Now().Add(time.Hour).UTC().Round(time.Second)
	wantConnName := "my-project:my-region:my-cluster:my-instance"
	cn, err := parseConnName(wantConnName)
	if err != nil {
		t.Fatalf("parseConnName(%s)failed : %v", cn, err)
	}
	inst := mock.NewFakeInstance(
		"my-project", "my-region", "my-cluster", "my-instance",
		mock.WithIPAddr(wantIP),
		mock.WithCertExpiry(wantExpiry),
	)
	mc, url, cleanup := mock.HTTPClient(
		mock.InstanceGetSuccess(inst, 1),
		mock.CreateEphemeralSuccess(inst, 1),
	)
	defer func() {
		if err := cleanup(); err != nil {
			t.Fatalf("%v", err)
		}
	}()

	cl, err := alloydb.NewClient(
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
}

func TestRefreshFailsFast(t *testing.T) {
	wantConnName := "my-project:my-region:my-cluster:my-instance"
	cn, err := parseConnName(wantConnName)
	if err != nil {
		t.Fatalf("parseConnName(%s)failed : %v", cn, err)
	}
	inst := mock.NewFakeInstance(
		"my-project", "my-region", "my-cluster", "my-instance",
	)
	mc, url, cleanup := mock.HTTPClient(
		mock.InstanceGetSuccess(inst, 1),
		mock.CreateEphemeralSuccess(inst, 1),
	)
	defer func() {
		if err := cleanup(); err != nil {
			t.Fatalf("%v", err)
		}
	}()

	cl, err := alloydb.NewClient(
		context.Background(),
		option.WithHTTPClient(mc),
		option.WithEndpoint(url),
	)
	if err != nil {
		t.Fatalf("admin API client error: %v", err)
	}
	r := newRefresher(cl, time.Hour, 30*time.Second, 1, "some-id")

	_, err = r.performRefresh(context.Background(), cn, RSAKey)
	if err != nil {
		t.Fatalf("expected no error, got = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// context is canceled
	_, err = r.performRefresh(ctx, cn, RSAKey)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled error, got = %v", err)
	}

	// force the rate limiter to throttle with a timed out context
	ctx, _ = context.WithTimeout(context.Background(), time.Millisecond)
	_, err = r.performRefresh(ctx, cn, RSAKey)

	var wantErr *errtype.DialError
	if !errors.As(err, &wantErr) {
		t.Fatalf("when refresh is throttled, want = %T, got = %v", wantErr, err)
	}
}
