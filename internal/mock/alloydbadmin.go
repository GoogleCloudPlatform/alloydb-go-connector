// Copyright 2022 Google LLC
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
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/alloydbconn/internal/alloydbapi"
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
func NewFakeInstance(proj, reg, clust, name string, _ ...Option) FakeAlloyDBInstance {
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

func (f FakeAlloyDBInstance) clientCert() *x509.Certificate {
	return &x509.Certificate{
		SerialNumber: &big.Int{},
		Subject: pkix.Name{
			CommonName: "alloydb-client",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(0, 0, 1),
		BasicConstraintsValid: true,
	}
}

// Request represents a HTTP request for a test Server to mock responses for.
//
// Use NewRequest to initialize new Requests.
type Request struct {
	sync.Mutex

	reqMethod string
	reqPath   string
	reqCt     int

	handle func(resp http.ResponseWriter, req *http.Request)
}

// matches returns true if a given http.Request should be handled by this Request.
func (r *Request) matches(hR *http.Request) bool {
	r.Lock()
	defer r.Unlock()
	if r.reqMethod != "" && r.reqMethod != hR.Method {
		return false
	}
	if r.reqPath != "" && r.reqPath != hR.URL.Path {
		return false
	}
	if r.reqCt <= 0 {
		return false
	}
	r.reqCt--
	return true
}

// InstanceGetSuccess returns a Request that responds to the `instance.get`
// AlloyDB Admin API endpoint.
func InstanceGetSuccess(i FakeAlloyDBInstance, ct int) *Request {
	p := fmt.Sprintf("/projects/%s/locations/%s/clusters/%s/instances/%s/connectionInfo",
		i.project, i.region, i.cluster, i.name)
	return &Request{
		reqMethod: http.MethodGet,
		reqPath:   p,
		reqCt:     ct,
		handle: func(resp http.ResponseWriter, req *http.Request) {
			resp.WriteHeader(http.StatusOK)
			resp.Write([]byte(fmt.Sprintf(`{"ipAddress":"%s","instanceUid":"%s"}`, i.ipAddr, i.uid)))
		},
	}
}

// CreateEphemeralSuccess returns a Request that responds to the
// `generateEphemeralCert` AlloyDB Admin API endpoint.
func CreateEphemeralSuccess(i FakeAlloyDBInstance, ct int) *Request {
	return &Request{
		reqMethod: http.MethodPost,
		reqPath: fmt.Sprintf(
			"/projects/%s/locations/%s/clusters/%s:generateClientCertificate",
			i.project, i.region, i.cluster),
		reqCt: ct,
		handle: func(resp http.ResponseWriter, req *http.Request) {
			// Read the body from the request.
			b, err := ioutil.ReadAll(req.Body)
			defer req.Body.Close()
			if err != nil {
				http.Error(resp, fmt.Errorf("unable to read body: %w", err).Error(), http.StatusBadRequest)
				return
			}
			var rreq alloydbapi.GenerateClientCertificateRequest
			err = json.Unmarshal(b, &rreq)
			if err != nil {
				http.Error(resp, fmt.Errorf("invalid or unexpected json: %w", err).Error(), http.StatusBadRequest)
				return
			}
			bl, _ := pem.Decode([]byte(rreq.PemCSR))
			if bl == nil {
				http.Error(resp, fmt.Errorf("unable to decode CSR: %w", err).Error(), http.StatusBadRequest)
				return
			}
			csr, err := x509.ParseCertificateRequest(bl.Bytes)
			if err != nil {
				http.Error(resp, fmt.Errorf("unable to decode CSR: %w", err).Error(), http.StatusBadRequest)
				return
			}

			template := &x509.Certificate{
				Signature:          csr.Signature,
				SignatureAlgorithm: csr.SignatureAlgorithm,
				PublicKeyAlgorithm: csr.PublicKeyAlgorithm,
				PublicKey:          csr.PublicKey,
				SerialNumber:       &big.Int{},
				Issuer:             i.intermedCert.Subject,
				Subject:            csr.Subject,
				NotBefore:          time.Now(),
				NotAfter:           i.certExpiry,
				KeyUsage:           x509.KeyUsageDigitalSignature,
				ExtKeyUsage:        []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
			}

			cert, err := x509.CreateCertificate(
				rand.Reader, template, i.intermedCert, template.PublicKey, i.intermedKey)

			certPEM := &bytes.Buffer{}
			pem.Encode(certPEM, &pem.Block{Type: "CERTIFICATE", Bytes: cert})

			instancePEM := &bytes.Buffer{}
			pem.Encode(instancePEM, &pem.Block{Type: "CERTIFICATE", Bytes: i.intermedCert.Raw})

			caPEM := &bytes.Buffer{}
			pem.Encode(caPEM, &pem.Block{Type: "CERTIFICATE", Bytes: i.rootCACert.Raw})

			rresp := alloydbapi.GenerateClientCertificateResponse{
				PemCertificate:      certPEM.String(),
				PemCertificateChain: []string{instancePEM.String(), caPEM.String()},
			}
			if err := json.NewEncoder(resp).Encode(&rresp); err != nil {
				http.Error(resp, fmt.Errorf("unable to encode response: %w", err).Error(), http.StatusBadRequest)
				return
			}
		},
	}
}

// HTTPClient returns an *http.Client, URL, and cleanup function. The http.Client is
// configured to connect to test SSL Server at the returned URL. This server will
// respond to HTTP requests defined, or return a 5xx server error for unexpected ones.
// The cleanup function will close the server, and return an error if any expected calls
// weren't received.
func HTTPClient(requests ...*Request) (*http.Client, string, func() error) {
	// Create a TLS Server that responses to the requests defined
	s := httptest.NewTLSServer(http.HandlerFunc(
		func(resp http.ResponseWriter, req *http.Request) {
			for _, r := range requests {
				if r.matches(req) {
					r.handle(resp, req)
					return
				}
			}

			// Unexpected requests should throw an error
			resp.WriteHeader(http.StatusNotImplemented)
			// TODO: follow error format better?
			resp.Write([]byte(fmt.Sprintf("unexpected request sent to mock client: %v", req)))
		},
	))
	// cleanup stops the test server and checks for uncalled requests
	cleanup := func() error {
		s.Close()
		for i, e := range requests {
			if e.reqCt > 0 {
				return fmt.Errorf("%d calls left for specified call in pos %d: %v", e.reqCt, i, e)
			}
		}
		return nil
	}

	return s.Client(), s.URL, cleanup

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
					tls.Certificate{
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
