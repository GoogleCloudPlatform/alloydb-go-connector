// Copyright 2025 Google LLC
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
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"cloud.google.com/go/auth"
	"golang.org/x/oauth2"
)

// Reusable mock credential to bypass ADC lookup
var mockCreds = WithTokenSource(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "mock-token"}))

type nullTokenSource struct{}

func (nullTokenSource) Token() (*oauth2.Token, error) {
	return nil, nil
}

func TestNewDialerConfig_IncompatibleOptions(t *testing.T) {
	tcs := []struct {
		desc string
		opts []Option
	}{
		{
			desc: "WithOptOutOfAdvancedConnectionCheck and WithIAMAuthN",
			opts: []Option{WithOptOutOfAdvancedConnectionCheck(), WithIAMAuthN()},
		},
		{
			desc: "WithCredentialsFile and WithCredentialsJSON",
			opts: []Option{WithCredentialsFile("/some/file"), WithCredentialsJSON(nil)},
		},
		{
			desc: "WithCredentialsFile and WithTokenSource",
			opts: []Option{WithCredentialsFile("/some/file"), WithTokenSource(nullTokenSource{})},
		},
		{
			desc: "WithCredentialsJSON and WithTokenSource",
			opts: []Option{WithCredentialsJSON([]byte(`sample-json`)), WithTokenSource(nullTokenSource{})},
		},
		{
			desc: "WithCredentials and WihtCredentialsJSON",
			opts: []Option{WithCredentials(&auth.Credentials{}), WithCredentialsJSON([]byte(`sample-json`))},
		},
		{
			desc: "WithCredentials and WihtCredentialsFile",
			opts: []Option{WithCredentials(&auth.Credentials{}), WithCredentialsFile("/some/file")},
		},
		{
			desc: "WithCredentials and WihtTokenSource",
			opts: []Option{WithCredentials(&auth.Credentials{}), WithTokenSource(nullTokenSource{})},
		},
	}

	for _, tc := range tcs {
		t.Run(tc.desc, func(t *testing.T) {
			_, err := newDialerConfig(tc.opts...)
			if err == nil {
				t.Fatal("expected an error, but got nil")
			}
		})
	}
}

type fakeTokenProvider struct {
}

func (fakeTokenProvider) Token(context.Context) (*auth.Token, error) {
	return &auth.Token{Value: "faketoken"}, nil
}

type fakeTokenSource struct{}

func (fakeTokenSource) Token() (*oauth2.Token, error) {
	return &oauth2.Token{AccessToken: "faketoken"}, nil
}

func TestIAMAuthOptions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping credential integration test")
	}
	c := &auth.Credentials{
		TokenProvider: fakeTokenProvider{},
	}

	tcs := []struct {
		desc string
		opts []Option
	}{
		{
			desc: "WithIAMAuthNCredentials",
			opts: []Option{
				WithIAMAuthNCredentials(c),
			},
		},
		{
			desc: "WithIAMAuthNTOkenSource",
			opts: []Option{
				WithIAMAuthNTokenSource(fakeTokenSource{}),
			},
		},
	}
	for _, tc := range tcs {
		t.Run(tc.desc, func(t *testing.T) {
			cfg, err := newDialerConfig(tc.opts...)
			if err != nil {
				t.Fatal(err)
			}
			tok, err := cfg.iamAuthNTokenProvider.Token(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if tok.Value != "faketoken" {
				t.Fatal("got unexpected token, want sentinel value \"faketoken\"")
			}
		})
	}

}

// TestWithUniverseDomain verifies that WithUniverseDomain properly sets
// the universeDomain field on dialerConfig.
func TestWithUniverseDomain(t *testing.T) {
	want := "my-universe.cloud"
	cfg, err := newDialerConfig(WithUniverseDomain(want), mockCreds)
	if err != nil {
		t.Fatalf("newDialerConfig failed: %v", err)
	}
	if cfg.universeDomain != want {
		t.Errorf("got %q, want %q", cfg.universeDomain, want)
	}
	if got := cfg.clientUniverseDomain(); got != want {
		t.Errorf("clientUniverseDomain() = %q, want %q", got, want)
	}
}

