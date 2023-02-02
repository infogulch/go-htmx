# About

This repo demonstrates how to build an interactive web application with NO
(*user-written) javascript, using just these tools:

- [htmx.org](https://htmx.org) javascript library
- The `http` and `html/template` modules from Go's standard library

Some notable features and anti-features:

- NO js build tooling
- NO duplicate browser / api endpoints. Actually, no api endpoint at all.

## What is HTMX?

HTMX is a library that lets you juice up regular HTML to increase
interactivity by adding annotations to nodes in the DOM. To quote
[the homepage](https://htmx.org/):

> ### motivation
> 
> - Why should only \<a\> and \<form\> be able to make HTTP requests?
> - Why should only click & submit events trigger them?
> - Why should only GET & POST methods be available?
> - Why should you only be able to replace the entire screen?
> 
> By removing these arbitrary constraints, htmx completes HTML as a hypertext
> 
> ### quick start
> 
> ```html
> <script src="https://unpkg.com/htmx.org@1.8.5"></script>
> <!-- have a button POST a click via AJAX -->
> <button hx-post="/clicked" hx-swap="outerHTML">
>   Click Me
> </button>
> ```
> 
> The hx-post and hx-swap attributes on this button tell htmx:
> 
> > **"When a user clicks on this button, issue an AJAX request to /clicked,
> > and replace the entire button with the HTML response"**

Yes it's that simple. When the browser loads a page it returns regular
HTML (with `hx-*` attributes and the htmx library in a script reference). The
user's action triggers an AJAX request, to which the server just responds with
more HTML, which is injected/replaced on the existing page (with no flashing).
For a more thorough explanation, check out the [docs introduction, *Htmx in a
Nutshell*](https://htmx.org/docs/#introduction).

The implementation of an interactive htmx page tends to be an html page written
in some template syntax, plus a bunch of 'partials' extracted from it to update
parts of it in reaction to certain actions taken by the user.

## Go templates?

Go's [`text/template`][text-template] and [`html/template`][html-template]
modules are well suited to this task because of the ability to define multiple
[nested template definitions][nested-template] with the `{{define ...}}`
(define at the top level) and `{{block ...}}` (define and use inline)
directives. Combining this with the ability to choose which files to include
in a template collection and [`FuncMap`][funcmap] to provide control over
behavior directly to the template, this makes a very flexible but still
type safe environment.

[text-template]: https://pkg.go.dev/text/template#section-documentation
[html-template]: https://pkg.go.dev/html/template#section-documentation
[nested-template]: https://pkg.go.dev/text/template#hdr-Nested_template_definitions
[funcmap]: https://pkg.go.dev/text/template#FuncMap

# Design

These parts look promising individually, but they need to be combined into
something functional. This is how it's done in this repo:

Each interactive page / route is its own template collection. You provide
functionality to the the page via FuncMap functions that any template in 
the collection can call. When a request is received on a particular route
the details of the request are used to determine which template definition
to invoke in response; the template can call FuncMap functions to perform
behaviors and get data, and they render an html response.

For example, when the index handler is created, its template collection 
consists of `layout.html` and `index.html`. When a regular browser page
request comes in at the index route, it executes the `"get"` template,
defined in `layout.html`, which in turn invokes the `"body"` template,
defined in `index.html` to render the rest of the page.

This kind of design is great for sharing and DRYing your html. For example
`layout.html` is reused by the `/todos` handler, where the `todos.html`
file redefines the `"body"` template to be the todos page instead. Thus
they share a common base layout page but have different body content.
Also, since both pages define a `"hx-get-nav"` template definition, when
a request comes in with the `HX-Request=true` header and `?nav` query
string, *just the `"body"`* template is rendered out to the client,
meaning it doesn't rerender the rest of the layout, just the part of the
DOM that changed.

Check out `main.go:TemplateHandler` docs for more details about how requests
are used to generate a routeId which is looked up which template to
out of the template collection to render in response.

## Goals

- Explore this way to use htmx with Go and Go templates
- Maximize cool factor, something like: slickness / (lines + complexity)
- Show a system that is complete enough that it's easy to imagine being
  useful for real projects

## Ideas

- Demo
	- [ ] Deploy a live  running version of the demo for people to test without having
	      to run it locally.
		- One list per user, or one global list for everyone?
		- Web server (probably Caddy)
		- Periodic cleanup to prevent abuse
	- [x] Clean up CSS
	- [ ] Placeholder for empty list https://blog.mathieu-leplatre.info/placeholder-for-empty-lists-in-css.html
	- [ ] Use CSS transitions when making htmx changes to the DOM
	- [ ] Websocket to update other tabs when changes are made
- Framework Features
	- [ ] Embed template and static files into the binary for single file deployments
		- [ ] Export embedded files to the filesystem
		- [ ] Override embedded templates with ones on the filesystem
	- [x] Automatic SQL migrations
	- [ ] Watch templates dir for changes and automatically reload on change https://stackoverflow.com/questions/57601700/how-to-change-the-handler-in-http-handle-after-server-start
	- [ ] See if DI is useful; https://github.com/infogulch/inject
- Performance
  - [ ] Serve pre-compressed static assets https://dev.to/vearutop/serving-compressed-static-assets-with-http-in-go-1-16-55bb
  - [ ] Optimize sqlite pragmas: wal, syncronous=NORMAL, foreign keys, strict, trusted_schema=OFF. See: https://pkg.go.dev/github.com/mattn/go-sqlite3?utm_source=godoc#SQLiteDriver.Open
  - [ ] Minify template text on load to reduce bandwidth. https://github.com/tdewolff/minify

