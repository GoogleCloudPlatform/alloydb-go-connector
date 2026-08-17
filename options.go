// Copyright 2020 Google LLC
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
	"crypto/rsa"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"cloud.google.com/go/alloydbconn/debug"
	"cloud.google.com/go/alloydbconn/errtype"
	"cloud.google.com/go/alloydbconn/internal/alloydb"
	"cloud.google.com/go/auth"
	"cloud.google.com/go/auth/credentials"
	"cloud.google.com/go/auth/oauth2adapt"
	"golang.org/x/net/proxy"
	"golang.org/x/oauth2"
	"google.golang.org/api/option"
)

const (
	// CloudPlatformScope is the default OAuth2 scope set on the API client.
	CloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"
	// AlloyDBLoginScope is the OAuth2 scope used for IAM Authentication.
	AlloyDBLoginScope = "https://www.googleapis.com/auth/alloydb.login"

	// Default GDU API Domain.
	defaultUniverseDomain = "googleapis.com"
)

// An Option is an option for configuring a Dialer.
type Option func(d *dialerConfig)

func newDialerConfig(opts ...Option) (*dialerConfig, error) {
	d := &dialerConfig{
		refreshTimeout: alloydb.RefreshTimeout,
		dialFunc:       proxy.Dial,
		logger:         nullLogger{},
		userAgents:     []string{userAgent},
	}
	for _, opt := range opts {
		opt(d)
	}

	badPairs := map[bool]string{
		d.credentialsFile != "" && d.credentialsJSON != nil:              "incompatible options: WithCredentialsFile cannot be used with WithCredentialsJSON",
		d.credentialsFile != "" && d.tokenProvider != nil:                "incompatible options: WithCredentialsFile cannot be used with WithTokenSource",
		d.credentialsJSON != nil && d.tokenProvider != nil:               "incompatible options: WithCredentialsJSON cannot be used with WithTokenSource",
		d.credentials != nil && d.credentialsFile != "":                  "incompatible options: WithCredentials cannot be used with WithCredentialsFile",
		d.credentials != nil && d.credentialsJSON != nil:                 "incompatible options: WithCredentials cannot be used with WithCredentialsJSON",
		d.credentials != nil && d.tokenProvider != nil:                   "incompatible options: WithCredentials cannot be used with WithTokenSource",
		d.adminAPIEndpoint != "" && d.universeDomain != "googleapis.com": "incompatible options: WithAdminAPIEndpoint cannot be used with TPC UniverseDomain",
	}
	for bad, msg := range badPairs {
		if bad {
			return nil, errors.New(msg)
		}
	}

	switch {
	case d.credentialsFile != "":
		b, err := os.ReadFile(d.credentialsFile)
		if err != nil {
			return nil, errtype.NewConfigError(err.Error(), "n/a")
		}
		c, err := credentials.DetectDefault(&credentials.DetectOptions{
			Scopes:          []string{CloudPlatformScope},
			CredentialsJSON: b,
			UniverseDomain:  d.clientUniverseDomain(),
		})
		if err != nil {
			return nil, errtype.NewConfigError(err.Error(), "n/a")
		}
		d.clientOpts = append(d.clientOpts, option.WithAuthCredentials(WithUniverseDomainCredentials(c, d.clientUniverseDomain())))
		// Now rebuild credentials with the Login Scope
		c, err = credentials.DetectDefault(&credentials.DetectOptions{
			Scopes:          []string{AlloyDBLoginScope},
			CredentialsJSON: b,
			UniverseDomain:  d.clientUniverseDomain(),
		})
		if err != nil {
			return nil, errtype.NewConfigError(err.Error(), "n/a")
		}
		d.iamAuthNTokenProvider = WithUniverseDomainCredentials(c, d.clientUniverseDomain()).TokenProvider
	case d.credentialsJSON != nil:
		c, err := credentials.DetectDefault(&credentials.DetectOptions{
			Scopes:          []string{CloudPlatformScope},
			CredentialsJSON: d.credentialsJSON,
			UniverseDomain:  d.clientUniverseDomain(),
		})
		if err != nil {
			return nil, errtype.NewConfigError(err.Error(), "n/a")
		}
		d.clientOpts = append(d.clientOpts, option.WithAuthCredentials(WithUniverseDomainCredentials(c, d.clientUniverseDomain())))

		// Now rebuild credentials with the Login Scope
		c, err = credentials.DetectDefault(&credentials.DetectOptions{
			Scopes:          []string{AlloyDBLoginScope},
			CredentialsJSON: d.credentialsJSON,
			UniverseDomain:  d.clientUniverseDomain(),
		})
		if err != nil {
			return nil, errtype.NewConfigError(err.Error(), "n/a")
		}
		d.iamAuthNTokenProvider = WithUniverseDomainCredentials(c, d.clientUniverseDomain()).TokenProvider
	case d.tokenProvider != nil:
		c := auth.NewCredentials(&auth.CredentialsOptions{
			TokenProvider: d.tokenProvider,
		})
		d.clientOpts = append(d.clientOpts, option.WithAuthCredentials(c))
		d.iamAuthNTokenProvider = d.tokenProvider
	case d.credentials != nil:
		c := WithUniverseDomainCredentials(d.credentials, d.clientUniverseDomain())
		d.iamAuthNTokenProvider = c.TokenProvider
		d.clientOpts = append(d.clientOpts, option.WithAuthCredentials(c))
	default:
		// If a credentials file, credentials JSON, or a token source was not provided,
		// default to Application Default Credentials.
		c, err := credentials.DetectDefault(&credentials.DetectOptions{
			Scopes:         []string{CloudPlatformScope},
			UniverseDomain: d.clientUniverseDomain(),
		})
		if err != nil {
			return nil, err
		}
		d.clientOpts = append(d.clientOpts, option.WithAuthCredentials(WithUniverseDomainCredentials(c, d.clientUniverseDomain())))
		c, err = credentials.DetectDefault(&credentials.DetectOptions{
			Scopes:         []string{AlloyDBLoginScope},
			UniverseDomain: d.clientUniverseDomain(),
		})
		if err != nil {
			return nil, err
		}
		d.iamAuthNTokenProvider = WithUniverseDomainCredentials(c, d.clientUniverseDomain()).TokenProvider
	}

	if d.iamAuthNTokenProviderOverride != nil {
		d.iamAuthNTokenProvider = d.iamAuthNTokenProviderOverride
	}

	if d.httpClient != nil {
		d.clientOpts = append(d.clientOpts, option.WithHTTPClient(d.httpClient))
	}

	if d.adminAPIEndpoint != "" {
		d.alloydbClientOpts = append(d.alloydbClientOpts, option.WithEndpoint(d.adminAPIEndpoint))
	}

	userAgent := strings.Join(d.userAgents, " ")
	// Add user agent to the end to make sure it's not overridden.
	d.clientOpts = append(d.clientOpts, option.WithUserAgent(userAgent))

	return d, nil
}

