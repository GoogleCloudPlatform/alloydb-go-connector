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

// This package is deprecated. Use pgxv5 directly instead.
package pgxv4

import (
	"cloud.google.com/go/alloydbconn"
	"cloud.google.com/go/alloydbconn/driver/pgxv5"
)

// RegisterDriver calls through to pgxv5.RegisterDriver. Prefer the pgxv5
// interface instead of using this one
func RegisterDriver(name string, opts ...alloydbconn.Option) (func() error, error) {
	return pgxv5.RegisterDriver(name, opts...)
}
