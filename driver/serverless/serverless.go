package serverless

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"log"
	"reflect"
	"strconv"
	"time"

	alloydb "cloud.google.com/go/alloydb/apiv1alpha"
	"cloud.google.com/go/alloydb/apiv1alpha/alloydbpb"
	"github.com/jackc/pgx/v5/pgconn"
)

// Define the name of the driver.
const driverName = "alloydb"

func init() {
	sql.Register(driverName, &severlessDriver{})
}

// severlessDriver implements the database/sql/driver.Driver interface backed
// by the ExecuteSQL RPC.
type severlessDriver struct{}

// Open returns a new connection to the database. The DSN should include an
// `alloydb` parameter set to the target AlloyDB instance URI.
func (d *severlessDriver) Open(dsn string) (driver.Conn, error) {
	log.Println("Opening serverless driver...")
	// TODO: this should only be done once to avoid unnecessary client
	// creation. Also, it should be possible to configure the options.
	client, err := alloydb.NewAlloyDBAdminClient(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to create AlloyDB client: %w", err)
	}

	cfg, err := pgconn.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}

	return &conn{
		client:       client,
		instancePath: cfg.RuntimeParams["alloydb"],
		cfg:          cfg,
	}, nil
}

// conn implements the database/sql/driver.Conn, driver.Pinger,
// driver.ExecerContext, and driver.QueryerContext interfaces.
type conn struct {
	client       *alloydb.AlloyDBAdminClient
	instancePath string
	cfg          *pgconn.Config
}

// Prepare returns a new statement for the given query.
// For the AlloyDB ExecuteSQL API, prepare often doesn't involve
// pre-compilation but rather just stores the query for later execution.
func (c *conn) Prepare(query string) (driver.Stmt, error) {
	return &statement{
		c:     c,
		query: query,
	}, nil
}

// Close closes the connection.
// This typically involves closing the underlying AlloyDB client.
func (c *conn) Close() error {
	// TODO: don't close the client until the driver is closed
	if c.client != nil {
		return c.client.Close()
	}
	return nil
}

// Begin returns a new transaction.
// TODO: a transaction may be used but it's only per RPC and not across
// multiple RPCs. Need a streaming interface into ExecuteSQL or similar to do
// this well.
func (c *conn) Begin() (driver.Tx, error) {
	return nil, driver.ErrSkip
}

// Ping implements driver.Pinger to check the connection's liveness.
func (c *conn) Ping(ctx context.Context) error {
	_, err := c.ExecContext(ctx, "SELECT 1", nil)
	return err
}

// ExecContext executes a query without returning any rows.
func (c *conn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	// TODO: necessary to avoid SQL injection
	// p, err := convertArgsParams(args)
	// if err != nil {
	// 	return nil, fmt.Errorf("failed to convert arguments: %w", err)
	// }
	// _ = p

	req := buildExecuteSqlRequest(c.instancePath, c.cfg, query)
	log.Printf("ExecContext: Sending request: %v", req)
	resp, err := c.client.ExecuteSql(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute SQL: %w", err)
	}
	log.Printf("ExecContext: Received response: %v", resp)
	if resp.GetMetadata().GetStatus() == alloydbpb.ExecuteSqlMetadata_ERROR {
		return nil, errors.New(resp.GetMetadata().GetMessage())
	}
	return &result{}, nil
}

// QueryContext executes a query that may return rows, such as a SELECT
// statement.
func (c *conn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	// TODO: necessary to avoid SQL injection
	// p, err := convertArgsParams(args)
	// if err != nil {
	// 	return nil, fmt.Errorf("failed to convert arguments: %w", err)
	// }
	// _ = p

	req := buildExecuteSqlRequest(c.instancePath, c.cfg, query)
	log.Printf("QueryContext: Sending request: %v", req)
	resp, err := c.client.ExecuteSql(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to query SQL: %w", err)
	}
	log.Printf("QueryContext: Received response: %v", resp)
	if resp.GetMetadata().GetStatus() == alloydbpb.ExecuteSqlMetadata_ERROR {
		return nil, errors.New(resp.GetMetadata().GetMessage())
	}
	return newRows(resp.GetSqlResults()), nil
}

func buildExecuteSqlRequest(instanceURI string, c *pgconn.Config, query string) *alloydbpb.ExecuteSqlRequest {
	return &alloydbpb.ExecuteSqlRequest{
		Instance: instanceURI,
		User:     c.User,
		UserCredential: &alloydbpb.ExecuteSqlRequest_Password{
			Password: c.Password,
		},
		Database:     c.Database,
		SqlStatement: query,
		// Parameters:   alloydbParams,
	}
}

