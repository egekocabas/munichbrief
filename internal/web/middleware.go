package web

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (s *Server) accessBoundary(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		// The deployment may route public and review traffic to the same process.
		// Host classification therefore happens before dispatch, so an accidentally
		// exposed admin route still returns 404 on a configured public hostname.
		adminPath := request.URL.Path == "/admin" || strings.HasPrefix(request.URL.Path, "/admin/") || request.URL.Path == "/api/admin" || strings.HasPrefix(request.URL.Path, "/api/admin/")
		if adminPath && (!s.options.AdminEnabled || s.isPublicRequest(request)) {
			http.NotFound(response, request)
			return
		}
		if s.isPublicRequest(request) && !s.isPublicPath(request.URL.Path) {
			http.NotFound(response, request)
			return
		}
		next.ServeHTTP(response, request)
	})
}

func (s *Server) isPublicRequest(request *http.Request) bool {
	host := requestHostname(request)
	for _, publicHost := range s.options.PublicHosts {
		if strings.EqualFold(host, publicHost) {
			return true
		}
	}
	return false
}

func (s *Server) health(response http.ResponseWriter, _ *http.Request) {
	response.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = response.Write([]byte("ok\n"))
}

func (s *Server) ready(response http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), time.Second)
	defer cancel()
	if err := s.store.Ready(ctx); err != nil {
		s.logger.ErrorContext(request.Context(), "readiness check failed", "error", err)
		http.Error(response, "not ready", http.StatusServiceUnavailable)
		return
	}
	response.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = response.Write([]byte("ready\n"))
}

func (s *Server) internalError(response http.ResponseWriter, request *http.Request, message string, err error) {
	s.logger.ErrorContext(request.Context(), message, "error", err)
	http.Error(response, "internal server error", http.StatusInternalServerError)
}

func (s *Server) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		startedAt := time.Now()
		requestID := validIdentifier(request.Header.Get("X-Request-ID"))
		if requestID == "" {
			requestID = randomIdentifier()
		}
		correlationID := validIdentifier(request.Header.Get("X-Correlation-ID"))
		if correlationID == "" {
			correlationID = requestID
		}
		response.Header().Set("X-Request-ID", requestID)
		response.Header().Set("X-Correlation-ID", correlationID)
		response.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		response.Header().Set("Referrer-Policy", "same-origin")
		response.Header().Set("X-Frame-Options", "DENY")
		recorder := &statusRecorder{ResponseWriter: response, status: http.StatusOK}
		next.ServeHTTP(recorder, request)
		s.logger.InfoContext(request.Context(), "http request", "method", request.Method, "path", request.URL.Path, "status", recorder.status, "duration_ms", time.Since(startedAt).Milliseconds(), "request_id", requestID, "correlation_id", correlationID)
	})
}

func normalizePublicHosts(values []string) ([]string, error) {
	hosts := make([]string, 0, len(values))
	seen := make(map[string]struct{})
	for _, value := range values {
		host := strings.ToLower(strings.TrimSpace(value))
		parsed, err := url.Parse("//" + host)
		if host == "" || err != nil || parsed.Hostname() != host || parsed.Port() != "" || strings.ContainsAny(host, "/@") {
			return nil, errors.New("public hosts must contain only hostnames without schemes, credentials, paths, or ports")
		}
		if _, exists := seen[host]; exists {
			continue
		}
		seen[host] = struct{}{}
		hosts = append(hosts, host)
	}
	return hosts, nil
}

func requestHostname(request *http.Request) string {
	host := request.Host
	if parsed, err := url.Parse("//" + host); err == nil && parsed.Hostname() != "" {
		return strings.ToLower(parsed.Hostname())
	}
	return strings.ToLower(host)
}

func (s *Server) isPublicPath(path string) bool {
	if path == "/" || path == "/about" || path == "/ai-disclosure/acknowledge" || path == "/healthz" || path == "/readyz" || path == "/robots.txt" || path == "/sitemap.xml" {
		return true
	}
	prefixes := []string{"/incidents", "/social", "/static"}
	for _, definition := range s.languages {
		prefixes = append(prefixes, "/"+definition.Code)
	}
	for _, prefix := range prefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.wroteHeader {
		return
	}
	r.status = status
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(contents []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(contents)
}

func validIdentifier(value string) string {
	if value == "" || len(value) > 128 {
		return ""
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' || character == '_' || character == '.' {
			continue
		}
		return ""
	}
	return value
}

func randomIdentifier() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes[:])
}
