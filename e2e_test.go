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

//go:build !skip_alloydb
// +build !skip_alloydb

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
	alloydbConnName = os.Getenv("ALLOYDB_CONNECTION_NAME") // "AlloyDB instance connection name, in the form of 'project:region:instance'.
	alloydbUser     = os.Getenv("ALLOYDB_USER")            // Name of database user.
	alloydbPass     = os.Getenv("ALLOYDB_PASS")            // Password for the database user; be careful when entering a password on the command line (it may go into your terminal's history).
	alloydbDB       = os.Getenv("ALLOYDB_DB")              // Name of the database to connect to.
	alloydbUserIAM  = os.Getenv("ALLOYDB_USER_IAM")        // Name of database IAM user.
)

func requireAlloyDBVars(t *testing.T) {
	switch "" {
	case alloydbConnName:
		t.Fatal("'ALLOYDB_CONNECTION_NAME' env var not set")
	case alloydbUser:
		t.Fatal("'ALLOYDB_USER' env var not set")
	case alloydbPass:
		t.Fatal("'ALLOYDB_PASS' env var not set")
	case alloydbDB:
		t.Fatal("'ALLOYDB_DB' env var not set")
	case alloydbUserIAM:
		t.Fatal("'ALLOYDB_USER_IAM' env var not set")
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
		return d.Dial(ctx, alloydbConnName)
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
		t.Skip("skipping Postgres integration tests")
	}
	testConn := func(db *sql.DB) {
		var now time.Time
		if err := db.QueryRow("SELECT NOW()").Scan(&now); err != nil {
			t.Fatalf("QueryRow failed: %v", err)
		}
		t.Log(now)
	}
	pgxv4.RegisterDriver("alloydb")
	db, err := sql.Open(
		"alloydb",
		fmt.Sprintf("host=%s user=%s password=%s dbname=%s sslmode=disable",
			alloydbConnName, alloydbUser, alloydbPass, alloydbDB),
	)
	if err != nil {
		t.Fatalf("sql.Open want err = nil, got = %v", err)
	}
	defer db.Close()
	testConn(db)
}
