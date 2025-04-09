// Copyright 2025 Google LLC
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
	"net/http"
	"testing"
)

func TestClientOptions(t *testing.T) {
	tcs := []struct {
		desc                     string
		opt                      Option
		wantClientOptsLen        int
		wantAlloyDBClientOptsLen int
	}{
		{
			desc:                     "WithAdminAPIEndpoint",
			opt:                      WithAdminAPIEndpoint("some-endpoint"),
			wantAlloyDBClientOptsLen: 1,
			wantClientOptsLen:        0,
		},
		{
			desc:                     "WithHTTPClient",
			opt:                      WithHTTPClient(http.DefaultClient),
			wantAlloyDBClientOptsLen: 0,
			wantClientOptsLen:        1,
		},
	}

	for _, tc := range tcs {
		t.Run(tc.desc, func(t *testing.T) {
			d := &dialerConfig{}
			tc.opt(d)

			if got, want := len(d.clientOpts), tc.wantClientOptsLen; got != want {
				t.Errorf("clientOpts: got = %v, want = %v", got, want)
			}
			if got, want := len(d.alloydbClientOpts), tc.wantAlloyDBClientOptsLen; got != want {
				t.Errorf("alloydbClientOpts: got = %v, want = %v", got, want)
			}
		})
	}
}
