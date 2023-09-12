// Copyright 2020 Google LLC

// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at

//     https://www.apache.org/licenses/LICENSE-2.0

// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package alloydb

import (
	"context"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"

	alloydbadmin "cloud.google.com/go/alloydb/apiv1beta"
	"cloud.google.com/go/alloydb/apiv1beta/alloydbpb"
	"cloud.google.com/go/alloydbconn/errtype"
	"cloud.google.com/go/alloydbconn/internal/trace"
	"google.golang.org/protobuf/types/known/durationpb"
)

type connectInfo struct {
	// ipAddr is the instance's IP addresses
	ipAddr string
	// uid is the instance UID
	uid string
}

// fetchMetadata uses the AlloyDB Admin APIs get method to retrieve the
// information about an AlloyDB instance that is used to create secure
// connections.
func fetchMetadata(ctx context.Context, cl *alloydbadmin.AlloyDBAdminClient, inst InstanceURI) (i connectInfo, err error) {
	var end trace.EndSpanFunc
	ctx, end = trace.StartSpan(ctx, "cloud.google.com/go/alloydbconn/internal.FetchMetadata")
	defer func() { end(err) }()
	req := &alloydbpb.GetConnectionInfoRequest{
		Parent: fmt.Sprintf(
			"projects/%s/locations/%s/clusters/%s/instances/%s", inst.project, inst.region, inst.cluster, inst.name,
		),
	}
	resp, err := cl.GetConnectionInfo(ctx, req)
	if err != nil {
		return connectInfo{}, errtype.NewRefreshError("failed to get instance metadata", inst.String(), err)
	}
	return connectInfo{ipAddr: resp.IpAddress, uid: resp.InstanceUid}, nil
}

var errInvalidPEM = errors.New("certificate is not a valid PEM")

func parseCert(cert string) (*x509.Certificate, error) {
	b, _ := pem.Decode([]byte(cert))
	if b == nil {
		return nil, errInvalidPEM
	}
	return x509.ParseCertificate(b.Bytes)
}

// fetchEphemeralCert uses the AlloyDB Admin API's generateClientCertificate
// method to create a signed TLS certificate that authorized to connect via the
// AlloyDB instance's serverside proxy. The cert is valid for one hour.
func fetchEphemeralCert(
	ctx context.Context,
	cl *alloydbadmin.AlloyDBAdminClient,
	inst InstanceURI,
	key *rsa.PrivateKey,
) (cc *certs, err error) {
	var end trace.EndSpanFunc
	ctx, end = trace.StartSpan(ctx, "cloud.google.com/go/alloydbconn/internal.FetchEphemeralCert")
	defer func() { end(err) }()

	req := &alloydbpb.GenerateClientCertificateRequest{
		Parent: fmt.Sprintf(
			"projects/%s/locations/%s/clusters/%s", inst.project, inst.region, inst.cluster,
		),
		PublicKey: key.N.String(),
		CertDuration: durationpb.New(time.Second * 3600),
	}
	resp, err := cl.GenerateClientCertificate(ctx, req)
	if err != nil {
		return nil, errtype.NewRefreshError(
			"create ephemeral cert failed",
			inst.String(),
			err,
		)
	}

	certChainPEM := append([]string{resp.PemCertificate}, resp.PemCertificateChain...)
	certPEMBlock := []byte(strings.Join(certChainPEM, "\n"))
	keyPEMBlock := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}

	cert, err := tls.X509KeyPair(certPEMBlock, pem.EncodeToMemory(keyPEMBlock))
	if err != nil {
		return nil, errtype.NewRefreshError(
			"create ephemeral cert failed",
			inst.String(),
			err,
		)
	}

	caCertPEMBlock, _ := pem.Decode([]byte(resp.CaCert))
	if caCertPEMBlock == nil {
		return nil, errtype.NewRefreshError(
			"create ephemeral cert failed",
			inst.String(),
			errors.New("no PEM data found in the ca cert"),
		)
	}
	caCert, err := x509.ParseCertificate(caCertPEMBlock.Bytes)
	if err != nil {
		return nil, errtype.NewRefreshError(
			"create ephemeral cert failed",
			inst.String(),
			err,
		)
	}

	// Extract expiry
	clientCertPEMBlock, _ := pem.Decode([]byte(certChainPEM[0]))
	if clientCertPEMBlock == nil {
		return nil, errtype.NewRefreshError(
			"create ephemeral cert failed",
			inst.String(),
			errors.New("no PEM data found in the client cert"),
		)
	}
	clientCert, err := x509.ParseCertificate(clientCertPEMBlock.Bytes)
	if err != nil {
		return nil, errtype.NewRefreshError(
			"create ephemeral cert failed",
			inst.String(),
			err,
		)
	}

	return &certs{
		certChain: cert,
		caCert:    caCert,
		expiry:    clientCert.NotAfter,
	}, nil
}

