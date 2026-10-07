// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package alloydbconn_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"cloud.google.com/go/alloydbconn"
	"cloud.google.com/go/auth"
	"cloud.google.com/go/auth/credentials"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// removeAuthEnvVar retrieves an OAuth2 token and a path to a service account key
// and then unsets GOOGLE_APPLICATION_CREDENTIALS. It returns a cleanup function
// that restores the original setup.
func removeAuthEnvVar(t *testing.T) (*auth.Credentials, *oauth2.Token, string, func()) {
	c, err := credentials.DetectDefault(&credentials.DetectOptions{
		Scopes: []string{"https://www.googleapis.com/auth/cloud-platform"},
	})
	if err != nil {
		t.Errorf("failed to detect credentials: %v", err)
	}
	ts, err := google.DefaultTokenSource(context.Background(),
		"https://www.googleapis.com/auth/cloud-platform",
	)
	if err != nil {
		t.Errorf("failed to resolve token source: %v", err)
	}

	tok, err := ts.Token()
	if err != nil {
		t.Errorf("failed to get token: %v", err)
	}
	path, ok := os.LookupEnv("GOOGLE_APPLICATION_CREDENTIALS")
	if !ok {
		t.Fatalf("GOOGLE_APPLICATION_CREDENTIALS was not set in the environment")
	}
	if err := os.Unsetenv("GOOGLE_APPLICATION_CREDENTIALS"); err != nil {
		t.Fatalf("failed to unset GOOGLE_APPLICATION_CREDENTIALS")
	}
	return c, tok, path, func() {
		os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", path)
	}
}

func keyfile(t *testing.T) string {
	path := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	if path == "" {
		t.Fatal("GOOGLE_APPLICATION_CREDENTIALS not set")
	}
	creds, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("io.ReadAll(): %v", err)
	}
	return string(creds)
}

func TestAuthenticationOptions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping credential integration tests")
	}
	requireAlloyDBVars(t)
	creds := keyfile(t)

	c, tok, path, cleanup := removeAuthEnvVar(t)
	defer cleanup()

	tcs := []struct {
		desc string
		opt  alloydbconn.Option
	}{
		// See e2e_test.go for examples of Application Default Credential tests.
		{
			desc: "with credentials",
			opt:  alloydbconn.WithCredentials(c),
		},
		{
			desc: "with token",
			opt:  alloydbconn.WithTokenSource(oauth2.StaticTokenSource(tok)),
		},
		{
			desc: "with credentials file",
			opt:  alloydbconn.WithCredentialsFile(path),
		},
		{
			desc: "with credentials JSON",
			opt:  alloydbconn.WithCredentialsJSON([]byte(creds)),
		},
	}
	for _, tc := range tcs {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := context.Background()

			opts := []alloydbconn.Option{
				alloydbconn.WithIAMAuthN(),
				alloydbconn.WithOptOutOfBuiltInTelemetry(),
			}
			if tc.opt != nil {
				opts = append(opts, tc.opt)
			}
			d, err := alloydbconn.NewDialer(ctx, opts...)
			if err != nil {
				t.Fatalf("failed to init Dialer: %v", err)
			}
			defer d.Close()

			dsn := fmt.Sprintf("user=%s dbname=%s sslmode=disable", alloydbIAMUser, alloydbDB)
			config, err := pgxpool.ParseConfig(dsn)
			if err != nil {
				t.Fatalf("failed to parse pgx config: %v", err)
			}

			config.ConnConfig.DialFunc = func(ctx context.Context, _ string, _ string) (net.Conn, error) {
				return d.Dial(ctx, alloydbInstanceName)
			}

			pool, err := pgxpool.NewWithConfig(ctx, config)
			if err != nil {
				t.Fatalf("failed to create pool: %s", err)
			}
			defer pool.Close()

			var now time.Time
			err = pool.QueryRow(context.Background(), "SELECT NOW()").Scan(&now)
			if err != nil {
				t.Fatalf("QueryRow failed: %s", err)
			}
			t.Log(now)
		})
	}
}
