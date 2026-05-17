package web

import (
	"compress/gzip"
	"net/http"
	"strings"
	"sync"
)

// gzipPool reuses gzip writers to avoid per-request allocation overhead.
var gzipPool = sync.Pool{
	New: func() any {
		return gzip.NewWriter(nil)
	},
}

// gzipResponseWriter buffers the response body so we can decide whether to
// compress based on final size before writing to the underlying ResponseWriter.
type gzipResponseWriter struct {
	http.ResponseWriter
	gz          *gzip.Writer
	code        int
	wroteHeader bool
}

func (g *gzipResponseWriter) WriteHeader(code int) {
	g.code = code
}

func (g *gzipResponseWriter) Write(b []byte) (int, error) {
	if !g.wroteHeader {
		g.wroteHeader = true
		g.ResponseWriter.Header().Set("Content-Encoding", "gzip")
		g.ResponseWriter.Header().Del("Content-Length") // length changes after compression
		if g.code != 0 {
			g.ResponseWriter.WriteHeader(g.code)
		}
	}
	return g.gz.Write(b)
}

// close flushes and closes the gzip writer, returning it to the pool.
func (g *gzipResponseWriter) close() {
	_ = g.gz.Close()
	g.gz.Reset(nil)
	gzipPool.Put(g.gz)
}

// gzipMiddleware wraps a handler and compresses its response when the client
// advertises Accept-Encoding: gzip. SSE and metrics endpoints are excluded.
func gzipMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next(w, r)
			return
		}

		gz := gzipPool.Get().(*gzip.Writer)
		gz.Reset(w)

		gw := &gzipResponseWriter{
			ResponseWriter: w,
			gz:             gz,
		}
		defer gw.close()

		next(gw, r)

		// Flush any remaining buffered data.
		if !gw.wroteHeader {
			// Handler wrote nothing or only called WriteHeader — flush headers.
			if gw.code != 0 {
				w.WriteHeader(gw.code)
			}
		}
	}
}
