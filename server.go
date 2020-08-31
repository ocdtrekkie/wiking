package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"time"

	// Logging
	"github.com/unrolled/logger"

	// Stats/Metrics
	"github.com/rcrowley/go-metrics"
	"github.com/rcrowley/go-metrics/exp"
	"github.com/thoas/stats"

	"github.com/GeertJohan/go.rice"
	"github.com/julienschmidt/httprouter"
	"github.com/microcosm-cc/bluemonday"
	"github.com/russross/blackfriday/v2"
)

var (
	validPath = regexp.MustCompile("^/(edit|save|view)/([a-zA-Z0-9]+)$")
)

// Page ...
type Page struct {
	Title string
	Body  []byte
	HTML  template.HTML
	Brand string
	Date  time.Time
}

// make sure user input path does not leave the directory
func mkSubDir(dir string, file string) error {
	d := path.Clean(dir)
	sd := path.Dir(path.Clean(path.Join(d, file)))
	if sd[ 0:len(d) ] != d {
		return errors.New("File in wrong directory")
	}
	return os.MkdirAll(sd, 0755)
}

func (p *Page) Save(datadir string) error {
	filename := p.Title + FileExtension
	filepath := path.Join(datadir, filename)

	if err := mkSubDir(datadir, filename); err != nil {
		return err
	}

	return ioutil.WriteFile(filepath, p.Body, 0600)
}

// LoadPage ...
func LoadPage(title string, config Config, baseurl *url.URL) (*Page, error) {
	filename := path.Join(config.data, title + FileExtension)
	body, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(filename)
	if err != nil {
		return nil, err
	}
	mtime := fi.ModTime()

	// Process and Parse the Markdown content
	// Also automatically replace CamelCase page identifiers as links
	markdown := AutoCamelCase(body, baseurl.String())

	unsafe := blackfriday.Run(markdown, blackfriday.WithNoExtensions())
	html := bluemonday.UGCPolicy().SanitizeBytes(unsafe)

	return &Page{
		Title: title,
		Body:  body,
		HTML:  template.HTML(html),
		Brand: config.brand,
		Date:  mtime,
	}, nil
}

// Counters ...
type Counters struct {
	r metrics.Registry
}

func NewCounters() *Counters {
	counters := &Counters{
		r: metrics.NewRegistry(),
	}
	return counters
}

func (c *Counters) Inc(name string) {
	metrics.GetOrRegisterCounter(name, c.r).Inc(1)
}

func (c *Counters) Dec(name string) {
	metrics.GetOrRegisterCounter(name, c.r).Dec(1)
}

func (c *Counters) IncBy(name string, n int64) {
	metrics.GetOrRegisterCounter(name, c.r).Inc(n)
}

func (c *Counters) DecBy(name string, n int64) {
	metrics.GetOrRegisterCounter(name, c.r).Dec(n)
}

// Server ...
type Server struct {
	config    Config
	templates *Templates
	router    *httprouter.Router

	// Logger
	logger *logger.Logger

	// Stats/Metrics
	counters *Counters
	stats    *stats.Stats
}

func (s *Server) render(name string, w http.ResponseWriter, ctx interface{}) {
	buf, err := s.templates.Exec(name, ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}

	_, err = buf.WriteTo(w)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// IndexHandler ...
func (s *Server) IndexHandler() httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
		s.counters.Inc("n_index")

		u, err := url.Parse(fmt.Sprintf("./view/FrontPage"))
		if err != nil {
			http.Error(w, "Internal Error", http.StatusInternalServerError)
		}

		http.Redirect(w, r, r.URL.ResolveReference(u).String(), http.StatusFound)
	}
}

// EditHandler ...
func (s *Server) EditHandler() httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
		s.counters.Inc("n_edit")

		title := strings.TrimLeft(p.ByName("title"), "/")

		u, err := url.Parse("/view/")
		if err != nil {
			http.Error(w, "Internal Error", http.StatusInternalServerError)
		}
		baseurl := r.URL.ResolveReference(u)

		page, err := LoadPage(title, s.config, baseurl)
		if err != nil {
			page = &Page{Title: title, Brand: s.config.brand}
		}

		s.render("edit", w, page)
	}
}

