/*
Copyright 2024.

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

package extproc

import (
	"fmt"
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"go.uber.org/zap"
)

// processRequestHeaders handles incoming request headers and determines routing
func (p *Processor) processRequestHeaders(headers *extprocv3.HttpHeaders) (*extprocv3.ProcessingResponse, *requestContext, error) {
	reqCtx := &requestContext{
		startTime: time.Now(),
	}

	// Debug: log complete headers structure
	p.logger.Debug("processRequestHeaders called",
		zap.Bool("headers_nil", headers == nil),
	)

	if headers != nil {
		p.logger.Debug("HttpHeaders structure",
			zap.Bool("headers.Headers_nil", headers.Headers == nil),
			zap.String("end_of_stream", fmt.Sprintf("%v", headers.EndOfStream)),
		)

		if headers.Headers != nil {
			p.logger.Debug("HeaderMap info",
				zap.Int("header_count", len(headers.Headers.Headers)),
			)
			for i, h := range headers.Headers.Headers {
				p.logger.Debug("header entry",
					zap.Int("index", i),
					zap.String("key", h.Key),
					zap.String("value", h.Value),
					zap.ByteString("raw_value", h.RawValue),
				)
			}
		}
	}

	// Extract host, path, and method from headers
	if headers != nil && headers.Headers != nil {
		for _, h := range headers.Headers.Headers {
			// Value can be in Value (string) or RawValue (bytes)
			value := h.Value
			if value == "" && len(h.RawValue) > 0 {
				value = string(h.RawValue)
			}

			switch h.Key {
			case ":authority":
				reqCtx.authority = value
			case ":path":
				reqCtx.path = value
			case ":method":
				reqCtx.method = value
			}
		}
	}

	p.logger.Debug("extracted values",
		zap.String("authority", reqCtx.authority),
		zap.String("path", reqCtx.path),
		zap.String("method", reqCtx.method),
	)

	// Find matching route
	route := p.routeFinder.FindRoute(reqCtx.authority, reqCtx.path)
	if route == nil {
		p.logger.Debug("no matching route found",
			zap.String("host", reqCtx.authority),
			zap.String("path", reqCtx.path),
		)
		reqCtx.routeFound = false
		// No matching route, continue without modification
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_RequestHeaders{
				RequestHeaders: &extprocv3.HeadersResponse{},
			},
		}, reqCtx, nil
	}

	// Populate request context with route match info
	reqCtx.routeFound = true
	reqCtx.matchedBackend = route.Backend
	reqCtx.matchedPattern = route.Path
	reqCtx.matchedType = route.Type
	reqCtx.matchedPriority = route.Priority

	p.logger.Debug("route matched, changing destination",
		zap.String("originalHost", reqCtx.authority),
		zap.String("path", reqCtx.path),
		zap.String("newBackend", route.Backend),
		zap.String("matchedPattern", route.Path),
		zap.String("matchType", route.Type),
		zap.Int32("priority", route.Priority),
	)

	// Build the Istio cluster name from the backend
	// Backend format: "service.namespace.svc.cluster.local:port"
	// Istio cluster format: "outbound|port||service.namespace.svc.cluster.local"
	host, port := route.ParseBackend()
	clusterName := fmt.Sprintf("outbound|%s||%s", port, host)

	// Build the response with header mutations
	resp := &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_RequestHeaders{
			RequestHeaders: &extprocv3.HeadersResponse{
				Response: &extprocv3.CommonResponse{
					// Clear route cache to force Envoy to re-route with new headers
					ClearRouteCache: true,
					HeaderMutation: &extprocv3.HeaderMutation{
						SetHeaders: []*corev3.HeaderValueOption{
							{
								// Set the cluster name for the EnvoyFilter route to use
								// This is the key header that triggers our injected route
								Header: &corev3.HeaderValue{
									Key:      "x-customrouter-cluster",
									RawValue: []byte(clusterName),
								},
							},
							{
								// Change :authority to route to the new backend
								Header: &corev3.HeaderValue{
									Key:   ":authority",
									Value: route.Backend,
								},
								AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
							},
							{
								// Also set Host header for compatibility
								Header: &corev3.HeaderValue{
									Key:   "Host",
									Value: route.Backend,
								},
								AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
							},
							{
								// Keep original host for debugging/logging
								Header: &corev3.HeaderValue{
									Key:   "x-original-authority",
									Value: reqCtx.authority,
								},
							},
							{
								// Add metadata about the matched route
								Header: &corev3.HeaderValue{
									Key:   "x-customrouter-matched-path",
									Value: route.Path,
								},
							},
							{
								Header: &corev3.HeaderValue{
									Key:   "x-customrouter-matched-type",
									Value: route.Type,
								},
							},
						},
					},
				},
			},
		},
	}

	// Log the response being sent back to Envoy
	p.logger.Debug("sending response to Envoy",
		zap.Bool("clear_route_cache", true),
		zap.String("x-customrouter-cluster", clusterName),
		zap.String("new_authority", route.Backend),
		zap.String("new_host", route.Backend),
		zap.String("x-original-authority", reqCtx.authority),
		zap.String("x-customrouter-matched-path", route.Path),
		zap.String("x-customrouter-matched-type", route.Type),
	)

	return resp, reqCtx, nil
}
