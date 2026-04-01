package lustid

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"sync"
	"testing"
)

// TestSQL_NativeDriverSupport shows that ID requires no driver.Valuer or
// sql.Scanner implementation to round-trip through database/sql.
//
// ID is "type ID int64", so database/sql handles it natively via reflection:
//   - write side: DefaultParameterConverter converts ID → int64 automatically
//   - read side:  Scan converts int64 from the driver back into *ID automatically
func TestSQL_NativeDriverSupport(t *testing.T) {
	id := New()

	// Write side: driver.DefaultParameterConverter is what database/sql uses
	// to convert query arguments before handing them to the driver.
	// It must yield int64 without ID implementing driver.Valuer.
	converted, err := driver.DefaultParameterConverter.ConvertValue(id)
	if err != nil {
		t.Fatalf("ConvertValue: %v", err)
	}
	if converted != int64(id) {
		t.Fatalf("ConvertValue = %v (%T), want %v (int64)", converted, converted, int64(id))
	}

	// Read side: use a minimal stub driver that stores and returns the int64.
	// Scan must fill *ID from int64 without ID implementing sql.Scanner.
	db := openStubDB()
	if _, err := db.Exec("store", id); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	var got ID
	if err := db.QueryRow("fetch").Scan(&got); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if got != id {
		t.Fatalf("round-trip: got %v, want %v", got, id)
	}
}

// — minimal stub SQL driver —
//
// The driver is registered once under a fixed name via sync.Once.
// database/sql provides no Unregister, so the name must be stable across the
// process lifetime. Consequences:
//   - go test -count=N (N > 1): the second run reuses the same *sql.DB, which
//     is fine because Exec always writes before QueryRow reads.
//   - t.Parallel(): safe — SetMaxOpenConns(1) serialises all operations through
//     a single connection.

var (
	stubOnce sync.Once
	stubDB   *sql.DB
)

func openStubDB() *sql.DB {
	stubOnce.Do(func() {
		sql.Register("lustid_stub", new(idStubDriver))
		db, err := sql.Open("lustid_stub", "")
		if err != nil {
			panic("lustid stub db: " + err.Error())
		}
		// Single connection so Exec and QueryRow share the same idStubConn.
		db.SetMaxOpenConns(1)
		stubDB = db
	})
	return stubDB
}

type idStubDriver struct{}

func (d *idStubDriver) Open(_ string) (driver.Conn, error) {
	return new(idStubConn), nil
}

type idStubConn struct{ stored int64 }

func (c *idStubConn) Prepare(_ string) (driver.Stmt, error) { return &idStubStmt{conn: c}, nil }
func (c *idStubConn) Close() error                          { return nil }
func (c *idStubConn) Begin() (driver.Tx, error)             { return nil, errors.New("stub: no tx") }

type idStubStmt struct{ conn *idStubConn }

func (s *idStubStmt) Close() error    { return nil }
func (s *idStubStmt) NumInput() int   { return -1 }
func (s *idStubStmt) Exec(args []driver.Value) (driver.Result, error) {
	s.conn.stored = args[0].(int64)
	return driver.RowsAffected(1), nil
}
func (s *idStubStmt) Query(_ []driver.Value) (driver.Rows, error) {
	return &idStubRows{value: s.conn.stored}, nil
}

type idStubRows struct {
	value int64
	done  bool
}

func (r *idStubRows) Columns() []string { return []string{"id"} }
func (r *idStubRows) Close() error      { return nil }
func (r *idStubRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	dest[0] = r.value
	return nil
}
