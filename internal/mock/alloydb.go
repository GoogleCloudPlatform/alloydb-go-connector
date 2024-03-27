// Copyright 2023 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package mock

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"fmt"
	"math/big"
	"net"
	"testing"
	"time"

	"cloud.google.com/go/alloydb/connectors/apiv1alpha/connectorspb"
	"google.golang.org/protobuf/proto"
)

// Option configures a FakeAlloyDBInstance
type Option func(*FakeAlloyDBInstance)

// WithPublicIP sets the public IP address to addr.
func WithPublicIP(addr string) Option {
	return func(f *FakeAlloyDBInstance) {
		f.ipAddrs["PUBLIC"] = addr
	}
}

// WithPrivateIP sets the private IP address to addr.
func WithPrivateIP(addr string) Option {
	return func(f *FakeAlloyDBInstance) {
		f.ipAddrs["PRIVATE"] = addr
	}
}

// WithPSC sets the PSC address to addr.
func WithPSC(addr string) Option {
	return func(f *FakeAlloyDBInstance) {
		f.ipAddrs["PSC"] = addr
	}
}

// WithServerName sets the name that server uses to identify itself in the TLS
// handshake.
func WithServerName(name string) Option {
	return func(f *FakeAlloyDBInstance) {
		f.serverName = name
	}
}

// WithCertExpiry sets the expiration time of the fake instance
func WithCertExpiry(expiry time.Time) Option {
	return func(f *FakeAlloyDBInstance) {
		f.certExpiry = expiry
	}
}

// FakeAlloyDBInstance represents the server side proxy.
type FakeAlloyDBInstance struct {
	project string
	region  string
	cluster string
	name    string
	// ipAddrs is a map of IP type (PUBLIC or PRIVATE) to IP address.
	ipAddrs    map[string]string
	uid        string
	serverName string
	certExpiry time.Time

	rootCACert *x509.Certificate
	rootKey    *rsa.PrivateKey

	intermedCert *x509.Certificate
	intermedKey  *rsa.PrivateKey

	serverCert *x509.Certificate
	serverKey  *rsa.PrivateKey
}

func mustGenerateKey() *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return key
}

var (
	rootCAKey     = mustGenerateKey()
	intermedCAKey = mustGenerateKey()
	serverKey     = mustGenerateKey()
)

