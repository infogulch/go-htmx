module todo-go

go 1.19

require (
	github.com/infogulch/inject v0.1.0
	github.com/jmoiron/sqlx v1.3.5
	github.com/mattn/go-sqlite3 v1.14.16
	golang.org/x/exp v0.0.0-20230118134722-a68e582fa157
)

replace (
	github.com/infogulch/inject => ../inject
)
