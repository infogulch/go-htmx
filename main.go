package main

import (
	"database/sql"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

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

	db, err := sql.Open("sqlite3", "todos.db")
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

func setupDB(db *sql.DB) {
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

func index(db *sql.DB, fs TemplateFS) http.HandlerFunc {
	const KEY = "index_count"
	files := []string{"layout.gohtml", "index.gohtml"}
	// db init
	{
		_, err := db.Exec("INSERT OR IGNORE INTO kv VALUES (?, 0);", KEY)
		if err != nil {
			log.Printf("failed to initialize kv %s = 0: %v", KEY, err)
		}
	}
	return TemplateHandler(fs, files, template.FuncMap{
		"counter": func() (counter int, err error) {
			err = db.QueryRow("SELECT value FROM kv WHERE key = ?;", KEY).Scan(&counter)
			return
		},
		"increment": func() (counter int, err error) {
			err = db.QueryRow("UPDATE kv SET value = value + 1 WHERE key = ? RETURNING value;", KEY).Scan(&counter)
			return
		},
	})
}

func todos(db *sql.DB, fs TemplateFS) http.HandlerFunc {
	type Todo struct {
		Id    int64
		Done  bool
		Label string
	}
	type Filter struct {
		Filter   string
		Selected int
	}
	scanTodo := func(todo *Todo) []interface{} {
		return []interface{}{&todo.Id, &todo.Done, &todo.Label}
	}
	scanFilter := func(filter *Filter) []interface{} {
		return []interface{}{&filter.Filter, &filter.Selected}
	}
	const FILTER_KEY = "todos_filter"
	{
		_, err := db.Exec("INSERT OR IGNORE INTO kv VALUES (?, 'all')", FILTER_KEY)
		if err != nil {
			log.Printf("failed to initialize kv %s = 'all': %v", FILTER_KEY, err)
		}
	}

	files := []string{"layout.gohtml", "todos.gohtml"}
	return TemplateHandler(fs, files, template.FuncMap{
		"new": func(data url.Values) (todo Todo, err error) {
			todo.Label = data.Get("newtodo")
			if todo.Label == "" {
				err = fmt.Errorf("empty todo")
				return
			}
			var res sql.Result
			res, err = db.Exec("INSERT INTO todos(done, label) VALUES (false, ?);", &todo.Label)
			if err != nil {
				return
			}
			todo.Id, err = res.LastInsertId()
			return
		},
		"toggleall": func() (changed bool, err error) {
			var res sql.Result
			res, err = db.Exec("UPDATE todos SET done = NOT (SELECT COALESCE(MIN(done),0) from todos)")
			if affected, _ := res.RowsAffected(); affected > 0 {
				changed = true
			}
			return
		},
		"toggle": func(id string) (todo Todo, err error) {
			var idn int64
			idn, err = strconv.ParseInt(id, 10, 64)
			if err != nil {
				return
			}
			res, err := db.Exec("UPDATE todos SET done = NOT done WHERE id = ?", idn)
			if err != nil {
				return
			}
			if affected, _ := res.RowsAffected(); affected != 1 {
				return Todo{}, fmt.Errorf("invalid todo id")
			}

			err = db.QueryRow("SELECT id, done, label FROM todos WHERE id = ?", idn).Scan(scanTodo(&todo)...)
			return
		},
		"delete": func(id string) (_ struct{}, err error) {
			var idn int64
			idn, err = strconv.ParseInt(id, 10, 64)
			if err != nil {
				return
			}
			var res sql.Result
			res, err = db.Exec("DELETE FROM todos WHERE id = ?", idn)
			if affected, _ := res.RowsAffected(); affected != 1 {
				return struct{}{}, fmt.Errorf("invalid todo id")
			}
			if err != nil {
				return
			}
			return
		},
		"alldone": func() (done bool, err error) {
			err = db.QueryRow("SELECT COUNT(1) > 0 AND COUNT(1) = SUM(done) FROM todos").Scan(&done)
			return
		},
		"countdone": func() (count int, err error) {
			err = db.QueryRow("SELECT COUNT(1) FROM todos WHERE NOT done").Scan(&count)
			return
		},
		"filter": func(filter string) (changed bool, err error) {
			if filter != "all" && filter != "active" && filter != "completed" {
				err = fmt.Errorf("invalid filter")
				return
			}
			var res sql.Result
			res, err = db.Exec("UPDATE kv SET value = ? where key = ?", filter, FILTER_KEY)
			if err != nil {
				return
			}
			if affected, _ := res.RowsAffected(); affected > 0 {
				changed = true
			}
			return
		},
		"todos": func() ([]Todo, error) {
			return RowsScanner(scanTodo)(db.Query(`SELECT id, done, label FROM todos_filtered`))
		},
		"todo": func(id string) (todo Todo, err error) {
			err = db.QueryRow("SELECT id, done, label FROM todos WHERE id = ?", id).Scan(scanTodo(&todo)...)
			return
		},
		"filters": func() ([]Filter, error) {
			return RowsScanner(scanFilter)(db.Query(
				`WITH f(filter) AS (VALUES ('all'),('active'),('completed'))
				 SELECT filter, filter == (SELECT value FROM kv WHERE key = ?) FROM f`, FILTER_KEY))
		},
	})
}

func RowsScanner[R any](getDest func(*R) []interface{}) func(*sql.Rows, error) ([]R, error) {
	return func(rows *sql.Rows, _ error) (results []R, err error) {
		defer func() {
			cerr := rows.Close()
			if err == nil {
				err = cerr
			}
		}()
		var result R
		dest := getDest(&result)
		for rows.Next() {
			err = rows.Scan(dest...)
			if err != nil {
				return
			}
			results = append(results, result)
		}
		return
	}
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

		log.Printf("Handling route at %s : %s", r.URL.Path, routeId)

		if t := tmpl.Lookup(routeId); t != nil {
			r.ParseForm()
			err := t.Execute(w, r.Form)
			if err != nil {
				log.Print(err)
			}
		} else {
			http.NotFound(w, r)
		}
	}
}
