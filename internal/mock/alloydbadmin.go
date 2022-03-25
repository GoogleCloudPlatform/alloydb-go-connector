package mock

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	"cloud.google.com/go/cloudsqlconn/internal/alloydb"
)

type Option func(*FakeAlloyDBInstance)

func WithIPAddr(addr string) Option {
	return func(f *FakeAlloyDBInstance) {
		f.ipAddr = addr
	}
}

func WithCertExpiry(expiry time.Time) Option {
	return func(f *FakeAlloyDBInstance) {
		f.certExpiry = expiry
	}
}

type FakeAlloyDBInstance struct {
	project string
	region  string
	cluster string
	name    string

	ipAddr     string
	certExpiry time.Time

	rootCACert *x509.Certificate
	rootKey    *rsa.PrivateKey

	instanceCert *x509.Certificate
	instanceKey  *rsa.PrivateKey
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
	instanceCAKey = mustGenerateKey()
)

func NewFakeInstance(proj, reg, clust, name string, opts ...Option) FakeAlloyDBInstance {
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
	instanceTemplate := &x509.Certificate{
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
	signedInstance, err := x509.CreateCertificate(
		rand.Reader, instanceTemplate, rootCert, &instanceCAKey.PublicKey, rootCAKey)
	if err != nil {
		panic(err)
	}
	instanceCert, err := x509.ParseCertificate(signedInstance)
	if err != nil {
		panic(err)
	}

	f := FakeAlloyDBInstance{
		project:      proj,
		region:       reg,
		cluster:      clust,
		name:         name,
		rootCACert:   rootCert,
		rootKey:      rootCAKey,
		instanceCert: instanceCert,
		instanceKey:  instanceCAKey,
	}

	for _, o := range opts {
		o(&f)
	}
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
	instanceName := fmt.Sprintf(
		"projects/%s/locations/%s/clusters/%s/instances/%s",
		i.project, i.region, i.cluster, i.name,
	)
	p := fmt.Sprintf("/v1alpha1/projects/%s/locations/%s/clusters/%s/instances/%s",
		i.project, i.region, i.cluster, i.name)
	return &Request{
		reqMethod: http.MethodGet,
		reqPath:   p,
		reqCt:     ct,
		handle: func(resp http.ResponseWriter, req *http.Request) {
			resp.WriteHeader(http.StatusOK)
			resp.Write([]byte(fmt.Sprintf(`{"name":"%s","ipAddress":"%s"}`, instanceName, i.ipAddr)))
		},
	}
}

// CreateEphemeralSuccess returns a Request that responds to the
// `generateEphemeralCert` AlloyDB Admin API endpoint.
func CreateEphemeralSuccess(i FakeAlloyDBInstance, ct int) *Request {
	return &Request{
		reqMethod: http.MethodPost,
		reqPath: fmt.Sprintf(
			"/v1alpha1/projects/%s/locations/%s/clusters/%s:generateClientCertificate",
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
			var rreq alloydb.GenerateClientCertificateRequest
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
				Issuer:             i.instanceCert.Subject,
				Subject:            csr.Subject,
				NotBefore:          time.Now(),
				NotAfter:           i.certExpiry,
				KeyUsage:           x509.KeyUsageDigitalSignature,
				ExtKeyUsage:        []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
			}

			cert, err := x509.CreateCertificate(
				rand.Reader, template, i.instanceCert, template.PublicKey, i.instanceKey)

			certPEM := &bytes.Buffer{}
			pem.Encode(certPEM, &pem.Block{Type: "CERTIFICATE", Bytes: cert})

			instancePEM := &bytes.Buffer{}
			pem.Encode(instancePEM, &pem.Block{Type: "CERTIFICATE", Bytes: i.instanceCert.Raw})

			caPEM := &bytes.Buffer{}
			pem.Encode(caPEM, &pem.Block{Type: "CERTIFICATE", Bytes: i.rootCACert.Raw})

			rresp := alloydb.GenerateClientCertificateResponse{
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
