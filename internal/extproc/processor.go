/*
Copyright 2024-2026 Freepik Company S.L.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package extproc implements the Envoy external processor for custom routing.
package extproc

import (
	"fmt"
	"io"
	"strconv"
	"time"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/freepik-company/customrouter/pkg/routes"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RouteFinder is an interface for finding routes
type RouteFinder interface {
	FindRoute(host string, req routes.RequestMatch) *routes.Route
}

// Processor implements the Envoy external processor service
type Processor struct {
	extprocv3.UnimplementedExternalProcessorServer
	routeFinder      RouteFinder
	logger           *zap.Logger
	accessLogEnabled bool
}

// NewProcessor creates a new external processor
func NewProcessor(routeFinder RouteFinder, logger *zap.Logger, accessLogEnabled bool) *Processor {
	return &Processor{
		routeFinder:      routeFinder,
		logger:           logger,
		accessLogEnabled: accessLogEnabled,
	}
}

// requestContext holds information about the current request for logging
type requestContext struct {
	startTime        time.Time
	authority        string
	path             string
	method           string
	matchedBackend   string
	matchedPattern   string
	matchedType      string
	matchedPriority  int32
	routeFound       bool
	processingTimeNs int64
}

// streamContext is the per-stream state shared across ext_proc phases
// (RequestHeaders → ResponseHeaders). Envoy opens one stream per request,
// so the matched route selected in the request phase is the one whose
// response-side actions must be applied when the response headers arrive.
type streamContext struct {
	// matchedRoute is the route selected during processRequestHeaders, or nil
	// if no route matched. Read-only after the request phase completes.
	matchedRoute *routes.Route

	// vars carries the request-time variable context (path, host, method, …)
	// so response-side header actions can expand ${...} placeholders using
	// the same source of truth as request-side actions. Read-only after the
	// request phase completes.
	vars *requestVars
}

// Process handles the bidirectional stream from Envoy
func (p *Processor) Process(stream extprocv3.ExternalProcessor_ProcessServer) error {
	streamCtx := &streamContext{}
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return status.Errorf(codes.Internal, "failed to receive request: %v", err)
		}

		resp, reqCtx, err := p.processRequest(req, streamCtx)
		if err != nil {
			processingErrorsTotal.Inc()
			p.logger.Error("failed to process request", zap.Error(err))
			return err
		}

		if resp != nil {
			if err := stream.Send(resp); err != nil {
				return status.Errorf(codes.Internal, "failed to send response: %v", err)
			}
		}

		// Log access after sending response
		if p.accessLogEnabled && reqCtx != nil {
			p.logAccess(reqCtx)
		}
	}
}

func (p *Processor) logAccess(ctx *requestContext) {
	ctx.processingTimeNs = time.Since(ctx.startTime).Nanoseconds()

	// Record metrics
	found := strconv.FormatBool(ctx.routeFound)
	durationSec := float64(ctx.processingTimeNs) / 1e9
	requestsTotal.WithLabelValues(found).Inc()
	requestDuration.WithLabelValues(found).Observe(durationSec)
	if ctx.routeFound {
		routeMatchesTotal.WithLabelValues(ctx.matchedType).Inc()
	} else {
		routeNotFoundTotal.Inc()
	}

	if ctx.routeFound {
		p.logger.Info("access",
			zap.String("original_authority", ctx.authority),
			zap.String("new_authority", ctx.matchedBackend),
			zap.String("path", ctx.path),
			zap.String("method", ctx.method),
			zap.String("matched_pattern", ctx.matchedPattern),
			zap.String("matched_type", ctx.matchedType),
			zap.Int32("matched_priority", ctx.matchedPriority),
			zap.Bool("route_found", true),
			zap.Int64("processing_time_ns", ctx.processingTimeNs),
		)
	} else {
		p.logger.Info("access",
			zap.String("original_authority", ctx.authority),
			zap.String("path", ctx.path),
			zap.String("method", ctx.method),
			zap.Bool("route_found", false),
			zap.Int64("processing_time_ns", ctx.processingTimeNs),
		)
	}
}

func (p *Processor) processRequest(req *extprocv3.ProcessingRequest, streamCtx *streamContext) (*extprocv3.ProcessingResponse, *requestContext, error) {
	// Debug: log request type
	p.logger.Debug("processRequest called",
		zap.String("request_type", fmt.Sprintf("%T", req.Request)),
	)

	switch r := req.Request.(type) {
	case *extprocv3.ProcessingRequest_RequestHeaders:
		p.logger.Debug("handling RequestHeaders")
		return p.processRequestHeaders(r.RequestHeaders, streamCtx)

	case *extprocv3.ProcessingRequest_ResponseHeaders:
		p.logger.Debug("handling ResponseHeaders")
		return p.processResponseHeaders(streamCtx), nil, nil

	case *extprocv3.ProcessingRequest_RequestBody:
		p.logger.Debug("handling RequestBody")
		// We don't process request body
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_RequestBody{
				RequestBody: &extprocv3.BodyResponse{},
			},
		}, nil, nil

	case *extprocv3.ProcessingRequest_ResponseBody:
		p.logger.Debug("handling ResponseBody")
		// We don't process response body
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_ResponseBody{
				ResponseBody: &extprocv3.BodyResponse{},
			},
		}, nil, nil

	default:
		p.logger.Debug("handling unknown request type")
		return nil, nil, nil
	}
}
