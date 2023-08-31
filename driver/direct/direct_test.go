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

package direct_test

import (
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "cloud.google.com/go/alloydbconn/driver/direct"
)

var (
	// Name of database user.
	alloydbUser = os.Getenv("ALLOYDB_USER")
	// Password for the database user; be careful when entering a password on the
	// command line (it may go into your terminal's history).
	alloydbPass = os.Getenv("ALLOYDB_PASS")
	// Name of the database to connect to.
	alloydbDB = os.Getenv("ALLOYDB_DB")
)

func TestDirectDriver(t *testing.T) {
	dsn := fmt.Sprintf("postgres://%v:%v@%v/%v",
		alloydbUser, alloydbPass, "10.60.0.21", alloydbDB,
	)

	db, err := sql.Open("alloydb-direct", dsn)
	if err != nil {
		t.Fatal(err)
	}

	var tt time.Time
	if err := db.QueryRow("SELECT NOW()").Scan(&tt); err != nil {
		t.Fatal(err)
	}
	t.Log(tt)
}
