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
	"math/big"
	"net"
	"testing"
	"time"
)

// Option configures a FakeAlloyDBInstance
type Option func(*FakeAlloyDBInstance)

// WithIPAddr sets the IP address of the instance.
func WithIPAddr(addr string) Option {
	return func(f *FakeAlloyDBInstance) {
		f.ipAddr = addr
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

	ipAddr     string
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
		ipAddr:     "127.0.0.1",
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
				ServerName: "FIXME", // FIXME: this will become the instance UID
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
