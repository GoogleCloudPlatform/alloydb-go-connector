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
	"fmt"
	"regexp"
	"strings"

	"cloud.google.com/go/alloydbconn/errtype"
)

// domainNameRegex matches a well-formed domain name in accordance with
// RFC 1035, RFC 1123 and RFC 2181. A trailing dot is permitted.
var domainNameRegex = regexp.MustCompile(
	`^(?:[_a-z0-9](?:[_a-z0-9-]{0,61}[a-z0-9])?\.)+(?:[a-z](?:[a-z0-9-]{0,61}[a-z0-9])?)?$`,
)

// IsValidDomain reports whether dn is a well-formed domain name.
func IsValidDomain(dn string) bool {
	return domainNameRegex.MatchString(strings.ToLower(dn))
}

// DialTarget is the result of resolving the instance string passed to
// Dial. It keeps the instance identity (URI) separate from how the caller
// addressed it (DomainName), so that InstanceURI remains a pure identity that
// is safe to use as a cache or metrics key.
type DialTarget struct {
	// URI identifies the AlloyDB instance.
	URI InstanceURI
	// DomainName is the normalized DNS name (lowercase, no trailing dot) that
	// resolved to URI, e.g. a write endpoint. It is empty when the caller
	// passed an instance URI.
	DomainName string
}

// HasDomainName reports whether the instance was resolved from a DNS name.
func (r DialTarget) HasDomainName() bool {
	return r.DomainName != ""
}

// String returns a representation for logs: "<domain> -> <instance>" when
// resolved from a DNS name, otherwise the instance short-hand.
func (r DialTarget) String() string {
	if r.HasDomainName() {
		return fmt.Sprintf("%s -> %s", r.DomainName, r.URI.String())
	}
	return r.URI.String()
}

// InstanceResolver resolves a user-provided instance string.
type InstanceResolver interface {
	// Resolve accepts a name and returns the instance it refers to. If the
	// name cannot be resolved, it returns an error.
	Resolve(ctx context.Context, name string) (DialTarget, error)
}

// NewURIResolver returns an InstanceResolver that only parses instance URIs.
// It does not use DNS.
func NewURIResolver() InstanceResolver {
	return uriResolver{}
}

// uriResolver parses instance URIs.
type uriResolver struct{}

// Resolve parses name as an instance URI.
func (uriResolver) Resolve(_ context.Context, name string) (DialTarget, error) {
	u, err := ParseInstURI(name)
	if err != nil {
		return DialTarget{}, err
	}
	return DialTarget{URI: u}, nil
}

// NetResolver groups the methods on net.Resolver that are used by the DNS
// resolver. This allows an application (or a test) to replace
// net.DefaultResolver with a custom implementation.
type NetResolver interface {
	LookupTXT(ctx context.Context, name string) ([]string, error)
}

// NewDNSResolver returns an InstanceResolver that resolves a DNS name (e.g. an
// AlloyDB write endpoint) whose TXT record holds an instance URI.
func NewDNSResolver(r NetResolver) InstanceResolver {
	return dnsResolver{r: r}
}

// dnsResolver resolves DNS names into instance URIs using TXT records.
type dnsResolver struct {
	r NetResolver
}

// Resolve returns the instance for name. name must be a valid domain name
// whose TXT record holds the instance URI.
func (d dnsResolver) Resolve(ctx context.Context, name string) (DialTarget, error) {
	if !IsValidDomain(name) {
		return DialTarget{}, errtype.NewConfigError("invalid domain name", name)
	}
	u, err := d.queryTXT(ctx, name)
	if err != nil {
		return DialTarget{}, err
	}
	return DialTarget{URI: u, DomainName: normalizeDomain(name)}, nil
}

// normalizeDomain lowercases dn and strips a trailing dot, so that equivalent
// spellings of a DNS name map to the same cache entry and TLS server name.
func normalizeDomain(dn string) string {
	return strings.ToLower(strings.TrimSuffix(dn, "."))
}

// queryTXT looks up the TXT record for domainName and returns the instance
// URI it contains.
//
// AlloyDB write endpoints publish exactly one TXT record holding the instance
// URI.
//
// Errors:
//   - a failed DNS lookup returns a RefreshError (transient)
//   - zero or multiple records, or a record that is not a valid instance URI,
//     returns a ConfigError (non-transient)
func (d dnsResolver) queryTXT(ctx context.Context, domainName string) (InstanceURI, error) {
	records, err := d.r.LookupTXT(ctx, domainName)
	if err != nil {
		return InstanceURI{}, errtype.NewRefreshError(
			fmt.Sprintf("failed to look up TXT record for %q", domainName),
			domainName,
			err,
		)
	}
	if len(records) != 1 {
		return InstanceURI{}, errtype.NewConfigError(
			fmt.Sprintf("expected exactly 1 TXT record for %q, found %d", domainName, len(records)),
			domainName,
		)
	}
	u, err := ParseInstURI(strings.TrimSpace(records[0]))
	if err != nil {
		return InstanceURI{}, errtype.NewConfigError(
			fmt.Sprintf("TXT record for %q is not a valid instance URI", domainName),
			domainName,
		)
	}
	return u, nil
}
