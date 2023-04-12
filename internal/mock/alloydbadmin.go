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
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	"cloud.google.com/go/alloydb/apiv1beta/alloydbpb"
	"google.golang.org/protobuf/encoding/protojson"
)

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
	p := fmt.Sprintf("/v1beta/projects/%s/locations/%s/clusters/%s/instances/%s/connectionInfo",
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
// `generateClientCertificate` AlloyDB Admin API endpoint.
func CreateEphemeralSuccess(i FakeAlloyDBInstance, ct int) *Request {
	return &Request{
		reqMethod: http.MethodPost,
		reqPath: fmt.Sprintf(
			"/v1beta/projects/%s/locations/%s/clusters/%s:generateClientCertificate",
			i.project, i.region, i.cluster),
		reqCt: ct,
		handle: func(resp http.ResponseWriter, req *http.Request) {
			// Read the body from the request.
			b, err := io.ReadAll(req.Body)
			defer req.Body.Close()
			if err != nil {
				http.Error(resp, fmt.Errorf("unable to read body: %w", err).Error(), http.StatusBadRequest)
				return
			}
			var rreq alloydbpb.GenerateClientCertificateRequest
			err = protojson.Unmarshal(b, &rreq)
			if err != nil {
				http.Error(resp, fmt.Errorf("invalid or unexpected json: %w", err).Error(), http.StatusBadRequest)
				return
			}
			bl, _ := pem.Decode([]byte(rreq.PemCsr))
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
			if err != nil {
				http.Error(resp, fmt.Errorf("unable to create certificate: %w", err).Error(), http.StatusBadRequest)
				return
			}

			certPEM := &bytes.Buffer{}
			pem.Encode(certPEM, &pem.Block{Type: "CERTIFICATE", Bytes: cert})

			instancePEM := &bytes.Buffer{}
			pem.Encode(instancePEM, &pem.Block{Type: "CERTIFICATE", Bytes: i.intermedCert.Raw})

			caPEM := &bytes.Buffer{}
			pem.Encode(caPEM, &pem.Block{Type: "CERTIFICATE", Bytes: i.rootCACert.Raw})

			rresp := alloydbpb.GenerateClientCertificateResponse{
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