// TestUniverseDomainResolutionPrecedence verifies the fallback order:
// 1. Explicit Option -> 2. Environment variable -> 3. Default "googleapis.com"
func TestUniverseDomainResolutionPrecedence(t *testing.T) {
	tcs := []struct {
		desc       string
		envUD      string
		opts       []Option
		wantDomain string
	}{
		{
			desc:       "resolves from environment variable",
			envUD:      "env-universe.cloud",
			opts:       []Option{mockCreds},
			wantDomain: "env-universe.cloud",
		},
		{
			desc:       "explicit option takes precedence over environment variable",
			envUD:      "env-universe.cloud",
			opts:       []Option{WithUniverseDomain("option-universe.cloud"), mockCreds},
			wantDomain: "option-universe.cloud",
		},
		{
			desc:       "setting WithAdminAPIEndpoint with environment variable",
			envUD:      "env-universe.cloud",
			opts:       []Option{mockCreds, WithAdminAPIEndpoint("alloydb.googleapis.com")},
			wantDomain: "env-universe.cloud",
		},
		{
			desc:       "default fallback when option not set",
			opts:       []Option{mockCreds},
			wantDomain: defaultUniverseDomain, // "googleapis.com"
		},
		{
			desc:       "setting WithAdminAPIEndpoint without universe domain",
			opts:       []Option{mockCreds, WithAdminAPIEndpoint("alloydb.googleapis.com")},
			wantDomain: defaultUniverseDomain, // "googleapis.com"
		},
		{
			desc:       "resolves from explicit WithUniverseDomain option",
			opts:       []Option{WithUniverseDomain("option-universe.cloud"), mockCreds},
			wantDomain: "option-universe.cloud",
		},
	}
	for _, tc := range tcs {
		t.Run(tc.desc, func(t *testing.T) {
			t.Setenv("GOOGLE_CLOUD_UNIVERSE_DOMAIN", tc.envUD)
			cfg, err := newDialerConfig(tc.opts...)
			if err != nil {
				t.Fatalf("newDialerConfig failed: %v", err)
			}
			if got := cfg.clientUniverseDomain(); got != tc.wantDomain {
				t.Errorf("clientUniverseDomain() = %q, want %q", got, tc.wantDomain)
			}
		})
	}
}

// serviceAccountJSON returns service account credentials JSON with a freshly
// generated key. When ud is non-empty, the JSON includes a universe_domain
// field.
func serviceAccountJSON(t *testing.T, ud string) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate private key: %v", err)
	}
	keyBytes, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("failed to marshal private key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes})
	m := map[string]string{
		"type":           "service_account",
		"project_id":     "test-project",
		"private_key_id": "some-key-id",
		"private_key":    string(keyPEM),
		"client_email":   "test@test-project.iam.gserviceaccount.com",
	}
	if ud != "" {
		m["universe_domain"] = ud
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("failed to marshal credentials JSON: %v", err)
	}
	return b
}

// TestCredentialsUniverseDomainMismatch verifies that credentials keep the
// universe_domain field of their JSON when WithUniverseDomain is not set. The
// AlloyDB Admin API client then rejects them as belonging to another universe
// before sending a request.
func TestCredentialsUniverseDomainMismatch(t *testing.T) {
	b := serviceAccountJSON(t, "my-universe.cloud")
	path := filepath.Join(t.TempDir(), "creds.json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("failed to write credentials file: %v", err)
	}
	tcs := []struct {
		desc string
		opt  Option
	}{
		{desc: "WithCredentialsJSON", opt: WithCredentialsJSON(b)},
		{desc: "WithCredentialsFile", opt: WithCredentialsFile(path)},
	}
	for _, tc := range tcs {
		t.Run(tc.desc, func(t *testing.T) {
			t.Setenv("GOOGLE_CLOUD_UNIVERSE_DOMAIN", "")
			var reqs atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, _ *http.Request) {
					reqs.Add(1)
					w.WriteHeader(http.StatusBadRequest)
				},
			))
			defer srv.Close()

			d, err := NewDialer(context.Background(), tc.opt,
				WithAdminAPIEndpoint(srv.URL),
				WithLazyRefresh(),
				WithOptOutOfBuiltInTelemetry(),
			)
			if err != nil {
				t.Fatalf("expected NewDialer to succeed, got: %v", err)
			}
			defer d.Close()

			_, err = d.Dial(context.Background(), testInstanceURI)
			if err == nil || !strings.Contains(err.Error(), "universe domain") {
				t.Fatalf("expected universe domain mismatch error, got: %v", err)
			}
			if got := reqs.Load(); got != 0 {
				t.Fatalf("AlloyDB Admin API requests = %d, want 0", got)
			}
		})
	}
}

// TestTokenSourceUniverseDomain verifies that credentials built from
// WithTokenSource use the configured universe domain, so the AlloyDB Admin API
// client sends requests instead of failing the universe domain check.
func TestTokenSourceUniverseDomain(t *testing.T) {
	for _, ud := range []string{"my-universe.cloud", ""} {
		t.Run("universe domain="+ud, func(t *testing.T) {
			t.Setenv("GOOGLE_CLOUD_UNIVERSE_DOMAIN", ud)
			var reqs atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, _ *http.Request) {
					reqs.Add(1)
					w.WriteHeader(http.StatusBadRequest)
				},
			))
			defer srv.Close()

			d, err := NewDialer(context.Background(),
				WithTokenSource(fakeTokenSource{}),
				WithAdminAPIEndpoint(srv.URL),
				WithLazyRefresh(),
				WithOptOutOfBuiltInTelemetry(),
			)
			if err != nil {
				t.Fatalf("expected NewDialer to succeed, got: %v", err)
			}
			defer d.Close()

			_, err = d.Dial(context.Background(), testInstanceURI)
			if err == nil || strings.Contains(err.Error(), "universe domain") {
				t.Fatalf("expected no universe domain mismatch, got: %v", err)
			}
			if got := reqs.Load(); got == 0 {
				t.Fatal("AlloyDB Admin API requests = 0, want at least 1")
			}
		})
	}
}

