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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool, cleanup, err := connectPgx(ctx, alloydbURI, alloydbUser, alloydbPass, alloydbDB)
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
	if testing.Short() {
		t.Skip("skipping integration tests")
	}

	pool, cleanup, err := connectDatabaseSQL(alloydbURI, alloydbUser, alloydbPass, alloydbDB)
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
