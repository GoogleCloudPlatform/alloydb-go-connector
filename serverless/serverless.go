package serverless

import (
	"context"

	alloydbadmin "cloud.google.com/go/alloydb/apiv1alpha"
	alloydbpb "cloud.google.com/go/alloydb/apiv1alpha/alloydbpb"
)

func NewClient() (*Client, error) {
	cl, err := alloydbadmin.NewAlloyDBAdminClient(context.Background())
	if err != nil {
		return nil, err
	}
	return &Client{client: cl}, nil
}

type Client struct {
	client *alloydbadmin.AlloyDBAdminClient
}

type Rows struct {
	results  []*alloydbpb.SqlResult
	metadata *alloydbpb.ExecuteSqlMetadata
}

func (r *Rows) Next() bool {
	return false
}

func (r *Rows) NextResultSet() bool {
	return false
}

func (r *Rows) Scan(dest ...any) error {
	return nil
}

// There are two problems to solve here:
// - how to prevent SQL injection
// - how to return the data in a Go-friendly format
// - how to handle transactions (or not)
func (c *Client) Query(ctx context.Context, query string) (*Rows, error) {
	req := &alloydbpb.ExecuteSqlRequest{
		UserCredential: &alloydbpb.ExecuteSqlRequest_Password{
			Password: "postgres",
		},
		Instance:     "projects/enocom-experiments-304623/locations/us-central1/clusters/enocom-cluster/instances/enocom-primary",
		Database:     "postgres",
		User:         "postgres",
		SqlStatement: query,
	}
	resp, err := c.client.ExecuteSql(ctx, req)
	if err != nil {
		return nil, err
	}
	return &Rows{
		results:  resp.GetSqlResults(),
		metadata: resp.GetMetadata(),
	}, nil
}
