// Copyright 2023 Google LLC
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

package alloydbconn_test

// [START alloydb_databasesql_connect_connector]
import (
	"database/sql"
	"fmt"

	"cloud.google.com/go/alloydbconn/driver/pgxv4"
)

// connectDatabaseSQL establishes a connection to your database using the Go
// standard library's database/sql package.
//
// The function takes an instance URI, a username, a password, and a database
// name. Usage looks like this:
//
//	db, cleanup, err := connectDatabaseSQL(
//	  "projects/myproject/locations/us-central1/clusters/mycluster/instances/myinstance",
//	  "postgres",
//	  "secretpassword",
//	  "mydb",
//	)
//
// In addition to a *db.SQL type, the function returns a cleanup function that
// should be called when you're done with the database connection.
func connectDatabaseSQL(
	instURI, user, pass, dbname string,
) (*sql.DB, func() error, error) {
	// First, register the AlloyDB driver. Note, the driver's name is arbitrary
	// and must only match what you use below in sql.Open. Also,
	// pgxv4.RegisterDriver accepts options to configure credentials, timeouts,
	// etc.
	//
	// For details, see:
	// https://pkg.go.dev/cloud.google.com/go/alloydbconn#Option
	//
	// The cleanup function will stop the dialer's background refresh
	// goroutines. Call it when you're done with your database connection to
	// avoid a goroutine leak.
	cleanup, err := pgxv4.RegisterDriver("alloydb")
	if err != nil {
		return nil, cleanup, err
	}

	db, err := sql.Open(
		"alloydb",
		fmt.Sprintf(
			// sslmode is disabled, because the Dialer will handle the SSL
			// connection instead.
			"host=%s user=%s password=%s dbname=%s sslmode=disable",
			instURI, user, pass, dbname,
		),
	)
	return db, cleanup, err
}

// [END alloydb_databasesql_connect_connector]
