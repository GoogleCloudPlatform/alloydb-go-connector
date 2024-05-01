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
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	alloydbadmin "cloud.google.com/go/alloydb/apiv1alpha"
	"cloud.google.com/go/alloydb/connectors/apiv1alpha/connectorspb"
	"cloud.google.com/go/alloydbconn/debug"
	"cloud.google.com/go/alloydbconn/errtype"
	"cloud.google.com/go/alloydbconn/internal/alloydb"
	"cloud.google.com/go/alloydbconn/internal/trace"
	"github.com/google/uuid"
	"golang.org/x/net/proxy"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
	"google.golang.org/protobuf/proto"
)

const (
	// defaultTCPKeepAlive is the default keep alive value used on connections
	// to a AlloyDB instance
	defaultTCPKeepAlive = 30 * time.Second
	// serverProxyPort is the port the server-side proxy receives connections on.
	serverProxyPort = "5433"
	// ioTimeout is the maximum amount of time to wait before aborting a
	// metadata exhange
	ioTimeout = 30 * time.Second
)

var (
	// ErrDialerClosed is used when a caller invokes Dial after closing the
	// Dialer.
	ErrDialerClosed = errors.New("alloydbconn: dialer is closed")
	// versionString indicates the version of this library.
	//go:embed version.txt
	versionString string
	userAgent     = "alloydb-go-connector/" + strings.TrimSpace(versionString)

	// defaultKey is the default RSA public/private keypair used by the clients.
	defaultKey    *rsa.PrivateKey
	defaultKeyErr error
	keyOnce       sync.Once
)

func getDefaultKeys() (*rsa.PrivateKey, error) {
	keyOnce.Do(func() {
		defaultKey, defaultKeyErr = rsa.GenerateKey(rand.Reader, 2048)
	})
	return defaultKey, defaultKeyErr
}

type connectionInfoCache interface {
	ConnectionInfo(context.Context) (alloydb.ConnectionInfo, error)
	ForceRefresh()
	io.Closer
}

// monitoredCache is a wrapper around a connectionInfoCache that tracks the
// number of connections to the associated instance.
type monitoredCache struct {
	openConns uint64
	connectionInfoCache
}

// A Dialer is used to create connections to AlloyDB instance.
//
// Use NewDialer to initialize a Dialer.
type Dialer struct {
	lock           sync.RWMutex
	cache          map[alloydb.InstanceURI]monitoredCache
	key            *rsa.PrivateKey
	refreshTimeout time.Duration
	// closed reports if the dialer has been closed.
	closed chan struct{}

	// lazyRefresh determines what kind of caching is used for ephemeral
	// certificates. When lazyRefresh is true, the dialer will use a lazy
	// cache, refresh certificates only when a connection attempt needs a fresh
	// certificate. Otherwise, a refresh ahead cache will be used. The refresh
	// ahead cache assumes a background goroutine may run consistently.
	lazyRefresh bool

	client *alloydbadmin.AlloyDBAdminClient
	logger debug.Logger

	// defaultDialCfg holds the constructor level DialOptions, so that it can
	// be copied and mutated by the Dial function.
	defaultDialCfg dialCfg

	// dialerID uniquely identifies a Dialer. Used for monitoring purposes,
	// *only* when a client has configured OpenCensus exporters.
	dialerID string

	// dialFunc is the function used to connect to the address on the named
	// network. By default it is golang.org/x/net/proxy#Dial.
	dialFunc func(cxt context.Context, network, addr string) (net.Conn, error)

	useIAMAuthN    bool
	iamTokenSource oauth2.TokenSource
	userAgent      string

	buffer *buffer
}

type nullLogger struct{}

func (nullLogger) Debugf(string, ...interface{}) {}

