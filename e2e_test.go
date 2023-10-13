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
	"os"
	"testing"
	"time"
)

var (
	// AlloyDB instance name, in the form of
	// projects/PROJECT_ID/locations/REGION_ID/clusters/CLUSTER_ID/instances/INSTANCE_ID
	alloydbInstanceName = os.Getenv("ALLOYDB_INSTANCE_NAME")
	// Name of database user.
	alloydbUser = os.Getenv("ALLOYDB_USER")
	// Name of database IAM user.
	alloydbIAMUser = os.Getenv("ALLOYDB_IAM_USER")
	// IP address of the instance
	alloydbInstanceIP = os.Getenv("ALLOYDB_INSTANCE_IP")
	// Password for the database user; be careful when entering a password on the
	// command line (it may go into your terminal's history).
	alloydbPass = os.Getenv("ALLOYDB_PASS")
	// Name of the database to connect to.
	alloydbDB = os.Getenv("ALLOYDB_DB")
)

func requireAlloyDBVars(t *testing.T) {
	switch "" {
	case alloydbInstanceName:
		t.Fatal("'ALLOYDB_INSTANCE_NAME' env var not set")
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool, cleanup, err := connectPgx(
		ctx, alloydbInstanceName, alloydbUser, alloydbPass, alloydbDB,
	)
	if err != nil {
		_ = cleanup()
		t.Fatal(err)
	}
	defer func() {
		pool.Close()
		// best effort
		_ = cleanup()
	}()
}

func TestDatabaseSQLConnect(t *testing.T) {
	tests := []struct {
		version string
	}{
		{
			version: "v4",
		},
		{
			version, "v5",
		},
	}

	if testing.Short() {
		t.Skip("skipping integration tests")
	}

	for _, tc := range tests {
		pool, cleanup, err := connectDatabaseSQL(
			alloydbInstanceName, alloydbUser, alloydbPass, alloydbDB, tc.version,
		)
		if err != nil {
			_ = cleanup()
			t.Fatal(err)
		}
		defer func() {
			pool.Close()
			// best effort
			_ = cleanup()
		}()
	}
}

func TestDirectDatabaseSQLAutoIAMAuthN(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	db, err := connectDirectDatabaseSQLAutoIAMAuthN(
		alloydbInstanceIP, alloydbIAMUser, alloydbDB,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var tt time.Time
	if err := db.QueryRow("SELECT NOW()").Scan(&tt); err != nil {
		t.Fatal(err)
	}
	t.Log(tt)
}

func TestDirectPGXAutoIAMAuthN(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	db, err := connectDirectPGXPoolAutoIAMAuthN(
		context.Background(),
		alloydbInstanceIP, alloydbIAMUser, alloydbDB,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var tt time.Time
	if err := db.QueryRow(context.Background(), "SELECT NOW()").Scan(&tt); err != nil {
		t.Fatal(err)
	}
	t.Log(tt)
}
