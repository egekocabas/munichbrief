package processing

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func callOllamaForTimeoutTest(ctx context.Context, client *OllamaClient, endpoint string) error {
	if endpoint == "chat" {
		_, _, err := client.chat(ctx, false, "Translate the supplied text.", "Text", nil)
		return err
	}
	_, _, err := client.rawGenerate(ctx, "Text", chatOptions{NumCtx: 2048})
	return err
}

func TestOllamaRequestDeadlines(t *testing.T) {
	for _, endpoint := range []string{"chat", "raw"} {
		for _, phase := range []string{"headers", "body"} {
			for _, deadline := range []string{"client", "parent", "canceled"} {
				t.Run(endpoint+"/"+phase+"/"+deadline, func(t *testing.T) {
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					clientTimeout := time.Second
					wantKind, wantCause := ErrorTransient, context.DeadlineExceeded
					switch deadline {
					case "client":
						clientTimeout = 50 * time.Millisecond
						wantKind = ErrorTimeout
					case "parent":
						var stop context.CancelFunc
						ctx, stop = context.WithTimeout(ctx, 50*time.Millisecond)
						defer stop()
					case "canceled":
						wantCause = context.Canceled
					}
					transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
						if phase == "body" {
							body := &waitingOllamaResponseBody{ctx: request.Context()}
							if deadline == "canceled" {
								body.cancel = cancel
							}
							return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
						}
						if deadline == "canceled" {
							cancel()
						}
						<-request.Context().Done()
						return nil, request.Context().Err()
					})
					client, err := NewOllamaClient("http://ollama.test", "test", clientTimeout, 2048, &http.Client{Transport: transport})
					if err != nil {
						t.Fatal(err)
					}
					err = callOllamaForTimeoutTest(ctx, client, endpoint)
					if err == nil || KindOf(err) != wantKind || !errors.Is(err, wantCause) {
						t.Fatalf("request failure = %v (kind %q), want %q wrapping %v", err, KindOf(err), wantKind, wantCause)
					}
					if deadline == "client" {
						operation := "call Ollama:"
						if phase == "body" {
							operation = "read ollama response:"
						}
						if ctx.Err() != nil || !strings.HasPrefix(err.Error(), operation) {
							t.Fatalf("provider deadline did not fail in expected %s phase: context=%v error=%v", phase, ctx.Err(), err)
						}
					}
				})
			}
		}
	}
}

type waitingOllamaResponseBody struct {
	ctx    context.Context
	cancel context.CancelFunc
}

func (body *waitingOllamaResponseBody) Read([]byte) (int, error) {
	if body.cancel != nil {
		body.cancel()
	}
	<-body.ctx.Done()
	return 0, body.ctx.Err()
}

func (*waitingOllamaResponseBody) Close() error { return nil }

type failingOllamaResponseBody struct{ err error }

func (body failingOllamaResponseBody) Read([]byte) (int, error) { return 0, body.err }
func (body failingOllamaResponseBody) Close() error             { return nil }

func TestOllamaTransportFailuresRemainTransient(t *testing.T) {
	for _, endpoint := range []string{"chat", "raw"} {
		for _, phase := range []string{"headers", "body"} {
			t.Run(endpoint+"/"+phase, func(t *testing.T) {
				cause := errors.New("connection interrupted")
				transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
					if phase == "headers" {
						return nil, cause
					}
					return &http.Response{StatusCode: http.StatusOK, Body: failingOllamaResponseBody{err: cause}}, nil
				})
				client, err := NewOllamaClient("http://ollama.test", "test", time.Second, 2048, &http.Client{Transport: transport})
				if err != nil {
					t.Fatal(err)
				}
				err = callOllamaForTimeoutTest(context.Background(), client, endpoint)
				if err == nil || KindOf(err) != ErrorTransient || !errors.Is(err, cause) {
					t.Fatalf("transport failure = %v, want transient wrapping original cause", err)
				}
			})
		}
	}
}

func TestOllamaHTTPFailuresRetainClassification(t *testing.T) {
	for _, endpoint := range []string{"chat", "raw"} {
		for _, test := range []struct {
			status int
			kind   ErrorKind
		}{{http.StatusRequestTimeout, ErrorTransient}, {http.StatusTooManyRequests, ErrorTransient}, {http.StatusServiceUnavailable, ErrorTransient}, {http.StatusNotFound, ErrorConfiguration}} {
			t.Run(endpoint+"/"+http.StatusText(test.status), func(t *testing.T) {
				transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: test.status, Body: io.NopCloser(strings.NewReader("not logged"))}, nil
				})
				client, err := NewOllamaClient("http://ollama.test", "test", time.Second, 2048, &http.Client{Transport: transport})
				if err != nil {
					t.Fatal(err)
				}
				err = callOllamaForTimeoutTest(context.Background(), client, endpoint)
				if err == nil || KindOf(err) != test.kind {
					t.Fatalf("HTTP %d failure = %v (kind %q), want %q", test.status, err, KindOf(err), test.kind)
				}
			})
		}
	}
}
