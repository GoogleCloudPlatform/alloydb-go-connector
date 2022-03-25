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

package cloudsql

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"testing"
	"time"

	"cloud.google.com/go/cloudsqlconn/errtype"
	"cloud.google.com/go/cloudsqlconn/internal/mock"
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

func TestInstanceEngineVersion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tests := []string{
		"MYSQL_5_7", "POSTGRES_14", "SQLSERVER_2019_STANDARD", "MYSQL_8_0_18",
	}
	for _, wantEV := range tests {
		inst := mock.NewFakeCSQLInstance("my-project", "my-region", "my-instance", mock.WithEngineVersion(wantEV))
		client, cleanup, err := mock.NewSQLAdminService(
			ctx,
			mock.DELETEInstanceGetSuccess(inst, 1),
			mock.DELETECreateEphemeralSuccess(inst, 1),
		)
		if err != nil {
			t.Fatalf("%s", err)
		}
		defer func() {
			if err := cleanup(); err != nil {
				t.Fatalf("%v", err)
			}
		}()
		i, err := NewInstance("my-project:my-region:my-instance", client, RSAKey, 30*time.Second, nil, "")
		if err != nil {
			t.Fatalf("failed to init instance: %v", err)
		}

		gotEV, err := i.InstanceEngineVersion(ctx)
		if err != nil {
			t.Fatalf("failed to retrieve engine version: %v", err)
		}
		if wantEV != gotEV {
			t.Errorf("InstanceEngineVersion(%s) failed: want %v, got %v", wantEV, gotEV, err)
		}

	}
}

func TestConnectInfo(t *testing.T) {
	ctx := context.Background()
	wantAddr := "0.0.0.0"
	inst := mock.NewFakeCSQLInstance("my-project", "my-region", "my-instance", mock.WithPublicIP(wantAddr))
	client, cleanup, err := mock.NewSQLAdminService(
		ctx,
		mock.DELETEInstanceGetSuccess(inst, 1),
		mock.DELETECreateEphemeralSuccess(inst, 1),
	)
	if err != nil {
		t.Fatalf("%s", err)
	}
	defer func() {
		if err := cleanup(); err != nil {
			t.Fatalf("%v", err)
		}
	}()

	i, err := NewInstance("my-project:my-region:my-instance", client, RSAKey, 30*time.Second, nil, "")
	if err != nil {
		t.Fatalf("failed to create mock instance: %v", err)
	}

	gotAddr, gotTLSCfg, err := i.ConnectInfo(ctx, "PUBLIC")
	if err != nil {
		t.Fatalf("failed to retrieve connect info: %v", err)
	}

	if gotAddr != wantAddr {
		t.Fatalf(
			"ConnectInfo returned unexpected IP address, want = %v, got = %v",
			wantAddr, gotAddr,
		)
	}

	wantServerName := "my-project:my-region:my-instance"
	if gotTLSCfg.ServerName != wantServerName {
		t.Fatalf(
			"ConnectInfo return unexpected server name in TLS Config, want = %v, got = %v",
			wantServerName, gotTLSCfg.ServerName,
		)
	}
}

func TestConnectInfoErrors(t *testing.T) {
	ctx := context.Background()

	client, cleanup, err := mock.NewSQLAdminService(ctx)
	if err != nil {
		t.Fatalf("%s", err)
	}
	defer cleanup()

	// Use a timeout that should fail instantly
	im, err := NewInstance("my-project:my-region:my-instance", client, RSAKey, 0, nil, "")
	if err != nil {
		t.Fatalf("failed to initialize Instance: %v", err)
	}

	_, _, err = im.ConnectInfo(ctx, "PUBLIC")
	var wantErr *errtype.DialError
	if !errors.As(err, &wantErr) {
		t.Fatalf("when connect info fails, want = %T, got = %v", wantErr, err)
	}

	// when client asks for wrong IP address type
	gotAddr, _, err := im.ConnectInfo(ctx, "PUBLIC")
	if err == nil {
		t.Fatalf("expected ConnectInfo to fail but returned IP address = %v", gotAddr)
	}
}

func TestClose(t *testing.T) {
	ctx := context.Background()

	client, cleanup, err := mock.NewSQLAdminService(ctx)
	if err != nil {
		t.Fatalf("%s", err)
	}
	defer cleanup()

	// Set up an instance and then close it immediately
	im, err := NewInstance("my-proj:my-region:my-inst", client, RSAKey, 30, nil, "")
	if err != nil {
		t.Fatalf("failed to initialize Instance: %v", err)
	}
	im.Close()

	_, _, err = im.ConnectInfo(ctx, "PUBLIC")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("failed to retrieve connect info: %v", err)
	}
}
