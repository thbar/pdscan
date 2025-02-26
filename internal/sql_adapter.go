package internal

import (
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/xo/dburl"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/mattn/go-sqlite3"
)

type SqlAdapter struct {
	DB *sqlx.DB
}

func (a *SqlAdapter) Init(url string) {
	u, err := dburl.Parse(url)
	if err != nil {
		abort(err)
	}

	db, err := sqlx.Connect(u.Driver, u.DSN)
	if err != nil {
		// TODO prompt for password if needed
		// var input string
		// fmt.Scanln(&input)
		abort(err)
	}
	// defer db.Close()

	a.DB = db
}

func (a SqlAdapter) FetchTables() (tables []table) {
	db := a.DB

	var query string

	switch db.DriverName() {
	case "sqlite3":
		query = `SELECT '' AS table_schema, name AS table_name FROM sqlite_master WHERE type = 'table' AND name != 'sqlite_sequence' ORDER BY name`
	case "mysql":
		query = `SELECT table_schema, table_name FROM information_schema.tables WHERE table_schema NOT IN ('information_schema', 'mysql', 'performance_schema') ORDER BY table_schema, table_name`
	default:
		query = `SELECT table_schema, table_name FROM information_schema.tables WHERE table_schema NOT IN ('information_schema', 'pg_catalog') ORDER BY table_schema, table_name`
	}

	err := db.Select(&tables, query)
	if err != nil {
		abort(err)
	}

	return tables
}

func (a SqlAdapter) FetchTableData(table table, limit int) ([]string, [][]string) {
	db := a.DB

	var sql string
	if db.DriverName() == "postgres" {
		quotedTable := quoteIdent(table.Schema) + "." + quoteIdent(table.Name)

		if tsmSystemRowsSupported(db) {
			sql = fmt.Sprintf("SELECT * FROM %s TABLESAMPLE SYSTEM_ROWS(%d)", quotedTable, limit)
		} else {
			// TODO randomize
			sql = fmt.Sprintf("SELECT * FROM %s LIMIT %d", quotedTable, limit)
		}
	} else if db.DriverName() == "sqlite3" {
		// TODO quote table name
		// TODO make more efficient if primary key exists
		// https://stackoverflow.com/questions/1253561/sqlite-order-by-rand
		sql = fmt.Sprintf("SELECT * FROM %s ORDER BY RANDOM() LIMIT %d", table.Name, limit)
	} else {
		// TODO quote table name
		// mysql
		sql = fmt.Sprintf("SELECT * FROM %s LIMIT %d", table.Schema+"."+table.Name, limit)
	}

	// run query on each table
	rows, err := db.Query(sql)
	if err != nil {
		abort(err)
	}

	// read everything as string and discard empty strings
	cols, err := rows.ColumnTypes()
	if err != nil {
		abort(err)
	}

	// map types
	columnNames := make([]string, len(cols))
	columnTypes := make([]string, len(cols))

	for i, col := range cols {
		columnNames[i] = col.Name()

		scanType := col.ScanType()
		if scanType == nil {
			columnTypes[i] = "string"
		} else {
			columnTypes[i] = scanType.String()
		}
	}

	// check values
	rawResult := make([][]byte, len(cols))

	columnValues := make([][]string, len(cols))
	for i := range columnValues {
		columnValues[i] = []string{}
	}

	dest := make([]interface{}, len(cols)) // A temporary interface{} slice
	for i := range rawResult {
		dest[i] = &rawResult[i] // Put pointers to each string in the interface slice
	}

	for rows.Next() {
		err = rows.Scan(dest...)
		if err != nil {
			abort(err)
		}

		for i, raw := range rawResult {
			if columnTypes[i] == "string" {
				if raw == nil {
					// ignore
				} else {
					str := string(raw)
					if str != "" {
						columnValues[i] = append(columnValues[i], str)
					}
				}
			}
		}
	}

	return columnNames, columnValues
}

// helpers

func quoteIdent(column string) string {
	return pq.QuoteIdentifier(column)
}

func tsmSystemRowsSupported(db *sqlx.DB) bool {
	row := db.QueryRow("SELECT COUNT(*) FROM pg_extension WHERE extname = 'tsm_system_rows'")
	var count int
	err := row.Scan(&count)
	if err != nil {
		abort(err)
	}
	return count > 0
}
