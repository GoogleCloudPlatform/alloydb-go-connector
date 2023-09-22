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
package alloydbconn_test

// [START alloydb_databasesql_connect_iam_authn_direct]
import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"

	"github.com/jackc/pgx/v4"
	"github.com/jackc/pgx/v4/stdlib"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// Register the driver as "alloydb-direct" to use the customer alloydbDirect.
func init() {
	sql.Register("alloydb-direct", &alloydbDirect{})
}

// alloydbDirect demonstrates how to implement the database/sql/driver.Driver
// interface to enable Auto IAM AuthN without using the Go Connector. Most
// users will want to copy this driver code and adjust its token generation to
// suit their needs. As written the driver uses Application Default Credentials
// which will work for ~80% of use cases.
type alloydbDirect struct{}

func authToken() (*oauth2.Token, error) {
	ts, err := google.DefaultTokenSource(context.Background(),
		"https://www.googleapis.com/auth/cloud-platform",
	)
	if err != nil {
		return nil, err
	}
	return ts.Token()
}

func (p *alloydbDirect) Open(name string) (driver.Conn, error) {
	config, err := pgx.ParseConfig(name)
	if err != nil {
		return nil, err
	}
	// Fetch the auth token and update the configuration's password before
	// attempting to connect. This ensures all connections will use a fresh
	// OAuth2 token.
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

// connectDirectDatabaseSQLAutoIAMAuthN establishes a connection to your
// database using database/sql with a small wrapper driver that provides a
// fresh OAuth2 token for every new connection. This approach enables you to
// connect to your AlloyDB instance directly without the AlloyDB Go Connector
// while still using Auto IAM Authentication.
//
// The function takes the revelent instance IP, a username, and a database
// name. Usage looks like this:
//
//	  db, err := connectDirectDatabaseSQLAutoIAMAuthN(
//		   "10.0.0.1",             // whatever your instance IP is
//		   "my-sa@my-project.iam", // whatever IAM user you're running as
//		   "mydb",                 // whatever database you want to connect to
//	  )
//
// Because this connection uses an OAuth2 token as a password, you must require
// SSL, or better, enforce all clients speak SSL on the server side. This
// ensures the OAuth2 token is not inadvertantly leaked.
func connectDirectDatabaseSQLAutoIAMAuthN(
	instIP, user, dbname string,
) (*sql.DB, error) {
	dsn := fmt.Sprintf(
		"host=%v user=%v dbname=%v sslmode=require",
		instIP, user, dbname,
	)

	return sql.Open("alloydb-direct", dsn)
}

// [END alloydb_databasesql_connect_iam_authn_direct]
