package main

import (
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"reflect"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

func NewFuncs(db *sqlx.DB) template.FuncMap {
	return template.FuncMap{
		"exec": func(query string, params ...any) (sql.Result, error) {
			return Exec(db, query, params...)
		},
		"queryrows": func(query string, params ...any) (rows []map[string]any, err error) {
			return QueryRows(db, query, params...)
		},
		"queryrow": func(query string, params ...any) (row map[string]any, err error) {
			return QueryRow(db, query, params...)
		},
		"queryval": func(query string, params ...any) (val any, err error) {
			return QueryVal(db, query, params...)
		},
		"idx":  Idx,
		"dict": Dict,
		"list": List,
		"uuid": uuid.New,
	}
}

func Exec(db *sqlx.DB, query string, params ...any) (result sql.Result, err error) {
	result, err = db.Exec(query, params...)
	// log.Printf("%+v, %+v", result, err)
	// LogQuery("QueryVal", query, result)
	return
}

func QueryRows(db *sqlx.DB, query string, params ...any) (rows []map[string]any, err error) {
	var r *sqlx.Rows
	r, err = db.Queryx(query, params...)
	for r.Next() {
		m := make(map[string]any)
		r.MapScan(m)
		rows = append(rows, m)
	}
	if err == nil {
		err = r.Err()
	}
	return
}

func QueryRow(db *sqlx.DB, query string, params ...any) (row map[string]any, err error) {
	var rows []map[string]any
	rows, err = QueryRows(db, query, params...)
	if len(rows) != 1 {
		return nil, fmt.Errorf("query returned %d rows, expected exactly 1 row", len(rows))
	}
	row = rows[0]
	return
}

func QueryVal(db *sqlx.DB, query string, params ...any) (val any, err error) {
	var results []any
	err = db.Select(&results, query, params...)
	val = results[0]
	return
}

// https://github.com/gohugoio/hugo/blob/6aededf6b42011c3039f5f66487a89a8dd65e0e7/tpl/collections/collections.go#L162
// Dictionary creates a new map from the given parameters by
// treating values as key-value pairs.  The number of values must be even.
// The keys can be string slices, which will create the needed nested structure.
func Dict(values ...any) (map[string]any, error) {
	if len(values)%2 != 0 {
		return nil, errors.New("invalid dictionary call")
	}

	root := make(map[string]any)

	for i := 0; i < len(values); i += 2 {
		dict := root
		var key string
		switch v := values[i].(type) {
		case string:
			key = v
		case []string:
			for i := 0; i < len(v)-1; i++ {
				key = v[i]
				var m map[string]any
				v, found := dict[key]
				if found {
					m = v.(map[string]any)
				} else {
					m = make(map[string]any)
					dict[key] = m
				}
				dict = m
			}
			key = v[len(v)-1]
		default:
			return nil, errors.New("invalid dictionary key")
		}
		dict[key] = values[i+1]
	}

	return root, nil
}

func List(values ...any) []any {
	return values
}

func Idx(idx int, arr any) any {
	return reflect.ValueOf(arr).Index(idx).Interface()
}
