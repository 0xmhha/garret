package store

// Register the pure-Go SQLite driver (decision D9 in docs/design/decisions.md).
import _ "modernc.org/sqlite"

// sqliteDriverName is the database/sql driver name registered by modernc.org/sqlite.
const sqliteDriverName = "sqlite"