// NewDialer creates a new Dialer.
//
// Initial calls to NewDialer make take longer than normal because generation of an
// RSA keypair is performed. Calls with a WithRSAKeyPair DialOption or after a default
// RSA keypair is generated will be faster.
func NewDialer(ctx context.Context, opts ...Option) (*Dialer, error) {
	cfg := &dialerConfig{
		refreshTimeout: alloydb.RefreshTimeout,
		dialFunc:       proxy.Dial,
		logger:         nullLogger{},
		userAgents:     []string{userAgent},
	}
	for _, opt := range opts {
		opt(cfg)
		if cfg.err != nil {
			return nil, cfg.err
		}
	}
	userAgent := strings.Join(cfg.userAgents, " ")
	// Add this to the end to make sure it's not overridden
	cfg.adminOpts = append(cfg.adminOpts, option.WithUserAgent(userAgent))

	if cfg.rsaKey == nil {
		key, err := getDefaultKeys()
		if err != nil {
			return nil, fmt.Errorf("failed to generate RSA keys: %v", err)
		}
		cfg.rsaKey = key
	}

	// If no token source is configured, use ADC's token source.
	ts := cfg.tokenSource
	if ts == nil {
		var err error
		ts, err = google.DefaultTokenSource(ctx, CloudPlatformScope)
		if err != nil {
			return nil, err
		}
	}

	client, err := alloydbadmin.NewAlloyDBAdminRESTClient(ctx, cfg.adminOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create AlloyDB Admin API client: %v", err)
	}

	dialCfg := dialCfg{
		ipType:       alloydb.PrivateIP,
		tcpKeepAlive: defaultTCPKeepAlive,
	}
	for _, opt := range cfg.dialOpts {
		opt(&dialCfg)
	}

	if err := trace.InitMetrics(); err != nil {
		return nil, err
	}
	d := &Dialer{
		closed:         make(chan struct{}),
		cache:          make(map[alloydb.InstanceURI]monitoredCache),
		lazyRefresh:    cfg.lazyRefresh,
		key:            cfg.rsaKey,
		refreshTimeout: cfg.refreshTimeout,
		client:         client,
		logger:         cfg.logger,
		defaultDialCfg: dialCfg,
		dialerID:       uuid.New().String(),
		dialFunc:       cfg.dialFunc,
		useIAMAuthN:    cfg.useIAMAuthN,
		iamTokenSource: ts,
		userAgent:      userAgent,
		buffer:         newBuffer(),
	}
	return d, nil
}

// Dial returns a net.Conn connected to the specified AlloyDB instance. The
// instance argument must be the instance's URI, which is in the format
// projects/<PROJECT>/locations/<REGION>/clusters/<CLUSTER>/instances/<INSTANCE>
func (d *Dialer) Dial(ctx context.Context, instance string, opts ...DialOption) (conn net.Conn, err error) {
	select {
	case <-d.closed:
		return nil, ErrDialerClosed
	default:
	}
	startTime := time.Now()
	var endDial trace.EndSpanFunc
	ctx, endDial = trace.StartSpan(ctx, "cloud.google.com/go/alloydbconn.Dial",
		trace.AddInstanceName(instance),
		trace.AddDialerID(d.dialerID),
	)
	defer func() {
		go trace.RecordDialError(context.Background(), instance, d.dialerID, err)
		endDial(err)
	}()
	cfg := d.defaultDialCfg
	for _, opt := range opts {
		opt(&cfg)
	}
	inst, err := alloydb.ParseInstURI(instance)
	if err != nil {
		return nil, err
	}

	var endInfo trace.EndSpanFunc
	ctx, endInfo = trace.StartSpan(ctx, "cloud.google.com/go/alloydbconn/internal.InstanceInfo")
	cache, err := d.connectionInfoCache(inst)
	if err != nil {
		endInfo(err)
		return nil, err
	}
	ci, err := cache.ConnectionInfo(ctx)
	if err != nil {
		d.removeCached(inst, cache, err)
		endInfo(err)
		return nil, err
	}
	endInfo(err)

	// If the client certificate has expired (as when the computer goes to
	// sleep, and the refresh cycle cannot run), force a refresh immediately.
	// The TLS handshake will not fail on an expired client certificate. It's
	// not until the first read where the client cert error will be surfaced.
	// So check that the certificate is valid before proceeding.
	if invalidClientCert(inst, d.logger, ci.Expiration) {
		d.logger.Debugf("[%v] Refreshing certificate now", inst.String())
		cache.ForceRefresh()
		// Block on refreshed connection info
		ci, err = cache.ConnectionInfo(ctx)
		if err != nil {
			d.removeCached(inst, cache, err)
			return nil, err
		}
	}
	addr, ok := ci.IPAddrs[cfg.ipType]
	if !ok {
		d.removeCached(inst, cache, err)
		err := errtype.NewConfigError(
			fmt.Sprintf("instance does not have IP of type %q", cfg.ipType),
			inst.String(),
		)
		return nil, err
	}

	var connectEnd trace.EndSpanFunc
	ctx, connectEnd = trace.StartSpan(ctx, "cloud.google.com/go/alloydbconn/internal.Connect")
	defer func() { connectEnd(err) }()
	hostPort := net.JoinHostPort(addr, serverProxyPort)
	f := d.dialFunc
	if cfg.dialFunc != nil {
		f = cfg.dialFunc
	}
	d.logger.Debugf("[%v] Dialing %v", inst.String(), hostPort)
	conn, err = f(ctx, "tcp", hostPort)
	if err != nil {
		d.logger.Debugf("[%v] Dialing %v failed: %v", inst.String(), hostPort, err)
		// refresh the instance info in case it caused the connection failure
		cache.ForceRefresh()
		return nil, errtype.NewDialError("failed to dial", inst.String(), err)
	}
	if c, ok := conn.(*net.TCPConn); ok {
		if err := c.SetKeepAlive(true); err != nil {
			return nil, errtype.NewDialError("failed to set keep-alive", inst.String(), err)
		}
		if err := c.SetKeepAlivePeriod(cfg.tcpKeepAlive); err != nil {
			return nil, errtype.NewDialError("failed to set keep-alive period", inst.String(), err)
		}
	}

	// TODO: use the correct addr as server name once PSC DNS is populated
	// in all existing clusters. When that happens, delete this if statement.
	serverName := addr
	if cfg.ipType == alloydb.PSC {
		serverName, ok = ci.IPAddrs[alloydb.PrivateIP]
		if !ok {
			// This shouldn't happen, but be prudent regardless.
			return nil, errtype.NewDialError(
				"failed to lookup server name", inst.String(), nil,
			)
		}
	}
	c := &tls.Config{
		Certificates: []tls.Certificate{ci.ClientCert},
		RootCAs:      ci.RootCAs,
		// The PSC, private, and public IP all appear in the certificate as
		// SAN. Use the server name that corresponds to the requested
		// connection path.
		ServerName: serverName,
		MinVersion: tls.VersionTLS13,
	}
	tlsConn := tls.Client(conn, c)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		d.logger.Debugf("[%v] TLS handshake failed: %v", inst.String(), err)
		// refresh the instance info in case it caused the handshake failure
		cache.ForceRefresh()
		_ = tlsConn.Close() // best effort close attempt
		return nil, errtype.NewDialError("handshake failed", inst.String(), err)
	}

	// The metadata exchange must occur after the TLS connection is established
	// to avoid leaking sensitive information.
	err = d.metadataExchange(tlsConn)
	if err != nil {
		_ = tlsConn.Close() // best effort close attempt
		return nil, err
	}

	latency := time.Since(startTime).Milliseconds()
	go func() {
		n := atomic.AddUint64(&cache.openConns, 1)
		trace.RecordOpenConnections(ctx, int64(n), d.dialerID, inst.String())
		trace.RecordDialLatency(ctx, instance, d.dialerID, latency)
	}()

	return newInstrumentedConn(tlsConn, func() {
		n := atomic.AddUint64(&cache.openConns, ^uint64(0))
		trace.RecordOpenConnections(context.Background(), int64(n), d.dialerID, inst.String())
	}), nil
}

