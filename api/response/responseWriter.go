package response

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/nicolastakashi/prom-analytics-proxy/api/models"
)

type responseWriter struct {
	http.ResponseWriter
	statusCode int
	body       *bytes.Buffer
}

func NewResponseWriter(w http.ResponseWriter) *responseWriter {
	return &responseWriter{w, http.StatusOK, &bytes.Buffer{}}
}

// WriteHeader to capture status code
func (rw *responseWriter) WriteHeader(statusCode int) {
	rw.statusCode = statusCode
	rw.ResponseWriter.WriteHeader(statusCode)
}

// Write to capture body
func (rw *responseWriter) Write(b []byte) (int, error) {
	rw.body.Write(b)                  // Write to buffer
	return rw.ResponseWriter.Write(b) // Write response to client
}

func (recw *responseWriter) ParseQueryResponse(includeQueryStats bool) *models.Response {
	if !includeQueryStats {
		return nil
	}

	var reader io.Reader = recw.body
	var err error

	if strings.Contains(recw.ResponseWriter.Header().Get("Content-Encoding"), "gzip") {
		reader, err = gzip.NewReader(recw.body)
		if err != nil {
			slog.Error("unable to create gzip reader", "err", err)
			return nil
		}
	}

	var response models.Response
	if err = json.NewDecoder(reader).Decode(&response); err != nil {
		slog.Error("unable to decode response body", "err", err)
		return nil
	}

	if response.Status != "success" {
		slog.Debug("query did not succeed", "status", response.Status)
		return nil
	}

	return &response
}

func (recw *responseWriter) GetStatusCode() int {
	return recw.statusCode
}

func (recw *responseWriter) GetBodySize() int {
	return recw.body.Len()
}