// SaveHandler ...
func (s *Server) SaveHandler() httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
		s.counters.Inc("n_save")

		title := strings.TrimLeft(p.ByName("title"), "/")

		err := r.ParseForm()
		if err != nil {
			http.Error(w, "Internal Error", http.StatusInternalServerError)
			return
		}

		body := r.Form.Get("body")

		page := &Page{Title: title, Body: []byte(body), Brand: s.config.brand}
		err = page.Save(s.config.data)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		u, err := url.Parse(fmt.Sprintf("/view/%s", title))
		if err != nil {
			http.Error(w, "Internal Error", http.StatusInternalServerError)
		}

		http.Redirect(w, r, r.URL.ResolveReference(u).String(), http.StatusFound)
	}
}

// ViewHandler ...
func (s *Server) ViewHandler() httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
		s.counters.Inc("n_view")

		title := strings.TrimLeft(p.ByName("title"), "/")

		u, err := url.Parse("/view/")
		if err != nil {
			http.Error(w, "Internal Error", http.StatusInternalServerError)
		}
		baseurl := r.URL.ResolveReference(u)

		page, err := LoadPage(title, s.config, baseurl)
		if err != nil {
			u, err := url.Parse(fmt.Sprintf("/edit/%s", title))
			if err != nil {
				http.Error(w, "Internal Error", http.StatusInternalServerError)
			}

			http.Redirect(
				w, r, r.URL.ResolveReference(u).String(), http.StatusFound,
			)

			return
		}

		s.render("view", w, page)
	}
}

// StatsHandler ...
func (s *Server) StatsHandler() httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		bs, err := json.Marshal(s.stats.Data())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		w.Write(bs)
	}
}

// SearchHandler - handles searching for text in the wiki
func (s *Server) SearchHandler() httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if err := r.ParseForm(); err != nil {
			s.logger.Printf("ERROR: %s\n", err.Error())
			http.Error(w, "Internal Error", http.StatusInternalServerError)
		}
		bs, err := json.Marshal(s.DoSearch(r.FormValue("search")))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		w.Write(bs)
	}
}

// ListenAndServe ...
func (s *Server) ListenAndServe() {
	log.Fatal(
		http.ListenAndServe(
			s.config.bind,
			s.logger.Handler(
				s.stats.Handler(s.router),
			),
		),
	)
}

func (s *Server) initRoutes() {
	s.router.Handler("GET", "/debug/metrics", exp.ExpHandler(s.counters.r))
	s.router.GET("/debug/stats", s.StatsHandler())

	s.router.ServeFiles(
		"/css/*filepath",
		rice.MustFindBox("static/css").HTTPBox(),
	)

	s.router.ServeFiles(
		"/js/*filepath",
		rice.MustFindBox("static/js").HTTPBox(),
	)

	s.router.GET("/", s.IndexHandler())
	s.router.GET("/view/*title", s.ViewHandler())
	s.router.GET("/edit/*title", s.EditHandler())
	s.router.POST("/save/*title", s.SaveHandler())
	s.router.POST("/search", s.SearchHandler())
}

// NewServer ...
func NewServer(config Config) *Server {
	server := &Server{
		config:    config,
		router:    httprouter.New(),
		templates: NewTemplates("base"),

		// Logger
		logger: logger.New(logger.Options{
			Prefix:               "wiki",
			RemoteAddressHeaders: []string{"X-Forwarded-For"},
			OutputFlags:          log.LstdFlags,
		}),

		// Stats/Metrics
		counters: NewCounters(),
		stats:    stats.New(),
	}

	// Templates
	box := rice.MustFindBox("templates")

	editTemplate := template.New("view")
	template.Must(editTemplate.Parse(box.MustString("edit.html")))
	template.Must(editTemplate.Parse(box.MustString("base.html")))

	viewTemplate := template.New("view")
	template.Must(viewTemplate.Parse(box.MustString("view.html")))
	template.Must(viewTemplate.Parse(box.MustString("base.html")))

	server.templates.Add("edit", editTemplate)
	server.templates.Add("view", viewTemplate)

	/*
		err := server.templates.Load()
		if err != nil {
			log.Panicf("error loading templates: %s", err)
		}
	*/

	server.initRoutes()

	return server
}
