package main

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"sort"
	"strings"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/exp/maps"
)

type TemplateFS interface {
	fs.ReadDirFS
}

//go:embed templates
var TemplateFiles embed.FS

func main() {
	static := http.FileServer(http.Dir("./static"))
	http.Handle("/static/", http.StripPrefix("/static/", static))

	db, err := sqlx.Connect("sqlite3", "todos.db")
	if err != nil {
		log.Fatal(err)
	} else {
		setupDB(db)
	}

	templates1, _ := fs.Sub(TemplateFiles, "templates")
	templates := templates1.(TemplateFS)

	http.HandleFunc("/todos", todos(db, templates))
	http.HandleFunc("/", index(db, templates))

	log.Print("Starting server...")
	log.Fatal(http.ListenAndServe("0.0.0.0:8080", nil))
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

func setupDB(db *sqlx.DB) {
	getSchema := func() (version int) {
		db.QueryRow("PRAGMA user_version;").Scan(&version)
		return
	}
	version := getSchema()
	log.Printf("Found schema version %d", version)
	for i, stmt := range SCHEMA_MIGRATIONS[version:] {
		_, err := db.Exec(stmt)
		if err != nil {
			log.Fatal(err)
		}
		i += 1
		// sadly, sqlite doesn't support params in PRAGMA statments
		db.Exec(fmt.Sprintf("PRAGMA user_version=%d", i))
		log.Printf("Migrated schema to version %d", getSchema())
	}
}

func index(db *sqlx.DB, fs TemplateFS) http.HandlerFunc {
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

func todos(db *sqlx.DB, fs TemplateFS) http.HandlerFunc {
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
		var routeId string
		{
			keys := maps.Keys(r.URL.Query())
			sort.Strings(keys)
			routeparts := append([]string{"hx", r.Method}, keys...)
			if r.Header.Get("HX-Request") != "true" {
				routeparts = routeparts[1:]
			}
			routeId = strings.ToLower(strings.Join(routeparts, "-"))
		}

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