// newRefresher creates a Refresher.
func newRefresher(
	client *alloydbadmin.AlloyDBAdminClient,
	dialerID string,
) refresher {
	return refresher{
		client:   client,
		dialerID: dialerID,
	}
}

// refresher manages the AlloyDB Admin API access to instance metadata and to
// ephemeral certificates.
type refresher struct {
	// client provides access to the AlloyDB Admin API
	client *alloydbadmin.AlloyDBAdminClient

	// dialerID is the unique ID of the associated dialer.
	dialerID string
}

type refreshResult struct {
	instanceIPAddr string
	conf           *tls.Config
	expiry         time.Time
}

type certs struct {
	certChain tls.Certificate   // TLS client certificate
	caCert    *x509.Certificate // CA certificate
	expiry    time.Time
}

func (r refresher) performRefresh(ctx context.Context, cn InstanceURI, k *rsa.PrivateKey) (res refreshResult, err error) {
	var refreshEnd trace.EndSpanFunc
	ctx, refreshEnd = trace.StartSpan(ctx, "cloud.google.com/go/alloydbconn/internal.RefreshConnection",
		trace.AddInstanceName(cn.String()),
	)
	defer func() {
		go trace.RecordRefreshResult(context.Background(), cn.String(), r.dialerID, err)
		refreshEnd(err)
	}()

	type mdRes struct {
		info connectInfo
		err  error
	}
	mdCh := make(chan mdRes, 1)
	go func() {
		defer close(mdCh)
		c, err := fetchMetadata(ctx, r.client, cn)
		mdCh <- mdRes{info: c, err: err}
	}()

	type certRes struct {
		cc  *certs
		err error
	}
	certCh := make(chan certRes, 1)
	go func() {
		defer close(certCh)
		cc, err := fetchEphemeralCert(ctx, r.client, cn, k)
		certCh <- certRes{cc: cc, err: err}
	}()

	var info connectInfo
	select {
	case r := <-mdCh:
		if r.err != nil {
			return refreshResult{}, fmt.Errorf("failed to get instance IP address: %w", r.err)
		}
		info = r.info
	case <-ctx.Done():
		return refreshResult{}, fmt.Errorf("refresh failed: %w", ctx.Err())
	}

	var cc *certs
	select {
	case r := <-certCh:
		if r.err != nil {
			return refreshResult{}, fmt.Errorf("fetch ephemeral cert failed: %w", r.err)
		}
		cc = r.cc
	case <-ctx.Done():
		return refreshResult{}, fmt.Errorf("refresh failed: %w", ctx.Err())
	}

	caCerts := x509.NewCertPool()
	caCerts.AddCert(cc.caCert)
	c := &tls.Config{
		Certificates: []tls.Certificate{cc.certChain},
		RootCAs:      caCerts,
		ServerName:   info.ipAddr,
		MinVersion:   tls.VersionTLS13,
	}

	return refreshResult{instanceIPAddr: info.ipAddr, conf: c, expiry: cc.expiry}, nil
}
