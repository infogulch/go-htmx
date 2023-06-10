package main

import (
	"context"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/cozodb/cozo-lib-go"
	"golang.org/x/exp/maps"
)

var config = struct {
	ShutdownDelayTolerance time.Duration
	ReloadDebounceDelay    time.Duration
}{
	ShutdownDelayTolerance: 5 * time.Second,
	ReloadDebounceDelay:    100 * time.Millisecond,
}

func main() {
	watcher, err := NewWatcher("./static", "./templates")
	if err != nil {
		log.Fatal(err)
	}
	defer watcher.Close()

	sigterm := make(chan os.Signal)
	signal.Notify(sigterm, os.Interrupt, syscall.SIGINT)
	signal.Notify(sigterm, os.Interrupt, syscall.SIGKILL)
	signal.Notify(sigterm, os.Interrupt, syscall.SIGUSR1)
	defer signal.Reset()

	handler, err := NewHandler()
	if err != nil {
		log.Fatal(err)
	}

server:
	for {
		server := http.Server{
			Handler: handler,
			Addr:    "0.0.0.0:8080",
		}

		// setup event handler
		action := make(chan string)
		go func() {
			act := ""
			select {
			case event, ok := <-watcher.Events:
				log.Printf("Restarting server due to file changed: %s %s (ok:%t)", event.Op, event.Name, ok)
				act = "reload"
			case sig := <-sigterm:
				log.Printf("Shutting down server due to %s", sig)
				act = "shutdown"
			}
			ctx, cancel := context.WithTimeout(context.Background(), config.ShutdownDelayTolerance)
			err := server.Shutdown(ctx)
			log.Printf("Server shut down: %v", err)
			action <- act
			cancel()
		}()

		log.Println("Starting server...")
		err = server.ListenAndServe()
		if err != http.ErrServerClosed {
			log.Fatal(err)
		}

		switch <-action {
		case "reload":
			watcher.Debounce(config.ReloadDebounceDelay)
			newHandler, err := NewHandler()
			if err != nil {
				log.Printf("Failed to make a new server, restarting previous server. Error: %e", err)
			} else {
				handler = newHandler
			}
			continue server
		case "shutdown":
			break server
		default:
			break server
		}
	}
	log.Println("Bye")
}

func NewHandler() (http.Handler, error) {
	db, err := cozo.New("sqlite", "todos.db", nil)
	if err != nil {
		return nil, err
	}

	funcs := NewFuncs(db)

	handler := http.NewServeMux()

	// Serve files from static dir
	if staticFS, err := fs.Sub(Files, "static"); err == nil {
		fileServer := http.FileServer(http.FS(staticFS))
		// fileServer := statigz.FileServer(staticFS, brotli.AddEncoding)
		handler.Handle("/static/", http.StripPrefix("/static/", fileServer))
	}

	// find var Files in embed.go/embed0.go
	templateFS, err := fs.Sub(Files, "templates")
	if err != nil {
		return nil, err
	}
	// Set up template files
	sharedFiles, err := fs.Glob(templateFS, "_*.html")
	if err != nil {
		return nil, err
	}
	sort.Strings(sharedFiles)
	pageFiles, err := fs.Glob(templateFS, "[^_]*.html")
	if err != nil {
		return nil, err
	}
	// log.Printf("Found template files: shared: %+v; pages: %+v", sharedFiles, pageFiles)

	for _, pageFile := range pageFiles {
		var path string
		if filepath.Base(pageFile) == "index.html" {
			path = filepath.Clean(filepath.Join("/", filepath.Dir(pageFile), "/"))
		} else {
			path = "/" + filepath.Clean(pageFile)
		}
		route := strings.TrimSuffix(path, filepath.Ext(path))
		files := append(append([]string(nil), sharedFiles...), pageFile)
		pageHandler, err := TemplateHandler(templateFS, files, funcs)
		if err != nil {
			return nil, err
		}
		handler.Handle(route, pageHandler)
	}

	return handler, nil
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
func TemplateHandler(fs fs.FS, files []string, funcs template.FuncMap) (http.HandlerFunc, error) {
	name := files[len(files)-1]
	tmpl, err := template.New(name).Funcs(funcs).ParseFS(fs, files...)
	if err != nil {
		return nil, err
	}
	// log.Printf("Setting up handler for %v", files)
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))

		if t := tmpl.Lookup("init-" + name); t != nil {
			// log.Printf("Initializing %s", name)
			err = t.Execute(io.Discard, nil)
			if err != nil {
				return nil, err
			}
		}
	}
	return func(w http.ResponseWriter, r *http.Request) {
		routeId := GetRouteId(r)

		var err error
		defer func(start time.Time) {
			log.Printf("Handled request %s?%s in %v. Error: %v\n", r.URL.Path, routeId, time.Since(start), err)
		}(time.Now())

		if t := tmpl.Lookup(routeId); t != nil {
			r.ParseForm()
			data := struct {
				Method   string
				URL      *url.URL
				Header   http.Header
				Form     url.Values
				PostForm url.Values
				Body     io.ReadCloser
				// Future: User
			}{
				Method:   r.Method,
				URL:      r.URL,
				Header:   r.Header,
				Form:     r.Form,
				PostForm: r.PostForm,
				Body:     r.Body,
			}
			err = t.Execute(w, data)
		} else {
			http.NotFound(w, r)
		}
	}, nil
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
	{ // filter out url parameters that start with _
		i := 0
		for j := 0; j < len(keys); j++ {
			if !strings.HasPrefix(keys[j], "_") {
				keys[i] = keys[j]
				i++
			}
		}
		keys = keys[:i]
	}

	routeparts := append([]string{prefix, r.Method}, keys...)
	return strings.ToLower(strings.Join(routeparts, "-"))
}

func must[T any](t T, err error) T {
	if err != nil {
		log.Fatal(err)
	}
	return t
}