// clientUniverseDomain returns the default service domain for a given Cloud
// universe, with the following precedence:
//
// 1. A non-empty option.WithUniverseDomain or similar client option.
// 2. The default value "googleapis.com".
func (d *dialerConfig) clientUniverseDomain() string {
	if d.universeDomain != "" {
		return d.universeDomain
	}
	return defaultUniverseDomain
}

type dialerConfig struct {
	rsaKey *rsa.PrivateKey
	// alloydbClientOpts are options to configure only the AlloyDB Rest API
	// client. Configuration that should apply to all Google Cloud API clients
	// should be included in clientOpts.
	alloydbClientOpts []option.ClientOption
	// clientOpts are options to configure any Google Cloud API client. They
	// should not include any AlloyDB-specific configuration.
	clientOpts       []option.ClientOption
	dialOpts         []DialOption
	dialFunc         func(ctx context.Context, network, addr string) (net.Conn, error)
	refreshTimeout   time.Duration
	userAgents       []string
	useIAMAuthN      bool
	logger           debug.ContextLogger
	lazyRefresh      bool
	adminAPIEndpoint string
	universeDomain   string

	credentials           *auth.Credentials
	tokenProvider         auth.TokenProvider
	iamAuthNTokenProvider auth.TokenProvider
	credentialsFile       string
	credentialsJSON       []byte
	httpClient            *http.Client

	// iamAuthNTokenProviderOverride if set replaces the iamAuthNTokenProvider
	// above.
	iamAuthNTokenProviderOverride auth.TokenProvider

	// disableBuiltInTelemetry disables the internal metric exporter.
	disableBuiltInTelemetry bool

	staticConnInfo io.Reader
}

