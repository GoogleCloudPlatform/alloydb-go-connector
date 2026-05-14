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

// Package pgxv5 provides an AlloyDB driver that uses pgx v5 and works with the
// database/sql package.
//
// Deprecated: Use cloud.google.com/go/alloydbconn/driver/postgres instead.
package pgxv5

import (
	"cloud.google.com/go/alloydbconn"
	"cloud.google.com/go/alloydbconn/driver/postgres"
)

// RegisterDriver registers a Postgres driver that uses the alloydbconn.Dialer
// configured with the provided options. The choice of name is entirely up to
// the caller and may be used to distinguish between multiple registrations of
// differently configured Dialers. The driver uses pgx/v5 internally.
// RegisterDriver returns a cleanup function that should be called one the
// database connection is no longer needed.
//
// Deprecated: Use postgres.RegisterDriver instead.
func RegisterDriver(name string, opts ...alloydbconn.Option) (func() error, error) {
	return postgres.RegisterDriver(name, opts...)
}
