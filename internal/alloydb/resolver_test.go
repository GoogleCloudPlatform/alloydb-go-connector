// Copyright 2026 Google LLC
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
	"errors"
	"testing"

	"cloud.google.com/go/alloydbconn/errtype"
)

const (
	testURI      = "projects/my-proj/locations/us-central1/clusters/my-cluster/instances/my-inst"
	testEndpoint = "my-endpoint.global.alloydb-psa.goog"
)

type fakeNetResolver struct {
	records []string
	err     error
	calls   int
}

func (f *fakeNetResolver) LookupTXT(_ context.Context, _ string) ([]string, error) {
	f.calls++
	return f.records, f.err
}

func mustParse(t *testing.T, s string) InstanceURI {
	t.Helper()
	u, err := ParseInstURI(s)
	if err != nil {
		t.Fatalf("ParseInstURI(%q): %v", s, err)
	}
	return u
}

func TestIsValidDomain(t *testing.T) {
	tcs := []struct {
		in   string
		want bool
	}{
		{in: testEndpoint, want: true},
		{in: testEndpoint + ".", want: true},
		{in: "My-Endpoint.Alloydb-psa.goog", want: true},
		{in: "localhost", want: false},
		{in: "", want: false},
		{in: testURI, want: false},
		{in: "bad_host!.goog", want: false},
	}
	for _, tc := range tcs {
		t.Run(tc.in, func(t *testing.T) {
			if got := IsValidDomain(tc.in); got != tc.want {
				t.Errorf("IsValidDomain(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestDialTarget(t *testing.T) {
	u := mustParse(t, testURI)

	plain := DialTarget{URI: u}
	if plain.HasDomainName() {
		t.Error("HasDomainName() = true for a URI-only instance")
	}
	if got, want := plain.String(), "my-proj.us-central1.my-cluster.my-inst"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}

	viaDNS := DialTarget{URI: u, DomainName: testEndpoint}
	if !viaDNS.HasDomainName() {
		t.Error("HasDomainName() = false for a DNS-resolved instance")
	}
	if got, want := viaDNS.String(), testEndpoint+" -> my-proj.us-central1.my-cluster.my-inst"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	// The instance identity must not depend on how it was addressed.
	if viaDNS.URI != plain.URI {
		t.Errorf("URI differs between DNS and URI addressing: %+v vs %+v", viaDNS.URI, plain.URI)
	}
}

func TestURIResolver(t *testing.T) {
	ctx := context.Background()
	r := NewURIResolver()
	got, err := r.Resolve(ctx, testURI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := (DialTarget{URI: mustParse(t, testURI)}); got != want {
		t.Errorf("Resolve() = %+v, want %+v", got, want)
	}

	_, err = r.Resolve(ctx, testEndpoint)
	var cfgErr *errtype.ConfigError
	if !errors.As(err, &cfgErr) {
		t.Errorf("NewURIResolver() must not resolve DNS names; got %T: %v", err, err)
	}
}

func TestDNSResolver(t *testing.T) {
	ctx := context.Background()
	want := DialTarget{URI: mustParse(t, testURI), DomainName: testEndpoint}

	t.Run("single TXT record", func(t *testing.T) {
		f := &fakeNetResolver{records: []string{testURI}}
		got, err := NewDNSResolver(f).Resolve(ctx, testEndpoint)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != want {
			t.Errorf("Resolve() = %+v, want %+v", got, want)
		}
	})

	t.Run("whitespace around the record is trimmed", func(t *testing.T) {
		f := &fakeNetResolver{records: []string{"  " + testURI + "  "}}
		got, err := NewDNSResolver(f).Resolve(ctx, testEndpoint)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != want {
			t.Errorf("Resolve() = %+v, want %+v", got, want)
		}
	})

	t.Run("domain name is normalized (case, trailing dot)", func(t *testing.T) {
		f := &fakeNetResolver{records: []string{testURI}}
		r := NewDNSResolver(f)
		for _, name := range []string{testEndpoint + ".", "MY-ENDPOINT.Global.AlloyDB-PSA.goog"} {
			got, err := r.Resolve(ctx, name)
			if err != nil {
				t.Fatalf("Resolve(%q) unexpected error: %v", name, err)
			}
			if got != want {
				t.Errorf("Resolve(%q) = %+v, want %+v", name, got, want)
			}
		}
	})

	errTCs := []struct {
		desc     string
		name     string
		resolver *fakeNetResolver
		wantCfg  bool // ConfigError if true, RefreshError otherwise
	}{
		{
			desc:     "instance URI is rejected",
			name:     testURI,
			resolver: &fakeNetResolver{},
			wantCfg:  true,
		},
		{
			desc:     "invalid name",
			name:     "not a domain",
			resolver: &fakeNetResolver{},
			wantCfg:  true,
		},
		{
			desc:     "lookup error",
			name:     testEndpoint,
			resolver: &fakeNetResolver{err: errors.New("dns down")},
			wantCfg:  false,
		},
		{
			desc:     "zero records",
			name:     testEndpoint,
			resolver: &fakeNetResolver{records: []string{}},
			wantCfg:  true,
		},
		{
			desc:     "multiple records",
			name:     testEndpoint,
			resolver: &fakeNetResolver{records: []string{testURI, testURI}},
			wantCfg:  true,
		},
		{
			desc:     "record is not an instance URI",
			name:     testEndpoint,
			resolver: &fakeNetResolver{records: []string{"unrelated"}},
			wantCfg:  true,
		},
	}
	for _, tc := range errTCs {
		t.Run(tc.desc, func(t *testing.T) {
			_, err := NewDNSResolver(tc.resolver).Resolve(ctx, tc.name)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			var cfgErr *errtype.ConfigError
			var refErr *errtype.RefreshError
			switch {
			case tc.wantCfg && !errors.As(err, &cfgErr):
				t.Errorf("want *errtype.ConfigError, got %T: %v", err, err)
			case !tc.wantCfg && !errors.As(err, &refErr):
				t.Errorf("want *errtype.RefreshError, got %T: %v", err, err)
			}
		})
	}
}