// WithUniverseDomain configures the underlying AlloyDB Admin API client and
// credentials to use the provided universe domain. Enables Trusted Partner Cloud (TPC).
func WithUniverseDomain(ud string) Option {
	return func(d *dialerConfig) {
		if ud == "" {
			return
		}
		d.universeDomain = ud
		d.alloydbClientOpts = append(d.alloydbClientOpts, option.WithUniverseDomain(ud))
	}
}

// WithUniverseDomainCredentials configures custom credential to be TPC aware.
func WithUniverseDomainCredentials(c *auth.Credentials, ud string) *auth.Credentials {
	if c == nil || ud == "" || ud == defaultUniverseDomain {
		return c
	}
	return auth.NewCredentials(&auth.CredentialsOptions{
		TokenProvider: c.TokenProvider,
		JSON:          c.JSON(),
		ProjectIDProvider: auth.CredentialsPropertyFunc(func(ctx context.Context) (string, error) {
			return c.ProjectID(ctx)
		}),
		QuotaProjectIDProvider: auth.CredentialsPropertyFunc(func(ctx context.Context) (string, error) {
			return c.QuotaProjectID(ctx)
		}),
		UniverseDomainProvider: auth.CredentialsPropertyFunc(func(_ context.Context) (string, error) {
			return ud, nil
		}),
	})
}

// WithOptions turns a list of Option's into a single Option.
func WithOptions(opts ...Option) Option {
	return func(d *dialerConfig) {
		for _, opt := range opts {
			opt(d)
		}
	}
}

// WithCredentials returns an option that specifies an auth.Credentials object
// to use for all AlloyDB API interactions.
func WithCredentials(c *auth.Credentials) Option {
	return func(d *dialerConfig) {
		d.credentials = c
	}
}

// WithIAMAuthNCredentials configures the credentials used for IAM
// authentication. When this option isn't set, the connector will use the
// credentials configured with other options or Application Default Credentials
// for IAM authentication.
func WithIAMAuthNCredentials(c *auth.Credentials) Option {
	return func(d *dialerConfig) {
		d.iamAuthNTokenProviderOverride = c.TokenProvider
	}
}

// WithCredentialsFile returns an Option that specifies a service account
// or refresh token JSON credentials file to be used as the basis for
// authentication.
func WithCredentialsFile(filename string) Option {
	return func(d *dialerConfig) {
		d.credentialsFile = filename
	}
}

// WithCredentialsJSON returns an Option that specifies a service account
// or refresh token JSON credentials to be used as the basis for authentication.
func WithCredentialsJSON(b []byte) Option {
	return func(d *dialerConfig) {
		d.credentialsJSON = b
	}
}

// WithUserAgent returns an Option that sets the User-Agent.
func WithUserAgent(ua string) Option {
	return func(d *dialerConfig) {
		d.userAgents = append(d.userAgents, ua)
	}
}

// WithDefaultDialOptions returns an Option that specifies the default
// DialOptions used.
func WithDefaultDialOptions(opts ...DialOption) Option {
	return func(d *dialerConfig) {
		d.dialOpts = append(d.dialOpts, opts...)
	}
}

// WithTokenSource returns an Option that specifies an OAuth2 token source
// to be used as the basis for authentication.

// WithTokenSource returns an Option that specifies an OAuth2 token source to be
// used as the basis for authentication.
//
// When Auth IAM AuthN is enabled, use WithIAMAuthNTokenSources or WithIAMAuthNCredentials to set the token
// source for login tokens separately from the API client token source.
//
// You may only use one of the following options:
// WithIAMAuthNCredentials, WithIAMAuthNTokenSources, WithCredentials, WithTokenSource
func WithTokenSource(s oauth2.TokenSource) Option {
	return func(d *dialerConfig) {
		tp := oauth2adapt.TokenProviderFromTokenSource(s)
		d.tokenProvider = tp
	}
}

