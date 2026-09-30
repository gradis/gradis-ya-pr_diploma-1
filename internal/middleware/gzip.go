package middleware

import (
	"compress/gzip"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

var compressibleContentTypes = map[string]struct{}{
	"application/json": {},
	"text/plain":       {},
	"text/html":        {},
}

type gzipResponseWriter struct {
	gin.ResponseWriter
	writer *gzip.Writer
}

func Gzip() gin.HandlerFunc {
	return func(c *gin.Context) {
		contentEncoding := strings.TrimSpace(c.GetHeader("Content-Encoding"))
		if contentEncoding != "" && !strings.EqualFold(contentEncoding, "identity") {
			if !strings.EqualFold(contentEncoding, "gzip") {
				c.AbortWithStatus(http.StatusUnsupportedMediaType)
				return
			}

			originalBody := c.Request.Body
			reader, err := gzip.NewReader(originalBody)
			if err != nil {
				c.AbortWithStatus(http.StatusBadRequest)
				return
			}
			defer reader.Close()
			defer originalBody.Close()
			c.Request.Body = reader
		}

		if acceptsGzip(c.GetHeader("Accept-Encoding")) {
			writer := &gzipResponseWriter{ResponseWriter: c.Writer}
			c.Writer = writer

			defer func() {
				if writer.writer != nil {
					_ = writer.writer.Close()
				}
			}()
		}

		c.Next()
	}
}

func (w *gzipResponseWriter) Write(data []byte) (int, error) {
	if w.writer == nil && w.shouldCompress(data) {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Add("Vary", "Accept-Encoding")
		w.Header().Del("Content-Length")
		w.writer = gzip.NewWriter(w.ResponseWriter)
	}

	if w.writer != nil {
		return w.writer.Write(data)
	}

	return w.ResponseWriter.Write(data)
}

func (w *gzipResponseWriter) WriteString(value string) (int, error) {
	return w.Write([]byte(value))
}

func (w *gzipResponseWriter) shouldCompress(data []byte) bool {
	contentType := w.Header().Get("Content-Type")
	if contentType == "" {
		contentType = http.DetectContentType(data)
		w.Header().Set("Content-Type", contentType)
	}

	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}

	_, ok := compressibleContentTypes[mediaType]
	return ok
}

func acceptsGzip(header string) bool {
	gzipQuality := -1.0
	wildcardQuality := -1.0

	for _, item := range strings.Split(header, ",") {
		parts := strings.Split(strings.TrimSpace(item), ";")
		coding := strings.ToLower(strings.TrimSpace(parts[0]))
		if coding != "gzip" && coding != "*" {
			continue
		}

		quality := 1.0
		for _, parameter := range parts[1:] {
			key, value, ok := strings.Cut(strings.TrimSpace(parameter), "=")
			if !ok || !strings.EqualFold(strings.TrimSpace(key), "q") {
				continue
			}

			parsedQuality, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
			if err != nil || parsedQuality < 0 || parsedQuality > 1 {
				quality = 0
			} else {
				quality = parsedQuality
			}
		}

		if coding == "gzip" {
			gzipQuality = quality
		} else {
			wildcardQuality = quality
		}
	}

	if gzipQuality >= 0 {
		return gzipQuality > 0
	}

	return wildcardQuality > 0
}
