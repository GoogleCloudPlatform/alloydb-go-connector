package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "cloud.google.com/go/alloydbconn/driver/serverless"
)

func main() {
	ctx := context.Background()
	var (
		projectID  = os.Getenv("ALLOYDB_PROJECT")
		location   = os.Getenv("ALLOYDB_REGION")
		clusterID  = os.Getenv("ALLOYDB_CLUSTER")
		instanceID = os.Getenv("ALLOYDB_INSTANCE")
		password   = os.Getenv("DB_PASS")
	)
	dsn := fmt.Sprintf(
		// Both key value DSNs and URL DSNs work
		//
		// A key value DSN looks like this:
		//
		// "alloydb=projects/%s/locations/%s/clusters/%s/instances/%s dbname=postgres user=postgres password=%s",
		//
		// A URL DSN looks like this:
		"postgresql://postgres:%s@localhost/postgres?alloydb=projects/%s/locations/%s/clusters/%s/instances/%s",
		password, projectID, location, clusterID, instanceID,
	)

	// This doesn't require a network path to the dataplane!
	db, err := sql.Open("alloydb", dsn)
	if err != nil {
		log.Fatalf("Error opening database: %v\n", err)
	}
	defer db.Close()

	err = db.PingContext(ctx)
	if err != nil {
		log.Fatalf("Error pinging database: %v\n", err)
		return
	}
	log.Println("Successfully connected to AlloyDB")

	_, err = db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS users (
			id SERIAL PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			email VARCHAR(255) UNIQUE NOT NULL
		);`)
	if err != nil {
		log.Fatalf("Error creating table: %v\n", err)
	}
	log.Println("Table 'users' created (or already existed)")

	_, err = db.ExecContext(ctx, "INSERT INTO users (name, email) VALUES ('Alice', 'alice@example.com')")
	if err != nil {
		log.Fatalf("Error inserting data: %v\n", err)
	}

	_, err = db.ExecContext(ctx, "INSERT INTO users (name, email) VALUES ('Bob', 'bob@example.com')")
	if err != nil {
		log.Fatalf("Error inserting data: %v\n", err)
	}

	rows, err := db.QueryContext(ctx, "SELECT id, name, email FROM users ORDER BY id DESC")
	if err != nil {
		log.Fatalf("Error querying data: %v\n", err)
	}
	defer rows.Close()

	log.Println("Users:")
	for rows.Next() {
		var id int
		var name, email string
		if err := rows.Scan(&id, &name, &email); err != nil {
			log.Fatalf("Error scanning row: %v\n", err)
		}
		log.Printf("ID: %d, Name: %s, Email: %s\n", id, name, email)
	}
	if err = rows.Err(); err != nil {
		log.Fatalf("Error iterating rows: %v\n", err)
	}

	_, err = db.ExecContext(ctx, "UPDATE users SET name = 'Alicia' WHERE email = 'alice@example.com'")
	if err != nil {
		log.Fatalf("Error updating data: %v\n", err)
		return
	}

	_, err = db.ExecContext(ctx, "DELETE FROM users WHERE email = 'bob@example.com'")
	if err != nil {
		log.Fatalf("Error deleting data: %v\n", err)
	}
	log.Println("Done")
}
