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

func (c *Client) Execute(ctx context.Context, query string) (*alloydbpb.ExecuteSqlResponse, error) {
	req := &alloydbpb.ExecuteSqlRequest{
		UserCredential: &alloydbpb.ExecuteSqlRequest_Password{
			Password: "postgres",
		},
		Instance:     "<INSTANCE_URI>",
		Database:     "postgres",
		User:         "postgres",
		SqlStatement: query,
	}
    resp, err := c.client.ExecuteSql(ctx, req)

    for _, res := range resp.GetSqlResults() {
        cols := res.GetColumns()
        for _, r := range res.GetRows() {
            for i, v := range r.GetValues() {
                vtype := cols[i]
                _ = vtype.GetName()
                _ = vtype.GetType()

                _ = v.GetValue()
            }
        }
    }

    return resp, err
}
