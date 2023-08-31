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

// Package direct provides a driver that connects directly to AlloyDB without
// traversing the server side proxy while also enabling Auto IAM AuthN.
package direct

import (
	"context"
	"database/sql"
	"database/sql/driver"

	"github.com/jackc/pgx/v4"
	"github.com/jackc/pgx/v4/stdlib"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

func init() {
	sql.Register("alloydb-direct", &pgDriver{})
}

type pgDriver struct{}

// TODO: allow callers to configure a token source
func authToken() (*oauth2.Token, error) {
	ts, err := google.DefaultTokenSource(context.Background())
	if err != nil {
		return nil, err
	}
	return ts.Token()
}

func (p *pgDriver) Open(name string) (driver.Conn, error) {
	config, err := pgx.ParseConfig(name)
	if err != nil {
		return nil, err
	}
	tok, err := authToken()
	if err != nil {
		return nil, err
	}
	config.Password = tok.AccessToken

	dbURI := stdlib.RegisterConnConfig(config)
	if err != nil {
		return nil, err
	}
	return stdlib.GetDefaultDriver().Open(dbURI)
}