// statement implements the database/sql/driver.Stmt, driver.StmtExecContext,
// and driver.StmtQueryContext interfaces.
type statement struct {
	c     *conn
	query string
}

// Close closes the statement.
// For a simple `ExecuteSQL` based driver, this is usually a no-op.
func (s *statement) Close() error { return nil }

// NumInput returns the number of placeholder parameters.
// This driver does not pre-parse the query for placeholders.
// It relies on the API to handle parameters.
func (s *statement) NumInput() int {
	// Returns -1 to indicate that the driver doesn't know the number of
	// parameters.
	return -1
}

// Exec executes a query that doesn't return rows.
// Deprecated: Use ExecContext instead.
func (s *statement) Exec(args []driver.Value) (driver.Result, error) {
	listArgs := make([]driver.NamedValue, len(args))
	for i, arg := range args {
		listArgs[i] = driver.NamedValue{Ordinal: i + 1, Value: arg}
	}
	return s.ExecContext(context.Background(), listArgs)
}

// Query executes a query that may return rows.
// Deprecated: Use QueryContext instead.
func (s *statement) Query(args []driver.Value) (driver.Rows, error) {
	listArgs := make([]driver.NamedValue, len(args))
	for i, arg := range args {
		listArgs[i] = driver.NamedValue{Ordinal: i + 1, Value: arg}
	}
	return s.QueryContext(context.Background(), listArgs)
}

// ExecContext executes a query without returning any rows.
func (s *statement) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	return s.c.ExecContext(ctx, s.query, args)
}

// QueryContext executes a query that may return rows.
func (s *statement) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	return s.c.QueryContext(ctx, s.query, args)
}

type resultSet struct {
	columnNames []string
	columnTypes []*columnType
	rows        []*alloydbpb.SqlResultRow
	rowIndex    int
}

func newColumnType(name, databaseType string, t reflect.Type) *columnType {
	return &columnType{
		name:         name,
		t:            t,
		databaseType: databaseType,
	}
}

type columnType struct {
	name         string
	t            reflect.Type
	databaseType string
}

func (c *columnType) scanType() reflect.Type {
	return c.t
}

func (c *columnType) databaseTypeName() string {
	return c.databaseType
}

func (c *columnType) Length() (int64, bool) {
	// TODO
	return 0, true
}

func (c *columnType) Nullable() (bool, bool) {
	// TODO
	return true, true
}

func (c *columnType) PrecisionScale() (int64, int64, bool) {
	// TODO
	return 0, 0, true
}

// HasNextResultSet is called at the end of the current result set and
// reports whether there is another result set after the current one.
func (r *rows) HasNextResultSet() bool {
	return r.resultSetIndex >= len(r.resultSets)
}

// NextResultSet advances the driver to the next result set even
// if there are remaining rows in the current result set.
//
// NextResultSet should return io.EOF when there are no more result sets.
func (r *rows) NextResultSet() error {
	r.resultSetIndex++
	if r.resultSetIndex >= len(r.resultSets) {
		return io.EOF
	}
	return nil
}

// rows implements the database/sql/driver.Rows interface.
type rows struct {
	resultSets     []*resultSet
	resultSetIndex int
}

// newRows creates a new rows type.
func newRows(results []*alloydbpb.SqlResult) *rows {
	if results == nil {
		return &rows{}
	}
	rss := make([]*resultSet, len(results))

	for i, r := range results {
		columnNames := make([]string, len(r.GetColumns()))
		columnTypes := make([]*columnType, len(r.GetColumns()))

		for j, col := range r.GetColumns() {
			var colType reflect.Type
			switch col.GetType() {
			case "BOOL":
				colType = reflect.TypeOf(true)
			case "INT64", "BIGINT":
				colType = reflect.TypeOf(int64(0))
			case "DOUBLE", "FLOAT":
				colType = reflect.TypeOf(float64(0))
			case "STRING", "TEXT", "VARCHAR":
				colType = reflect.TypeOf("")
			case "BYTES":
				colType = reflect.TypeOf([]byte{})
			case "TIMESTAMP", "DATETIME":
				colType = reflect.TypeOf(time.Time{})
			default:
				// TODO: how to handle custom types?
				colType = reflect.TypeOf(new(any)).Elem()
			}
			columnNames[j] = col.GetName()
			columnTypes[j] = newColumnType(col.GetName(), col.GetType(), colType)
		}

		rss[i] = &resultSet{
			columnNames: columnNames,
			columnTypes: columnTypes,
			rows:        r.GetRows(),
			rowIndex:    -1,
		}
	}

	return &rows{resultSets: rss, resultSetIndex: -1}
}

