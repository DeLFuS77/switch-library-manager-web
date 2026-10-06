package web

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"sync"
)

// withCompression gzips pages, styles, scripts and JSON for browsers that accept it, which
// makes pages load much faster over Wi-Fi or from a phone. Images and game files are
// already compressed and are sent as they are, as are range requests and live events.
func withCompression(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") || r.Header.Get("Range") != "" ||
			r.Method == http.MethodHead || strings.HasSuffix(r.URL.Path, "/events") {
			next.ServeHTTP(w, r)
			return
		}
		writer := &gzipResponseWriter{ResponseWriter: w}
		defer writer.Close()
		next.ServeHTTP(writer, r)
	})
}

var gzipWriters = sync.Pool{New: func() any {
	writer, _ := gzip.NewWriterLevel(io.Discard, gzip.DefaultCompression)
	return writer
}}

// compressible content types; everything else (images, ZIP, NSP...) is sent unchanged
func compressible(contentType string) bool {
	contentType = strings.ToLower(contentType)
	for _, prefix := range []string{"text/", "application/json", "application/javascript", "application/manifest+json", "image/svg+xml"} {
		if strings.HasPrefix(contentType, prefix) {
			return true
		}
	}
	return false
}

// gzipResponseWriter decides when the headers are written whether to compress.
type gzipResponseWriter struct {
	http.ResponseWriter
	gzip        *gzip.Writer
	wroteHeader bool
}

func (w *gzipResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	headers := w.Header()
	headers.Add("Vary", "Accept-Encoding")
	if status != http.StatusNoContent && status != http.StatusNotModified && headers.Get("Content-Encoding") == "" &&
		compressible(headers.Get("Content-Type")) {
		headers.Set("Content-Encoding", "gzip")
		headers.Del("Content-Length")
		w.gzip = gzipWriters.Get().(*gzip.Writer)
		w.gzip.Reset(w.ResponseWriter)
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *gzipResponseWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", http.DetectContentType(data))
		}
		w.WriteHeader(http.StatusOK)
	}
	if w.gzip != nil {
		return w.gzip.Write(data)
	}
	return w.ResponseWriter.Write(data)
}

// Flush sends what was written so far, for responses streamed in parts.
func (w *gzipResponseWriter) Flush() {
	if w.gzip != nil {
		w.gzip.Flush()
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *gzipResponseWriter) Close() {
	if w.gzip != nil {
		w.gzip.Close()
		gzipWriters.Put(w.gzip)
		w.gzip = nil
	}
}