// TestUniverseDomainWithCredentialsJSON verifies credentials detection with universe domain.
func TestUniverseDomainWithCredentialsJSON(t *testing.T) {
	// Generate a valid mock RSA private key
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate mock private key: %v", err)
	}
	keyBytes, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("failed to marshal private key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: keyBytes,
	})

	// Format JSON with the valid PEM block using %q to escape newlines correctly
	wantUniverseDomain := "my-universe.com"
	credsJSON := []byte(fmt.Sprintf(`{
      "type": "service_account",
      "project_id": "test-project",
      "private_key_id": "some-key-id",
      "private_key": %q,
      "client_email": "test@test-project.iam.gserviceaccount.com",
      "universe_domain": %q
    }`, string(keyPEM), wantUniverseDomain))

	cfg, err := newDialerConfig(WithCredentialsJSON(credsJSON), WithUniverseDomain(wantUniverseDomain))
	if err != nil {
		t.Fatalf("newDialerConfig failed: %v", err)
	}
	if cfg.clientUniverseDomain() != wantUniverseDomain {
		t.Errorf("got %q, want %q", cfg.clientUniverseDomain(), wantUniverseDomain)
	}
}

// TestWithUniverseDomainCredentials verifies that credentials whose UniverseDomain
// evaluates to "googleapis.com" (such as AuthorizedUser / Workforce Identity ADC)
// are wrapped to report the configured universe domain.
func TestWithUniverseDomainCredentials(t *testing.T) {
	ctx := context.Background()
	wantDomain := "apis-tpczero.goog"
	// Create a mock credential whose UniverseDomainProvider defaults to "googleapis.com"
	// to reproduce the Workforce Identity / AuthorizedUser credential behavior.
	gduCreds := auth.NewCredentials(&auth.CredentialsOptions{
		TokenProvider: fakeTokenProvider{},
		ProjectIDProvider: auth.CredentialsPropertyFunc(func(_ context.Context) (string, error) {
			return "test-project", nil
		}),
		UniverseDomainProvider: auth.CredentialsPropertyFunc(func(_ context.Context) (string, error) {
			return "googleapis.com", nil
		}),
	})
	// Verify original mock credentials report "googleapis.com"
	origDomain, err := gduCreds.UniverseDomain(ctx)
	if err != nil {
		t.Fatalf("UniverseDomain() failed on mock creds: %v", err)
	}
	if origDomain != "googleapis.com" {
		t.Fatalf("got %q, want %q", origDomain, "googleapis.com")
	}
	// Wrap the credentials with the custom universe domain
	wrappedCreds := WithUniverseDomainCredentials(gduCreds, wantDomain)
	// Verify wrapped credentials report "apis-tpczero.goog" and preserve properties
	gotDomain, err := wrappedCreds.UniverseDomain(ctx)
	if err != nil {
		t.Fatalf("UniverseDomain() failed on wrapped creds: %v", err)
	}
	if gotDomain != wantDomain {
		t.Errorf("wrapped UniverseDomain() = %q, want %q", gotDomain, wantDomain)
	}
	gotProject, err := wrappedCreds.ProjectID(ctx)
	if err != nil {
		t.Fatalf("ProjectID() failed on wrapped creds: %v", err)
	}
	if gotProject != "test-project" {
		t.Errorf("wrapped ProjectID() = %q, want %q", gotProject, "test-project")
	}
}

// TestNewDialerConfig_WrapsCredentialsWithUniverseDomain verifies that passing
// WithCredentials with a non-matching universe domain along with WithUniverseDomain
// wraps the credential attached to dialerConfig.
func TestNewDialerConfig_WrapsCredentialsWithUniverseDomain(t *testing.T) {
	ctx := context.Background()
	wantDomain := "apis-tpczero.goog"
	gduCreds := auth.NewCredentials(&auth.CredentialsOptions{
		TokenProvider: fakeTokenProvider{},
		UniverseDomainProvider: auth.CredentialsPropertyFunc(func(_ context.Context) (string, error) {
			return "googleapis.com", nil
		}),
	})
	cfg, err := newDialerConfig(
		WithCredentials(gduCreds),
		WithUniverseDomain(wantDomain),
	)
	if err != nil {
		t.Fatalf("newDialerConfig failed: %v", err)
	}
	if cfg.clientUniverseDomain() != wantDomain {
		t.Errorf("clientUniverseDomain() = %q, want %q", cfg.clientUniverseDomain(), wantDomain)
	}
	// Verify the wrapped credential used for dialer IAM AuthN provider matches the universe domain
	wrappedCreds := WithUniverseDomainCredentials(gduCreds, wantDomain)
	gotDomain, err := wrappedCreds.UniverseDomain(ctx)
	if err != nil {
		t.Fatalf("failed to get domain: %v", err)
	}
	if gotDomain != wantDomain {
		t.Errorf("got credential universe domain %q, want %q", gotDomain, wantDomain)
	}
}