// Columns returns the names of the columns.
func (r *rows) Columns() []string {
	if r.resultSetIndex == -1 {
		r.resultSetIndex = 0
	}
	return r.resultSets[r.resultSetIndex].columnNames
}

// ColumnTypeScanType returns the Go scan type for the column.
func (r *rows) ColumnTypeScanType(index int) reflect.Type {
	ct := r.resultSets[r.resultSetIndex].columnTypes
	if index < 0 || index >= len(ct) {
		return nil
	}
	return ct[index].scanType()
}

// ColumnTypeDatabaseTypeName returns the database column type name.
func (r *rows) ColumnTypeDatabaseTypeName(index int) string {
	ct := r.resultSets[r.resultSetIndex].columnTypes
	if index < 0 || index >= len(ct) {
		return ""
	}
	return ct[index].databaseTypeName()
}

// ColumnTypeLength returns the length of the column.
func (r *rows) ColumnTypeLength(index int) (length int64, ok bool) {
	ct := r.resultSets[r.resultSetIndex].columnTypes
	if index < 0 || index >= len(ct) {
		return 0, false
	}
	return ct[index].Length()
}

// ColumnTypeNullable returns whether the column is nullable.
func (r *rows) ColumnTypeNullable(index int) (nullable, ok bool) {
	ct := r.resultSets[r.resultSetIndex].columnTypes
	if index < 0 || index >= len(ct) {
		return false, false
	}
	return ct[index].Nullable()
}

// ColumnTypePrecisionScale returns the precision and scale for decimal/numeric types.
func (r *rows) ColumnTypePrecisionScale(index int) (precision, scale int64, ok bool) {
	ct := r.resultSets[r.resultSetIndex].columnTypes
	if index < 0 || index >= len(ct) {
		return 0, 0, false
	}
	return ct[index].PrecisionScale()
}

func (r *rows) Close() error {
	// TODO: track closed state?
	return nil
}

// Next prepares the next row for reading.
func (r *rows) Next(dest []driver.Value) error {
	// When the caller doesn't advance the result set, do it for them
	// initially.
	if r.resultSetIndex == -1 {
		r.resultSetIndex = 0
	}
	rs := r.resultSets[r.resultSetIndex]
	rs.rowIndex++

	if rs.rowIndex >= len(rs.rows) {
		return io.EOF
	}

	row := rs.rows[rs.rowIndex]
	if len(dest) != len(row.GetValues()) {
		return fmt.Errorf("column count mismatch: expected %d, got %d", len(row.GetValues()), len(dest))
	}

	for i, val := range row.GetValues() {
		v := val.GetValue()
		// TODO
		isNull := val.GetNullValue()
		_ = isNull

		switch rs.columnTypes[i].databaseTypeName() {
		case "BOOL":
			// TODO
			dest[i] = false
		case "INT64", "BIGINT":
			val, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return err
			}
			dest[i] = val
		case "DOUBLE", "FLOAT":
			val, err := strconv.ParseFloat(v, 10)
			if err != nil {
				return err
			}
			dest[i] = val
		case "STRING", "TEXT", "VARCHAR":
			dest[i] = v
		case "BYTES":
			dest[i] = []byte(v)
		case "TIMESTAMP", "DATETIME":
			t, err := time.Parse(v, time.RFC3339)
			if err != nil {
				return err
			}
			dest[i] = t
		default:
			dest[i] = v
		}
	}
	return nil
}

// result implements the database/sql/driver.Result interface.
type result struct {
	rowsAffected int64
}

// LastInsertId returns the database's auto-generated ID after an insert.
// The AlloyDB ExecuteSQL API DmlStats does not provide this directly.
// In a real scenario, you'd need to explicitly query for it (e.g., using `RETURNING id`).
func (r *result) LastInsertId() (int64, error) {
	return 0, fmt.Errorf("LastInsertId is not supported by this AlloyDB driver implementation")
}

// RowsAffected returns the number of rows affected by the query.
func (r *result) RowsAffected() (int64, error) {
	return 0, fmt.Errorf("RowsAffected is not supported by this AlloyDB driver implementation")
}
