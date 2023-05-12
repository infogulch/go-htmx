package main

import (
	"embed"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strings"

	"github.com/cozodb/cozo-lib-go"
	"github.com/stretchr/objx"
	"golang.org/x/exp/maps"
)

type TemplateFS interface {
	fs.ReadDirFS
}

//go:embed templates static
var EmbedFiles embed.FS

var Files fs.FS = os.DirFS(".") /* EmbedFiles */

func main() {
	templates := must(fs.Sub(Files, "templates")).(TemplateFS)
	db := must(cozo.New("mem", "", nil))

	funcs := template.FuncMap{
		"query": func(query string, params any) (cozo.NamedRows, error) {
			return db.Run(query, makeParams(params))
		},
		"queryrows": func(query string, params any) ([]map[string]any, error) {
			return QueryRows(db, query, makeParams(params))
		},
		"queryrow": func(query string, params any) (map[string]any, error) {
			return QueryRow(db, query, makeParams(params))
		},
		"queryval": func(query string, params any) (any, error) {
			return QueryVal(db, query, makeParams(params))
		},
		"idx": func(idx int, arr any) any {
			return reflect.ValueOf(arr).Index(idx).Interface()
		},
	}

	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(must(fs.Sub(Files, "static"))))))
	http.HandleFunc("/", TemplateHandler(templates, []string{"layout.html", "index.html"}, funcs))
	http.HandleFunc("/todos", TemplateHandler(templates, []string{"layout.html", "todos.html"}, funcs))

	log.Print("Starting server...")
	log.Fatal(http.ListenAndServe("0.0.0.0:8080", nil))
}

func QueryRows(db cozo.CozoDB, query string, params objx.Map) (rows []map[string]any, err error) {
	var result cozo.NamedRows
	result, err = db.Run(query, params)
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

func QueryRow(db cozo.CozoDB, query string, params objx.Map) (row map[string]any, err error) {
	var result cozo.NamedRows
	result, err = db.Run(query, params)
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

func QueryVal(db cozo.CozoDB, query string, params objx.Map) (val any, err error) {
	var result cozo.NamedRows
	result, err = db.Run(query, params)
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

func makeParams(param any) (p objx.Map) {
	if param != nil {
		p = objx.Map{"p": param}
	}
	return
}

func must[T any](t T, err error) T {
	if err != nil {
		log.Fatal(err)
	}
	return t
}

// TemplateHandler constructs an html.Template from the provided args
// and returns an http.HandlerFunc that routes requests to named nested
// template definitions based on a routeId derived from URL query params.
//
// routeId is a string constructed from the request:
// - If the request has header HX-Request=true, then it's prefixed with `hx-`
// - The http method
// - A sorted list of unique URL query params
//
// These parts are joined with '-' and lowercased, then used to look up
// a template definition; if found, the template is rendered as a response,
// else it renders a 404.
//
// Example requests and matching routeId:
// - Plain HTTP GET: get
// - HTTP GET with HX-Request header: hx-get
// - HTTP POST with nav param: post-nav
// - HTTP DELETE with HX-Request header and id param: hx-delete-id
// - HTTP POST with tYPe and iD params: post-id-type
func TemplateHandler(fs TemplateFS, files []string, funcs template.FuncMap) http.HandlerFunc {
	tmpl, err := template.New(files[0]).Funcs(funcs).ParseFS(fs, files...)
	if err != nil {
		log.Fatal(err)
	}
	if t := tmpl.Lookup("init"); t != nil {
		err = t.Execute(io.Discard, nil)
		if err != nil {
			log.Fatal(err)
		}
	}
	return func(w http.ResponseWriter, r *http.Request) {
		routeId := GetRouteId(r)

		log.Printf("Handling request %s at %s\n", routeId, r.URL.Path)

		if t := tmpl.Lookup(routeId); t != nil {
			r.ParseForm()
			err := t.Execute(w, r)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				log.Print(err)
			}
		} else {
			http.NotFound(w, r)
		}
	}
}

func GetRouteId(r *http.Request) string {
	var prefix string
	if r.Header.Get("HX-Request") == "true" {
		prefix = "htmx"
	} else {
		prefix = "http"
	}
	keys := maps.Keys(r.URL.Query())
	sort.Strings(keys)
	routeparts := append([]string{prefix, r.Method}, keys...)
	return strings.ToLower(strings.Join(routeparts, "-"))
}
