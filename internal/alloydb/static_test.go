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
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"testing"

	"cloud.google.com/go/alloydbconn/internal/mock"
)

// writeStaticInfo builds a static connection info document for inst, with the
// given pscInstanceConfig object (which may be nil to omit it entirely).
func writeStaticInfo(
	t *testing.T, inst mock.FakeAlloyDBInstance, pscConfig map[string]any,
) *bytes.Reader {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{
		Type: "RSA PUBLIC KEY", Bytes: x509.MarshalPKCS1PublicKey(&key.PublicKey),
	})
	privPEM := pem.EncodeToMemory(&pem.Block{
		Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	chain, err := inst.GeneratePEMCertificateChain(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	info := map[string]any{
		"ipAddress":           "10.0.0.1",
		"publicIpAddress":     "127.0.0.1",
		"pemCertificateChain": chain,
		"caCert":              chain[len(chain)-1],
	}
	if pscConfig != nil {
		info["pscInstanceConfig"] = pscConfig
	}
	data, err := json.Marshal(map[string]any{
		"publicKey":   string(pubPEM),
		"privateKey":  string(privPEM),
		inst.String(): info,
	})
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(data)
}

func TestStaticConnectionInfoPSCAddrs(t *testing.T) {
	inst := mock.NewFakeInstance(
		"my-project", "my-region", "my-cluster", "my-instance",
	)
	cn, err := ParseInstURI(inst.String())
	if err != nil {
		t.Fatal(err)
	}

	tcs := []struct {
		desc        string
		pscConfig   map[string]any
		wantPSC     string
		wantPSCAuto string
		wantHasAuto bool
	}{
		{
			desc: "manual and automatic PSC DNS names",
			pscConfig: map[string]any{
				"pscDnsName":     "manual.alloydb.goog.",
				"pscAutoDnsName": "auto.alloydb.goog.",
			},
			wantPSC:     "manual.alloydb.goog.",
			wantPSCAuto: "auto.alloydb.goog.",
			wantHasAuto: true,
		},
		{
			desc: "manual PSC DNS name only",
			pscConfig: map[string]any{
				"pscDnsName": "manual.alloydb.goog.",
			},
			wantPSC:     "manual.alloydb.goog.",
			wantHasAuto: false,
		},
		{
			// An explicitly empty automatic name must not register as a
			// fallback address, or the Dialer would attempt to dial "".
			desc: "empty automatic PSC DNS name is not recorded",
			pscConfig: map[string]any{
				"pscDnsName":     "manual.alloydb.goog.",
				"pscAutoDnsName": "",
			},
			wantPSC:     "manual.alloydb.goog.",
			wantHasAuto: false,
		},
		{
			desc:        "no pscInstanceConfig",
			pscConfig:   nil,
			wantPSC:     "",
			wantHasAuto: false,
		},
	}

	for _, tc := range tcs {
		t.Run(tc.desc, func(t *testing.T) {
			r := writeStaticInfo(t, inst, tc.pscConfig)
			cache, err := NewStaticConnectionInfoCache(cn, nullLogger{}, r)
			if err != nil {
				t.Fatalf("NewStaticConnectionInfoCache: %v", err)
			}
			ci, err := cache.ConnectionInfo(context.Background())
			if err != nil {
				t.Fatalf("ConnectionInfo: %v", err)
			}
			if got := ci.IPAddrs[PSC]; got != tc.wantPSC {
				t.Errorf("IPAddrs[PSC] = %q, want %q", got, tc.wantPSC)
			}
			got, ok := ci.IPAddrs[PSCAuto]
			if ok != tc.wantHasAuto {
				t.Errorf("IPAddrs[PSCAuto] present = %v, want %v", ok, tc.wantHasAuto)
			}
			if got != tc.wantPSCAuto {
				t.Errorf("IPAddrs[PSCAuto] = %q, want %q", got, tc.wantPSCAuto)
			}
		})
	}
}