// removeCached stops all background refreshes and deletes the connection
// info cache from the map of caches.
func (d *Dialer) removeCached(
	i alloydb.InstanceURI, c connectionInfoCache, err error,
) {
	d.logger.Debugf(
		"[%v] Removing connection info from cache: %v",
		i.String(),
		err,
	)
	d.lock.Lock()
	defer d.lock.Unlock()
	c.Close()
	delete(d.cache, i)
}

func invalidClientCert(
	inst alloydb.InstanceURI, l debug.Logger, expiration time.Time,
) bool {
	now := time.Now().UTC()
	notAfter := expiration.UTC()
	invalid := now.After(notAfter)
	l.Debugf(
		"[%v] Now = %v, Current cert expiration = %v",
		inst.String(),
		now.Format(time.RFC3339),
		notAfter.Format(time.RFC3339),
	)
	l.Debugf("[%v] Cert is valid = %v", inst.String(), !invalid)
	return invalid
}

// metadataExchange sends metadata about the connection prior to the database
// protocol taking over. The exchange consists of four steps:
//
//  1. Prepare a MetadataExchangeRequest including the IAM Principal's OAuth2
//     token, the user agent, and the requested authentication type.
//
//  2. Write the size of the message as a big endian uint32 (4 bytes) to the
//     server followed by the marshaled message. The length does not include the
//     initial four bytes.
//
//  3. Read a big endian uint32 (4 bytes) from the server. This is the
//     MetadataExchangeResponse message length and does not include the initial
//     four bytes.
//
//  4. Unmarshal the response using the message length in step 3. If the
//     response is not OK, return the response's error. If there is no error, the
//     metadata exchange has succeeded and the connection is complete.
//
// Subsequent interactions with the server use the database protocol.
func (d *Dialer) metadataExchange(conn net.Conn) error {
	tok, err := d.iamTokenSource.Token()
	if err != nil {
		return err
	}
	authType := connectorspb.MetadataExchangeRequest_DB_NATIVE
	if d.useIAMAuthN {
		authType = connectorspb.MetadataExchangeRequest_AUTO_IAM
	}
	req := &connectorspb.MetadataExchangeRequest{
		UserAgent:   d.userAgent,
		AuthType:    authType,
		Oauth2Token: tok.AccessToken,
	}
	m, err := proto.Marshal(req)
	if err != nil {
		return err
	}
	b := d.buffer.get()
	defer d.buffer.put(b)

	buf := *b
	reqSize := proto.Size(req)
	binary.BigEndian.PutUint32(buf, uint32(reqSize))
	buf = append(buf[:4], m...)

	// Set IO deadline before write
	err = conn.SetDeadline(time.Now().Add(ioTimeout))
	if err != nil {
		return err
	}
	defer conn.SetDeadline(time.Time{})

	_, err = conn.Write(buf)
	if err != nil {
		return err
	}

	// Reset IO deadline before read
	err = conn.SetDeadline(time.Now().Add(ioTimeout))
	if err != nil {
		return err
	}
	defer conn.SetDeadline(time.Time{})

	buf = buf[:4]
	_, err = conn.Read(buf)
	if err != nil {
		return err
	}

	respSize := binary.BigEndian.Uint32(buf)
	resp := buf[:respSize]
	_, err = conn.Read(resp)
	if err != nil {
		return err
	}

	var mdxResp connectorspb.MetadataExchangeResponse
	err = proto.Unmarshal(resp, &mdxResp)
	if err != nil {
		return err
	}

	if mdxResp.GetResponseCode() != connectorspb.MetadataExchangeResponse_OK {
		return errors.New(mdxResp.GetError())
	}

	return nil
}