// NewFakeInstance creates a Fake AlloyDB instance.
func NewFakeInstance(proj, reg, clust, name string, opts ...Option) FakeAlloyDBInstance {
	f := FakeAlloyDBInstance{
		project:    proj,
		region:     reg,
		cluster:    clust,
		name:       name,
		ipAddrs:    map[string]string{"PRIVATE": "127.0.0.1"},
		uid:        "00000000-0000-0000-0000-000000000000",
		serverName: "00000000-0000-0000-0000-000000000000.server.alloydb",
		certExpiry: time.Now().Add(24 * time.Hour),
	}

	for _, o := range opts {
		o(&f)
	}

	rootTemplate := &x509.Certificate{
		SerialNumber: &big.Int{},
		Subject: pkix.Name{
			CommonName: "root.alloydb",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(0, 0, 1),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}

	// create a self-signed root certificate
	signedRoot, err := x509.CreateCertificate(
		rand.Reader, rootTemplate, rootTemplate, &rootCAKey.PublicKey, rootCAKey)
	if err != nil {
		panic(err)
	}
	rootCert, err := x509.ParseCertificate(signedRoot)
	if err != nil {
		panic(err)
	}
	// create an intermediate CA, signed by the root
	// This CA signs all client certs.
	intermedTemplate := &x509.Certificate{
		SerialNumber: &big.Int{},
		Subject: pkix.Name{
			CommonName: "client.alloydb",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(0, 0, 1),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}
	signedIntermed, err := x509.CreateCertificate(
		rand.Reader, intermedTemplate, rootCert, &intermedCAKey.PublicKey, rootCAKey)
	if err != nil {
		panic(err)
	}
	intermedCert, err := x509.ParseCertificate(signedIntermed)
	if err != nil {
		panic(err)
	}
	// create a server certificate, signed by the root
	// This is what the server side proxy uses.
	serverTemplate := &x509.Certificate{
		SerialNumber: &big.Int{},
		Subject: pkix.Name{
			CommonName: f.serverName,
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(0, 0, 1),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	signedServer, err := x509.CreateCertificate(
		rand.Reader, serverTemplate, rootCert, &serverKey.PublicKey, rootCAKey)
	if err != nil {
		panic(err)
	}
	serverCert, err := x509.ParseCertificate(signedServer)
	if err != nil {
		panic(err)
	}

	// save all TLS certificates for later use.
	f.rootCACert = rootCert
	f.rootKey = rootCAKey
	f.intermedCert = intermedCert
	f.intermedKey = intermedCAKey
	f.serverCert = serverCert
	f.serverKey = serverKey

	return f
}

// StartServerProxy starts a fake server proxy and listens on the provided port
// on all interfaces, configured with TLS as specified by the
// FakeAlloyDBInstance. Callers should invoke the returned function to clean up
// all resources.
func StartServerProxy(t *testing.T, inst FakeAlloyDBInstance) func() {
	pool := x509.NewCertPool()
	pool.AddCert(inst.rootCACert)
	tryListen := func(t *testing.T, attempts int) net.Listener {
		var (
			ln  net.Listener
			err error
		)
		for i := 0; i < attempts; i++ {
			ln, err = tls.Listen("tcp", ":5433", &tls.Config{
				Certificates: []tls.Certificate{
					{
						Certificate: [][]byte{inst.serverCert.Raw, inst.rootCACert.Raw},
						PrivateKey:  inst.serverKey,
						Leaf:        inst.serverCert,
					},
				},
				ServerName: "127.0.0.1",
				ClientAuth: tls.RequireAndVerifyClientCert,
				ClientCAs:  pool,
			})
			if err != nil {
				t.Log("listener failed to start, waiting 100ms")
				time.Sleep(500 * time.Millisecond)
				continue
			}
			return ln
		}
		t.Fatalf("failed to start listener: %v", err)
		return nil
	}
	ln := tryListen(t, 10)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				if err := metadataExchange(conn); err != nil {
					conn.Close()
					return
				}

				// Database protocol takes over from here.
				conn.Write([]byte(inst.name))
				conn.Close()
			}
		}
	}()
	return func() {
		cancel()
		ln.Close()
	}
}

// metadataExchange mimics server side behavior in four steps:
//
//  1. Read a big endian uint32 (4 bytes) from the client. This is the number of
//     bytes the message consumes. The length does not include the initial four
//     bytes.
//
//  2. Read the message from the client using the message length and unmarshal
//     it into a MetadataExchangeResponse message.
//
// The real server implementation will then validate the client has connection
// permissions using the provided OAuth2 token based on the auth type. Here in
// the test implementation, the server does nothing.
//
//  3. Prepare a response and write the size of the response as a uint32 (4
//     bytes)
//
// 4. Marshal the response to bytes and write those to the client as well.
//
// Subsequent interactions with the test server use the database protocol.
func metadataExchange(conn net.Conn) error {
	msgSize := make([]byte, 4)
	n, err := conn.Read(msgSize)
	if err != nil {
		return err
	}
	if n != 4 {
		return fmt.Errorf("read %d bytes, want = 4", n)
	}

	size := binary.BigEndian.Uint32(msgSize)
	buf := make([]byte, size)
	n, err = conn.Read(buf)
	if err != nil {
		return err
	}
	if n != int(size) {
		return fmt.Errorf("read %d bytes, want = %d", n, size)
	}

	m := &connectorspb.MetadataExchangeRequest{}
	err = proto.Unmarshal(buf, m)
	if err != nil {
		return err
	}

	resp := &connectorspb.MetadataExchangeResponse{
		ResponseCode: connectorspb.MetadataExchangeResponse_OK,
	}
	data, err := proto.Marshal(resp)
	if err != nil {
		return err
	}
	respSize := proto.Size(resp)
	buf = make([]byte, 4)
	binary.BigEndian.PutUint32(buf, uint32(respSize))

	buf = append(buf, data...)
	n, err = conn.Write(buf)
	if err != nil {
		return err
	}
	if n != len(buf) {
		return fmt.Errorf("write %d bytes, want = %d", n, len(buf))
	}
	return nil
}
