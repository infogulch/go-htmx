package main

import (
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"

	_ "github.com/mattn/go-sqlite3"

	"github.com/infogulch/inject"
	"github.com/jmoiron/sqlx"
	"golang.org/x/exp/maps"
)

type TemplateFS interface {
	fs.FS
}

type StaticFS interface {
	fs.FS
}

func main() {
	injector, err := inject.New(
		TemplateFS(os.DirFS("./templates")),
		StaticFS(os.DirFS("./static")),
		make_db,
		make_static,
		make_index,
		make_todos,
		make_server,
	)
	if err != nil {
		log.Fatal(err)
	}

	iserver, err := injector.Get((*http.Server)(nil))
	if err != nil {
		log.Fatal(err)
	}
	server := iserver.(*http.Server)

	log.Print("Starting server...")
	log.Fatal(server.ListenAndServe())
}

func make_server(s StaticHandler, i IndexHandler, t TodosHandler) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/static", s)
	mux.Handle("/", i)
	mux.Handle("/todos", t)
	return &http.Server{
		Addr:    "0.0.0.0:8080",
		Handler: mux,
	}
}

var SCHEMA_MIGRATIONS = [...]string{`
CREATE TABLE kv (
	key TEXT PRIMARY KEY NOT NULL,
	value ANY NOT NULL
) STRICT, WITHOUT ROWID;

INSERT OR IGNORE INTO kv VALUES('index_count', 0);
INSERT OR IGNORE INTO kv VALUES('todos_filter', 'all')

CREATE TABLE todos (
	id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
	done INTEGER NOT NULL CHECK (done BETWEEN 0 AND 1),
	label TEXT NOT NULL
) STRICT;

CREATE VIEW todos_filtered AS
WITH f(filter) AS (SELECT value FROM kv WHERE key = 'todos_filter')
SELECT id, done, label
FROM todos
JOIN f
WHERE done AND f.filter IN ('all','completed')
OR NOT done AND f.filter IN ('all','active');
`}

func make_db() (*sqlx.DB, error) {
	db, err := sqlx.Connect("sqlite3", "app.db")
	if err != nil {
		return nil, err
	}
	schemaVersion := func() (version int) {
		db.Get(&version, "PRAGMA user_version;")
		return
	}
	version := schemaVersion()
	log.Printf("Found schema version %d", version)
	for i, stmt := range SCHEMA_MIGRATIONS[version:] {
		_, err = db.Exec(stmt)
		if err != nil {
			return nil, err
		}
		i += 1
		// sadly, sqlite doesn't support params in PRAGMA statments
		db.Exec(fmt.Sprintf("PRAGMA user_version=%d", i))
		log.Printf("Migrated schema to version %d", schemaVersion())
	}
	return db, nil
}

type StaticHandler interface {
	http.Handler
}

func make_static(fs StaticFS) StaticHandler {
	return http.StripPrefix("/static/", http.FileServer(http.FS(fs)))
}

type IndexHandler interface {
	http.Handler
}

func make_index(db *sqlx.DB, fs TemplateFS) IndexHandler {
	const KEY = "index_count"
	files := []string{"layout.html", "index.html"}
	return TemplateHandler(fs, files, template.FuncMap{
		"counter": func() (counter int, err error) {
			err = db.Get(&counter, "SELECT value FROM kv WHERE key = ?;", KEY)
			return
		},
		"increment": func() (counter int, err error) {
			err = db.Get(&counter, "UPDATE kv SET value = value + 1 WHERE key = ? RETURNING value;", KEY)
			return
		},
	})
}

type TodosHandler interface {
	http.Handler
}

func make_todos(db *sqlx.DB, fs TemplateFS) TodosHandler {
	type Todo struct {
		Id    int64
		Done  bool
		Label string
	}
	type Filter struct {
		Filter   string
		Selected int
	}
	const FILTER_KEY = "todos_filter"
	files := []string{"layout.html", "todos.html"}
	return TemplateHandler(fs, files, template.FuncMap{
		"new": func(label string) (todo Todo, err error) {
			if label == "" {
				err = fmt.Errorf("empty todo")
				return
			}
			todo.Label = label
			err = db.Get(&todo.Id, "INSERT INTO todos(done, label) VALUES (false, ?) RETURNING id;", label)
			return
		},
		"toggleall": func() (changed bool, err error) {
			err = db.Get(&changed, "UPDATE todos SET done = NOT (SELECT COALESCE(MIN(done),0) from todos) RETURNING changes() > 0;")
			return
		},
		"toggle": func(id string) (todo Todo, err error) {
			err = db.Get(&todo, "UPDATE todos SET done = NOT done WHERE id = ? RETURNING id, done, label;", id)
			return
		},
		"delete": func(id string) (changed bool, err error) {
			err = db.Get(&changed, "DELETE FROM todos WHERE id = ? RETURNING changes() == 1;", id)
			return
		},
		"alldone": func() (done bool, err error) {
			err = db.Get(&done, "SELECT COUNT(1) > 0 AND COUNT(1) = SUM(done) FROM todos;")
			return
		},
		"countdone": func() (count int, err error) {
			err = db.Get(&count, "SELECT COUNT(1) FROM todos WHERE NOT done;")
			return
		},
		"filter": func(filter string) (changed bool, err error) {
			if filter != "all" && filter != "active" && filter != "completed" {
				err = fmt.Errorf("invalid filter")
				return
			}
			err = db.Get(&changed, "UPDATE kv SET value = ? WHERE key = ? RETURNING changes() > 0;", filter, FILTER_KEY)
			return
		},
		"todos": func() (todos []Todo, err error) {
			err = db.Select(&todos, `SELECT id, done, label FROM todos_filtered ORDER BY id DESC;`)
			return
		},
		"todo": func(id string) (todo Todo, err error) {
			err = db.Get(&todo, `SELECT id, done, label FROM todos where id = ?`, id)
			return
		},
		"filters": func() (filters []Filter, err error) {
			err = db.Select(&filters,
				`WITH f(filter) AS (VALUES ('all'),('active'),('completed'))
				 SELECT filter, filter == (SELECT value FROM kv WHERE key = ?) as selected FROM f;`, FILTER_KEY)
			return
		},
	})
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
	return func(w http.ResponseWriter, r *http.Request) {
		routeId := GetRouteId(r)

		log.Printf("Handling request %s at %s\n", routeId, r.URL.Path)

		if t := tmpl.Lookup(routeId); t != nil {
			r.ParseForm()
			err := t.Execute(w, r)
			if err != nil {
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
