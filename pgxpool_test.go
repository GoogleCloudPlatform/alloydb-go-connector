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

// [START alloydb_pgxpool_connect_connector]
import (
	"context"
	"fmt"
	"net"

	"cloud.google.com/go/alloydbconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// connectPgx establishes a connection to your database using pgxpool and the
// AlloyDB Go Connector (aka alloydbconn.Dialer)
//
// The function takes an instance URI, a username, a password, and a database
// name. Usage looks like this:
//
//	pool, cleanup, err := connectPgx(
//	  context.Background(),
//	  "projects/myproject/locations/us-central1/clusters/mycluster/instances/myinstance",
//	  "postgres",
//	  "secretpassword",
//	  "mydb",
//	)
//
// In addition to a *pgxpool.Pool type, the function returns a cleanup function
// that should be called when you're done with the database connection.
func connectPgx(
	ctx context.Context, instURI, user, pass, dbname string,
) (*pgxpool.Pool, func() error, error) {
	// First initialize the dialer. alloydbconn.NewDialer accepts additional
	// options to configure credentials, timeouts, etc.
	//
	// For details, see:
	// https://pkg.go.dev/cloud.google.com/go/alloydbconn#Option
	d, err := alloydbconn.NewDialer(ctx)
	if err != nil {
		noop := func() error { return nil }
		return nil, noop, fmt.Errorf("failed to init Dialer: %v", err)
	}
	// The cleanup function will stop the dialer's background refresh
	// goroutines. Call it when you're done with your database connection to
	// avoid a goroutine leak.
	cleanup := func() error { return d.Close() }

	dsn := fmt.Sprintf(
		// sslmode is disabled, because the Dialer will handle the SSL
		// connection instead.
		"user=%s password=%s dbname=%s sslmode=disable",
		user, pass, dbname,
	)

	// Prefer pgxpool for applications.
	// For more information, see:
	// https://github.com/jackc/pgx/wiki/Getting-started-with-pgx#using-a-connection-pool
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, cleanup, fmt.Errorf("failed to parse pgx config: %v", err)
	}

	// Tell pgx to use alloydbconn.Dialer to connect to the instance.
	config.ConnConfig.DialFunc = func(ctx context.Context, _ string, _ string) (net.Conn, error) {
		return d.Dial(ctx, instURI)
	}

	// Establish the connection.
	pool, connErr := pgxpool.NewWithConfig(ctx, config)
	if connErr != nil {
		return nil, cleanup, fmt.Errorf("failed to connect: %s", connErr)
	}

	return pool, cleanup, nil
}

// [END alloydb_pgxpool_connect_connector]