// WithIAMAuthNTokenSource sets the token use used for IAM Authentication.
// Any AlloyDB API interactions will not use this token source.
//
// The IAM AuthN token source on the other hand should only have:
//
//   - https://www.googleapis.com/auth/alloydb.login
func WithIAMAuthNTokenSource(s oauth2.TokenSource) Option {
	return func(d *dialerConfig) {
		tp := oauth2adapt.TokenProviderFromTokenSource(s)
		d.iamAuthNTokenProviderOverride = tp
	}
}

// WithRSAKey returns an Option that specifies a rsa.PrivateKey used to
// represent the client.
func WithRSAKey(k *rsa.PrivateKey) Option {
	return func(d *dialerConfig) {
		d.rsaKey = k
	}
}

// WithRefreshTimeout returns an Option that sets a timeout on refresh
// operations. Defaults to 60s.
func WithRefreshTimeout(t time.Duration) Option {
	return func(d *dialerConfig) {
		d.refreshTimeout = t
	}
}

// WithHTTPClient configures the underlying AlloyDB Admin API client with the
// provided HTTP client. This option is generally unnecessary except for
// advanced use-cases.
func WithHTTPClient(client *http.Client) Option {
	return func(d *dialerConfig) {
		d.httpClient = client
	}
}

// WithAdminAPIEndpoint configures the underlying AlloyDB Admin API client to
// use the provided URL.
func WithAdminAPIEndpoint(url string) Option {
	return func(d *dialerConfig) {
		d.adminAPIEndpoint = url
	}
}

// WithDialFunc configures the function used to connect to the address on the
// named network. This option is generally unnecessary except for advanced
// use-cases. The function is used for all invocations of Dial. To configure
// a dial function per individual calls to dial, use WithOneOffDialFunc.
func WithDialFunc(dial func(ctx context.Context, network, addr string) (net.Conn, error)) Option {
	return func(d *dialerConfig) {
		d.dialFunc = dial
	}
}

// WithIAMAuthN enables automatic IAM Authentication. If no token source has
// been configured (such as with WithTokenSource, WithCredentialsFile, etc),
// the dialer will use the default token source as defined by
// https://pkg.go.dev/golang.org/x/oauth2/google#FindDefaultCredentialsWithParams.
func WithIAMAuthN() Option {
	return func(d *dialerConfig) {
		d.useIAMAuthN = true
	}
}

type debugLoggerWithoutContext struct {
	logger debug.Logger
}

// Debugf implements debug.ContextLogger.
func (d *debugLoggerWithoutContext) Debugf(_ context.Context, format string, args ...any) {
	d.logger.Debugf(format, args...)
}

var _ debug.ContextLogger = new(debugLoggerWithoutContext)

// WithDebugLogger configures a debug logger for reporting on internal
// operations. By default the debug logger is disabled.
// Prefer WithContextLogger.
func WithDebugLogger(l debug.Logger) Option {
	return func(d *dialerConfig) {
		d.logger = &debugLoggerWithoutContext{l}
	}
}

// WithContextLogger configures a debug lgoger for reporting on internal
// operations. By default the debug logger is disabled.
func WithContextLogger(l debug.ContextLogger) Option {
	return func(d *dialerConfig) {
		d.logger = l
	}
}

// WithLazyRefresh configures the dialer to refresh certificates on an
// as-needed basis. If a certificate is expired when a connection request
// occurs, the Go Connector will block the attempt and refresh the certificate
// immediately. This option is useful when running the Go Connector in
// environments where the CPU may be throttled, thus preventing a background
// goroutine from running consistently (e.g., in Cloud Run the CPU is throttled
// outside of a request context causing the background refresh to fail).
func WithLazyRefresh() Option {
	return func(d *dialerConfig) {
		d.lazyRefresh = true
	}
}

