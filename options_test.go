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
	"testing"

	"cloud.google.com/go/auth"
	"golang.org/x/oauth2"
)

type nullTokenSource struct{}

func (nullTokenSource) Token() (*oauth2.Token, error) {
	return nil, nil
}

func TestNewDialerConfig_IncompatibleOptions(t *testing.T) {
	tcs := []struct {
		desc string
		opts []Option
	}{
		{
			desc: "WithOptOutOfAdvancedConnectionCheck and WithIAMAuthN",
			opts: []Option{WithOptOutOfAdvancedConnectionCheck(), WithIAMAuthN()},
		},
		{
			desc: "WithCredentialsFile and WithCredentialsJSON",
			opts: []Option{WithCredentialsFile("/some/file"), WithCredentialsJSON(nil)},
		},
		{
			desc: "WithCredentialsFile and WithTokenSource",
			opts: []Option{WithCredentialsFile("/some/file"), WithTokenSource(nullTokenSource{})},
		},
		{
			desc: "WithCredentialsJSON and WithTokenSource",
			opts: []Option{WithCredentialsJSON([]byte(`sample-json`)), WithTokenSource(nullTokenSource{})},
		},
		{
			desc: "WithCredentials and WihtCredentialsJSON",
			opts: []Option{WithCredentials(&auth.Credentials{}), WithCredentialsJSON([]byte(`sample-json`))},
		},
		{
			desc: "WithCredentials and WihtCredentialsFile",
			opts: []Option{WithCredentials(&auth.Credentials{}), WithCredentialsFile("/some/file")},
		},
		{
			desc: "WithCredentials and WihtTokenSource",
			opts: []Option{WithCredentials(&auth.Credentials{}), WithTokenSource(nullTokenSource{})},
		},
	}

	for _, tc := range tcs {
		t.Run(tc.desc, func(t *testing.T) {
			_, err := newDialerConfig(tc.opts...)
			if err == nil {
				t.Fatal("expected an error, but got nil")
			}
		})
	}
}
