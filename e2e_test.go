// Copyright 2020 Google LLC
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

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"cloud.google.com/go/alloydbconn"
	"cloud.google.com/go/alloydbconn/driver/pgxv4"
	"github.com/jackc/pgx/v4"
)

var (
	// AlloyDB instance URI, in the form of
	// /projects/PROJECT_ID/locations/REGION_ID/clusters/CLUSTER_ID/instances/INSTANCE_ID
	alloydbURI = os.Getenv("ALLOYDB_URI")
	// Name of database user.
	alloydbUser = os.Getenv("ALLOYDB_USER")
	// Password for the database user; be careful when entering a password on the
	// command line (it may go into your terminal's history).
	alloydbPass = os.Getenv("ALLOYDB_PASS")
	// Name of the database to connect to.
	alloydbDB = os.Getenv("ALLOYDB_DB")
)

func requireAlloyDBVars(t *testing.T) {
	switch "" {
	case alloydbURI:
		t.Fatal("'ALLOYDB_URI' env var not set")
	case alloydbUser:
		t.Fatal("'ALLOYDB_USER' env var not set")
	case alloydbPass:
		t.Fatal("'ALLOYDB_PASS' env var not set")
	case alloydbDB:
		t.Fatal("'ALLOYDB_DB' env var not set")
	}
}

func TestPgxConnect(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration tests")
	}
	requireAlloyDBVars(t)

	ctx := context.Background()

	d, err := alloydbconn.NewDialer(ctx)
	if err != nil {
		t.Fatalf("failed to init Dialer: %v", err)
	}

	dsn := fmt.Sprintf("user=%s password=%s dbname=%s sslmode=disable", alloydbUser, alloydbPass, alloydbDB)
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("failed to parse pgx config: %v", err)
	}

	config.DialFunc = func(ctx context.Context, network string, instance string) (net.Conn, error) {
		return d.Dial(ctx, alloydbURI)
	}

	conn, connErr := pgx.ConnectConfig(ctx, config)
	if connErr != nil {
		t.Fatalf("failed to connect: %s", connErr)
	}
	defer conn.Close(ctx)

	var now time.Time
	err = conn.QueryRow(context.Background(), "SELECT NOW()").Scan(&now)
	if err != nil {
		t.Fatalf("QueryRow failed: %s", err)
	}
	t.Log(now)
}

func TestAlloyDBHook(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration tests")
	}
	testConn := func(db *sql.DB) {
		var now time.Time
		if err := db.QueryRow("SELECT NOW()").Scan(&now); err != nil {
			t.Fatalf("QueryRow failed: %v", err)
		}
		t.Log(now)
	}
	cleanup, err := pgxv4.RegisterDriver("alloydb")
	if err != nil {
		t.Fatalf("failed to register driver: %v", err)
	}
	defer cleanup()
	db, err := sql.Open(
		"alloydb",
		fmt.Sprintf("host=%s user=%s password=%s dbname=%s sslmode=disable",
			alloydbURI, alloydbUser, alloydbPass, alloydbDB),
	)
	if err != nil {
		t.Fatalf("sql.Open want err = nil, got = %v", err)
	}
	defer db.Close()
	testConn(db)
}