const maxMessageSize = 16 * 1024 // 16 kb

type buffer struct {
	pool sync.Pool
}

func newBuffer() *buffer {
	return &buffer{
		pool: sync.Pool{
			New: func() any {
				buf := make([]byte, maxMessageSize)
				return &buf
			},
		},
	}
}

func (b *buffer) get() *[]byte {
	return b.pool.Get().(*[]byte)
}

func (b *buffer) put(buf *[]byte) {
	b.pool.Put(buf)
}

// newInstrumentedConn initializes an instrumentedConn that on closing will
// decrement the number of open connects and record the result.
func newInstrumentedConn(conn net.Conn, closeFunc func()) *instrumentedConn {
	return &instrumentedConn{
		Conn:      conn,
		closeFunc: closeFunc,
	}
}

// instrumentedConn wraps a net.Conn and invokes closeFunc when the connection
// is closed.
type instrumentedConn struct {
	net.Conn
	closeFunc func()
}

// Close delegates to the underlying net.Conn interface and reports the close
// to the provided closeFunc only when Close returns no error.
func (i *instrumentedConn) Close() error {
	err := i.Conn.Close()
	if err != nil {
		return err
	}
	go i.closeFunc()
	return nil
}

// Close closes the Dialer; it prevents the Dialer from refreshing the information
// needed to connect. Additional dial operations may succeed until the information
// expires.
func (d *Dialer) Close() error {
	// Check if Close has already been called.
	select {
	case <-d.closed:
		return nil
	default:
	}
	close(d.closed)

	d.lock.Lock()
	defer d.lock.Unlock()
	for _, i := range d.cache {
		i.Close()
	}
	return nil
}

func (d *Dialer) connectionInfoCache(
	uri alloydb.InstanceURI,
) (monitoredCache, error) {
	d.lock.RLock()
	c, ok := d.cache[uri]
	d.lock.RUnlock()
	if !ok {
		d.lock.Lock()
		defer d.lock.Unlock()
		// Recheck to ensure instance wasn't created between locks
		c, ok = d.cache[uri]
		if !ok {
			d.logger.Debugf(
				"[%v] Connection info added to cache",
				uri.String(),
			)
			var cache connectionInfoCache
			if d.lazyRefresh {
				cache = alloydb.NewLazyRefreshCache(
					uri,
					d.logger,
					d.client, d.key,
					d.refreshTimeout, d.dialerID,
				)
			} else {
				cache = alloydb.NewRefreshAheadCache(
					uri,
					d.logger,
					d.client, d.key,
					d.refreshTimeout, d.dialerID,
				)
			}
			c = monitoredCache{connectionInfoCache: cache}
			d.cache[uri] = c
		}
	}
	return c, nil
}
