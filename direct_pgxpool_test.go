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

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v4"
	"github.com/jackc/pgx/v4/pgxpool"
	"golang.org/x/oauth2/google"
)

// connectDirectPGXPoolAutoIAMAuthN establishes a connection to your
// database using pgxpool and inserts a fresh OAuth2 token before initiating a
// connection. This enables Auto IAM AuthN on the direct path without using
// a connector.
//
// The function takes the revelent instance IP, a username, and a database
// name. Usage looks like this:
//
//	db, err := connectDirectPGXPoolAutoIAMAuthN(
//	  context.Background(),
//	  "10.0.0.1",             // whatever your instance IP is
//	  "my-sa@my-project.iam", // whatever IAM user you're running as
//	  "mydb",                 // whatever database you want to connect to
//	)
//
// Because this connection uses an OAuth2 token as a password, you must require
// SSL, or better, enforce all clients speak SSL on the server side. This
// ensures the OAuth2 token is not inadvertantly leaked.
func connectDirectPGXPoolAutoIAMAuthN(
	ctx context.Context,
	instIP, user, dbname string,
) (*pgxpool.Pool, error) {
	ts, err := google.DefaultTokenSource(
		ctx,
		"https://www.googleapis.com/auth/cloud-platform",
	)
	if err != nil {
		return nil, err
	}
	config, err := pgxpool.ParseConfig(
		fmt.Sprintf(
			"host=%v user=%v dbname=%v sslmode=require",
			instIP, user, dbname,
		),
	)
	if err != nil {
		return nil, err
	}
	// This function is called before every connection
	config.BeforeConnect = func(ctx context.Context, c *pgx.ConnConfig) error {
		tok, err := ts.Token()
		if err != nil {
			return err
		}
		c.Password = tok.AccessToken
		return nil
	}
	return pgxpool.ConnectConfig(ctx, config)
}
