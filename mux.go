package restserver

import (
	"log"
	"net/http"
	"os"

	goji "goji.io"

	"github.com/gorilla/handlers"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"goji.io/pat"
)

func (s *Server) debugHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			log.Printf("%s %s", r.Method, r.URL)
			next.ServeHTTP(w, r)
		})
}

func (s *Server) logHandler(next http.Handler) http.Handler {
	accessLog, err := os.OpenFile(s.Log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Fatalf("error: %v", err)
	}

	return handlers.CombinedLoggingHandler(accessLog, next)
}

// NewHandler returns the master HTTP multiplexer/router.
func NewHandler(server Server) *goji.Mux {
	mux := goji.NewMux()

	if server.Debug {
		mux.Use(server.debugHandler)
	}

	if server.Log != "" {
		mux.Use(server.logHandler)
	}

	if server.Prometheus {
		mux.Handle(pat.Get("/metrics"), promhttp.Handler())
	}

	mux.HandleFunc(pat.Head("/config"), server.CheckConfig)
	mux.HandleFunc(pat.Head("/:repo/config"), server.CheckConfig)
	mux.HandleFunc(pat.Head("/*/:repo/config"), server.CheckConfig)

	mux.HandleFunc(pat.Get("/config"), server.GetConfig)
	mux.HandleFunc(pat.Get("/:repo/config"), server.GetConfig)
	mux.HandleFunc(pat.Get("/:folder/:repo/config"), server.GetConfig)

	mux.HandleFunc(pat.Post("/config"), server.SaveConfig)
	mux.HandleFunc(pat.Post("/:repo/config"), server.SaveConfig)
	mux.HandleFunc(pat.Post("/:folder/:repo/config"), server.SaveConfig)

	mux.HandleFunc(pat.Delete("/config"), server.DeleteConfig)
	mux.HandleFunc(pat.Delete("/:repo/config"), server.DeleteConfig)
	mux.HandleFunc(pat.Delete("/:folder/:repo/config"), server.DeleteConfig)

	mux.HandleFunc(pat.Get("/:type/"), server.ListBlobs)
	mux.HandleFunc(pat.Get("/:repo/:type/"), server.ListBlobs)
	mux.HandleFunc(pat.Get("/:folder/:repo/:type/"), server.ListBlobs)

	mux.HandleFunc(pat.Head("/:type/:name"), server.CheckBlob)
	mux.HandleFunc(pat.Head("/:repo/:type/:name"), server.CheckBlob)
	mux.HandleFunc(pat.Head("/:folder/:type/:name"), server.CheckBlob)
	mux.HandleFunc(pat.Head("/:folder/:repo/:type/:name"), server.CheckBlob)

	mux.HandleFunc(pat.Get("/:type/:name"), server.GetBlob)
	mux.HandleFunc(pat.Get("/:repo/:type/:name"), server.GetBlob)
	mux.HandleFunc(pat.Get("/:folder/:type/:name"), server.GetBlob)
	mux.HandleFunc(pat.Get("/:folder/:repo/:type/:name"), server.GetBlob)

	mux.HandleFunc(pat.Post("/:type/:name"), server.SaveBlob)
	mux.HandleFunc(pat.Post("/:repo/:type/:name"), server.SaveBlob)
	mux.HandleFunc(pat.Post("/:folder/:repo/:type/:name"), server.SaveBlob)

	mux.HandleFunc(pat.Delete("/:type/:name"), server.DeleteBlob)
	mux.HandleFunc(pat.Delete("/:repo/:type/:name"), server.DeleteBlob)
	mux.HandleFunc(pat.Delete("/:folder/:type/:name"), server.DeleteBlob)
	mux.HandleFunc(pat.Delete("/:folder/:repo/:type/:name"), server.DeleteBlob)

	mux.HandleFunc(pat.Post("/"), server.CreateRepo)
	mux.HandleFunc(pat.Post("/:repo/"), server.CreateRepo)

	mux.HandleFunc(pat.Post("/:folder/:repo/"), server.CreateRepo)

	return mux
}
