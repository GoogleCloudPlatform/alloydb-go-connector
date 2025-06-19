package serverless_test

import (
	"context"
	"testing"

	"cloud.google.com/go/alloydbconn/serverless"
)

func Test(t *testing.T) {
    cl, err := serverless.NewClient()
    if err != nil {
        t.Fatal(err)
    }

    resp, err := cl.Execute(context.Background(), "select * from pg_stat_activity LIMIT 1;")
    if err != nil {
        t.Fatal(err)
    }
    t.Logf("%+v", resp)
}