// WithStaticConnectionInfo specifies an io.Reader from which to read static
// connection info. This is a *dev-only* option and should not be used in
// production as it will result in failed connections after the client
// certificate expires. It is also subject to breaking changes in the format.
// NOTE: The static connection info is not refreshed by the dialer. The JSON
// format supports multiple instances, regardless of cluster.
//
// The reader should hold JSON with the following format:
//
//	{
//	    "publicKey": "<PEM Encoded public RSA key>",
//	    "privateKey": "<PEM Encoded private RSA key>",
//	    "projects/<PROJECT>/locations/<REGION>/clusters/<CLUSTER>/instances/<INSTANCE>": {
//	        "ipAddress": "<PSA-based private IP address>",
//	        "publicIpAddress": "<public IP address>",
//	        "pscInstanceConfig": {
//	            "pscDnsName": "<PSC DNS name>"
//	        },
//	        "pemCertificateChain": [
//	            "<client cert>", "<intermediate cert>", "<CA cert>"
//	        ],
//	        "caCert": "<CA cert>"
//	    }
//	}
func WithStaticConnectionInfo(r io.Reader) Option {
	return func(d *dialerConfig) {
		d.staticConnInfo = r
	}
}

// WithOptOutOfAdvancedConnectionCheck is a no-op.
//
// Deprecated: In the past this was used to help internal clients migrate
// to connections using the metadata exchange. The migration is complete
// and now this method is a no-op.
func WithOptOutOfAdvancedConnectionCheck() Option {
	return func(_ *dialerConfig) {}
}

// WithOptOutOfBuiltInTelemetry disables the internal metric export. By
// default, the Dialer will report on its internal operations to the
// alloydb.googleapis.com system metric prefix. These metrics help AlloyDB
// improve performance and identify client connectivity problems. Presently,
// these metrics aren't public, but will be made public in the future. To
// disable this telemetry, provide this option when initializing a Dialer.
func WithOptOutOfBuiltInTelemetry() Option {
	return func(d *dialerConfig) {
		d.disableBuiltInTelemetry = true
	}
}

// A DialOption is an option for configuring how a Dialer's Dial call is
// executed.
type DialOption func(d *dialCfg)

type dialCfg struct {
	dialFunc     func(ctx context.Context, network, addr string) (net.Conn, error)
	ipType       string
	tcpKeepAlive time.Duration
	iamAuthN     bool
}

// DialOptions turns a list of DialOption instances into an DialOption.
func DialOptions(opts ...DialOption) DialOption {
	return func(cfg *dialCfg) {
		for _, opt := range opts {
			opt(cfg)
		}
	}
}

// WithOneOffDialFunc configures the dial function on a one-off basis for an
// individual call to Dial. To configure a dial function across all invocations
// of Dial, use WithDialFunc.
func WithOneOffDialFunc(dial func(ctx context.Context, network, addr string) (net.Conn, error)) DialOption {
	return func(c *dialCfg) {
		c.dialFunc = dial
	}
}

// WithTCPKeepAlive returns a DialOption that specifies the tcp keep alive
// period for the connection returned by Dial.
func WithTCPKeepAlive(d time.Duration) DialOption {
	return func(cfg *dialCfg) {
		cfg.tcpKeepAlive = d
	}
}

// WithPublicIP returns a DialOption that specifies a public IP will be used to
// connect.
func WithPublicIP() DialOption {
	return func(cfg *dialCfg) {
		cfg.ipType = alloydb.PublicIP
	}
}

// WithPrivateIP returns a DialOption that specifies a private IP (VPC) will be
// used to connect.
func WithPrivateIP() DialOption {
	return func(cfg *dialCfg) {
		cfg.ipType = alloydb.PrivateIP
	}
}

// WithPSC returns a DialOption that specifies a PSC endpoint will be used to
// connect.
func WithPSC() DialOption {
	return func(cfg *dialCfg) {
		cfg.ipType = alloydb.PSC
	}
}

// WithDialIAMAuthN allows calls to Dial to enable or disable IAM AuthN on a
// one-off basis, regardless whether the dialer itself is configured with IAM
// AuthN. There is no performance penalty to using this option.
func WithDialIAMAuthN(enabled bool) DialOption {
	return func(cfg *dialCfg) {
		cfg.iamAuthN = enabled
	}
}
