package extproc

import (
	"context"
	"io"
	"testing"
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/freepik-company/customrouter/pkg/routes"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

// fakeStream feeds Process a fixed list of messages and then blocks like a
// long-lived Envoy stream (WebSocket/SSE) would, until the test cancels it.
type fakeStream struct {
	grpc.ServerStream
	ctx  context.Context
	in   []*extprocv3.ProcessingRequest
	sent []*extprocv3.ProcessingResponse
}

func (f *fakeStream) Context() context.Context { return f.ctx }

func (f *fakeStream) Send(r *extprocv3.ProcessingResponse) error {
	f.sent = append(f.sent, r)
	return nil
}

func (f *fakeStream) Recv() (*extprocv3.ProcessingRequest, error) {
	if len(f.in) > 0 {
		r := f.in[0]
		f.in = f.in[1:]
		return r, nil
	}
	<-f.ctx.Done()
	return nil, io.EOF
}

type staticFinder struct{ route *routes.Route }

func (s staticFinder) FindRoute(string, routes.RequestMatch) *routes.Route { return s.route }

func requestHeaders() *extprocv3.ProcessingRequest {
	return &extprocv3.ProcessingRequest{Request: &extprocv3.ProcessingRequest_RequestHeaders{
		RequestHeaders: &extprocv3.HttpHeaders{Headers: &corev3.HeaderMap{Headers: []*corev3.HeaderValue{
			{Key: ":authority", RawValue: []byte("www.example.com")},
			{Key: ":path", RawValue: []byte("/app/ws")},
			{Key: ":method", RawValue: []byte("GET")},
		}}},
	}}
}

func responseHeaders() *extprocv3.ProcessingRequest {
	return &extprocv3.ProcessingRequest{Request: &extprocv3.ProcessingRequest_ResponseHeaders{
		ResponseHeaders: &extprocv3.HttpHeaders{},
	}}
}

// runProcess reports whether Process returned on its own; a stream that
// waits for Envoy blocks until the deadline and counts as still open.
func runProcess(t *testing.T, route *routes.Route, in ...*extprocv3.ProcessingRequest) (bool, *fakeStream) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	stream := &fakeStream{ctx: ctx, in: in}
	done := make(chan error, 1)
	go func() { done <- NewProcessor(staticFinder{route}, zap.NewNop(), false).Process(stream) }()
	select {
	case err := <-done:
		cancel()
		if err != nil {
			t.Fatalf("Process returned error: %v", err)
		}
		return true, stream
	case <-time.After(200 * time.Millisecond):
		cancel()
		<-done
		return false, stream
	}
}

func TestProcessClosesStreamAfterRequestHeaders(t *testing.T) {
	route := &routes.Route{Path: "/app", Type: routes.RouteTypePrefix, Backend: "pikaso-reverb.pikaso.svc.cluster.local:80"}

	closed, stream := runProcess(t, route, requestHeaders())
	if !closed {
		t.Fatal("stream left open after request headers: long-lived requests stay pinned to the pod")
	}
	if len(stream.sent) != 1 || stream.sent[0].GetRequestHeaders() == nil {
		t.Fatalf("expected one RequestHeaders response, got %v", stream.sent)
	}
}

func TestProcessClosesStreamWhenNoRouteMatches(t *testing.T) {
	if closed, _ := runProcess(t, nil, requestHeaders()); !closed {
		t.Fatal("stream left open after request headers with no matching route")
	}
}

func TestProcessKeepsStreamForResponseHeaderActions(t *testing.T) {
	route := &routes.Route{Path: "/app", Type: routes.RouteTypePrefix, Backend: "svc.ns.svc.cluster.local:80",
		Actions: []routes.RouteAction{{Type: routes.ActionTypeResponseHeaderSet, HeaderName: "x-test", Value: "1"}}}

	if closed, _ := runProcess(t, route, requestHeaders()); closed {
		t.Fatal("stream closed before the response phase of a route with response-header actions")
	}

	closed, stream := runProcess(t, route, requestHeaders(), responseHeaders())
	if !closed {
		t.Fatal("stream left open after the response phase")
	}
	if len(stream.sent) != 2 || stream.sent[1].GetResponseHeaders().GetResponse().GetHeaderMutation() == nil {
		t.Fatalf("expected the response header mutation, got %v", stream.sent)
	}
}
