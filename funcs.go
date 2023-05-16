package main

import (
	"errors"
	"fmt"
	"html/template"
	"reflect"

	"github.com/cozodb/cozo-lib-go"
)

type Params map[string]any

func NewFuncs(db cozo.CozoDB) template.FuncMap {
	return template.FuncMap{
		"query": func(query string, params Params) (cozo.NamedRows, error) {
			return Query(db, query, params)
		},
		"queryrows": func(query string, params Params) (rows []map[string]any, err error) {
			return QueryRows(db, query, params)
		},
		"queryrow": func(query string, params Params) (row map[string]any, err error) {
			return QueryRow(db, query, params)
		},
		"queryval": func(query string, params Params) (val any, err error) {
			return QueryVal(db, query, params)
		},
		"idx":  Idx,
		"dict": Dict,
	}
}

func Query(db cozo.CozoDB, query string, params Params) (cozo.NamedRows, error) {
	return db.Run(query, (map[string]any)(params))
}

func QueryRows(db cozo.CozoDB, query string, params Params) (rows []map[string]any, err error) {
	var result cozo.NamedRows
	result, err = db.Run(query, (map[string]any)(params))
	if err != nil {
		return
	}
	for _, row := range result.Rows {
		rowmap := map[string]any{}
		for colidx, colname := range result.Headers {
			rowmap[colname] = row[colidx]
		}
		rows = append(rows, rowmap)
	}
	return
}

func QueryRow(db cozo.CozoDB, query string, params Params) (row map[string]any, err error) {
	var result cozo.NamedRows
	result, err = db.Run(query, (map[string]any)(params))
	if err != nil {
		return
	}
	if len(result.Rows) != 1 {
		return nil, fmt.Errorf("the query must return a single row, instead it returned %d", len(result.Rows))
	}
	row = map[string]any{}
	for colidx, colname := range result.Headers {
		row[colname] = result.Rows[0][colidx]
	}
	return
}

func QueryVal(db cozo.CozoDB, query string, params Params) (val any, err error) {
	var result cozo.NamedRows
	result, err = db.Run(query, (map[string]any)(params))
	if err != nil {
		return
	}
	if len(result.Rows) != 1 {
		return nil, fmt.Errorf("the query must return a single row, instead it returned %d", len(result.Rows))
	}
	if len(result.Rows[0]) != 1 {
		return nil, fmt.Errorf("the query must return a single column, instead it returned %d", len(result.Rows))
	}
	val = result.Rows[0][0]
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

func Idx(idx int, arr any) any {
	return reflect.ValueOf(arr).Index(idx).Interface()
}
