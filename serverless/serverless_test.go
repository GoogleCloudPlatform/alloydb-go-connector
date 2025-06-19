package serverless_test

import (
	"context"
	"fmt"
	"testing"

	"cloud.google.com/go/alloydbconn/serverless"
)

func Test(t *testing.T) {
	cl, err := serverless.NewClient()
	if err != nil {
		t.Fatal(err)
	}

	rows, err := cl.Query(context.Background(), `select pid,usename from pg_stat_activity; select now();`)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(rows)
	var ps []process
	for rows.Next() {
		var p process
		rows.Scan(&p.id, &p.usename)
		ps = append(ps, p)
	}

	fmt.Println(ps)
}

type process struct {
	id      int
	usename string
}
