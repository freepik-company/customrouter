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
	"strconv"
	"strings"
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	"github.com/freepik-company/customrouter/pkg/routes"
	"go.uber.org/zap"
)

// requestVars holds extracted values for variable substitution
type requestVars struct {
	clientIP   string
	requestID  string
	host       string
	path       string
	method     string
	scheme     string
	pathSegments []string
}

// processRequestHeaders handles incoming request headers and determines routing
func (p *Processor) processRequestHeaders(headers *extprocv3.HttpHeaders) (*extprocv3.ProcessingResponse, *requestContext, error) {
	reqCtx := &requestContext{
		startTime: time.Now(),
	}
	vars := &requestVars{}

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

	// Extract headers for routing and variable substitution
	if headers != nil && headers.Headers != nil {
		for _, h := range headers.Headers.Headers {
			value := h.Value
			if value == "" && len(h.RawValue) > 0 {
				value = string(h.RawValue)
			}

			switch h.Key {
			case ":authority":
				reqCtx.authority = value
				vars.host = value
			case ":path":
				reqCtx.path = value
				vars.path = value
				vars.pathSegments = splitPath(value)
			case ":method":
				reqCtx.method = value
				vars.method = value
			case ":scheme":
				vars.scheme = value
			case "x-forwarded-for":
				vars.clientIP = extractFirstIP(value)
			case "x-request-id":
				vars.requestID = value
			case "x-forwarded-proto":
				if vars.scheme == "" {
					vars.scheme = value
				}
			}
		}
	}

	// Default scheme to https if not set
	if vars.scheme == "" {
		vars.scheme = "https"
	}

	p.logger.Debug("extracted values",
		zap.String("authority", reqCtx.authority),
		zap.String("path", reqCtx.path),
		zap.String("method", reqCtx.method),
		zap.String("scheme", vars.scheme),
		zap.String("client_ip", vars.clientIP),
		zap.String("request_id", vars.requestID),
	)

	// Find matching route
	route := p.routeFinder.FindRoute(reqCtx.authority, reqCtx.path)
	if route == nil {
		p.logger.Debug("no matching route found",
			zap.String("host", reqCtx.authority),
			zap.String("path", reqCtx.path),
		)
		reqCtx.routeFound = false
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

	p.logger.Debug("route matched",
		zap.String("originalHost", reqCtx.authority),
		zap.String("path", reqCtx.path),
		zap.String("backend", route.Backend),
		zap.String("matchedPattern", route.Path),
		zap.String("matchType", route.Type),
		zap.Int32("priority", route.Priority),
		zap.Int("action_count", len(route.Actions)),
	)

	// Check if there's a redirect action - redirects take precedence
	for _, action := range route.Actions {
		if action.Type == routes.ActionTypeRedirect {
			return p.buildRedirectResponse(action, vars, reqCtx)
		}
	}

	// Build forwarding response with header mutations
	return p.buildForwardResponse(route, vars, reqCtx)
}

// buildRedirectResponse creates an immediate redirect response
func (p *Processor) buildRedirectResponse(action routes.RouteAction, vars *requestVars, reqCtx *requestContext) (*extprocv3.ProcessingResponse, *requestContext, error) {
	// Build redirect URL components
	scheme := action.RedirectScheme
	if scheme == "" {
		scheme = vars.scheme
	}

	hostname := action.RedirectHostname
	if hostname == "" {
		hostname = stripPort(vars.host)
	}

	path := substituteVariables(action.RedirectPath, vars)
	if path == "" {
		path = vars.path
	}

	// Build port string
	portStr := ""
	if action.RedirectPort > 0 {
		// Only include port if non-standard
		if !((scheme == "http" && action.RedirectPort == 80) ||
			(scheme == "https" && action.RedirectPort == 443)) {
			portStr = ":" + strconv.Itoa(int(action.RedirectPort))
		}
	}

	// Build full redirect URL
	redirectURL := scheme + "://" + hostname + portStr + path

	statusCode := action.RedirectStatusCode
	if statusCode == 0 {
		statusCode = 302
	}

	p.logger.Debug("sending redirect response",
		zap.String("location", redirectURL),
		zap.Int32("status_code", statusCode),
	)

	resp := &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_ImmediateResponse{
			ImmediateResponse: &extprocv3.ImmediateResponse{
				Status: &typev3.HttpStatus{
					Code: typev3.StatusCode(statusCode),
				},
				Headers: &extprocv3.HeaderMutation{
					SetHeaders: []*corev3.HeaderValueOption{
						{
							Header: &corev3.HeaderValue{
								Key:   "Location",
								Value: redirectURL,
							},
						},
					},
				},
			},
		},
	}

	return resp, reqCtx, nil
}

// buildForwardResponse creates a response that forwards to the backend with modifications
func (p *Processor) buildForwardResponse(route *routes.Route, vars *requestVars, reqCtx *requestContext) (*extprocv3.ProcessingResponse, *requestContext, error) {
	// Determine final authority (may be rewritten)
	finalAuthority := route.Backend
	finalPath := vars.path

	// Build base headers
	host, port := route.ParseBackend()
	clusterName := fmt.Sprintf("outbound|%s||%s", port, host)

	setHeaders := []*corev3.HeaderValueOption{
		{
			Header: &corev3.HeaderValue{
				Key:      "x-customrouter-cluster",
				RawValue: []byte(clusterName),
			},
		},
		{
			Header: &corev3.HeaderValue{
				Key:   "x-original-authority",
				Value: reqCtx.authority,
			},
		},
		{
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
	}

	var removeHeaders []string

	// Apply actions from the route
	for _, action := range route.Actions {
		switch action.Type {
		case routes.ActionTypeRewrite:
			if action.RewritePath != "" {
				finalPath = substituteVariables(action.RewritePath, vars)
				p.logger.Debug("rewriting path",
					zap.String("original", vars.path),
					zap.String("rewritten", finalPath),
				)
			}
			if action.RewriteHostname != "" {
				finalAuthority = action.RewriteHostname
				p.logger.Debug("rewriting hostname",
					zap.String("original", route.Backend),
					zap.String("rewritten", finalAuthority),
				)
			}

		case routes.ActionTypeHeaderSet:
			if action.HeaderName != "" {
				value := substituteVariables(action.Value, vars)
				setHeaders = append(setHeaders, &corev3.HeaderValueOption{
					Header: &corev3.HeaderValue{
						Key:   action.HeaderName,
						Value: value,
					},
					AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
				})
				p.logger.Debug("setting header",
					zap.String("name", action.HeaderName),
					zap.String("value", value),
				)
			}

		case routes.ActionTypeHeaderAdd:
			if action.HeaderName != "" {
				value := substituteVariables(action.Value, vars)
				setHeaders = append(setHeaders, &corev3.HeaderValueOption{
					Header: &corev3.HeaderValue{
						Key:   action.HeaderName,
						Value: value,
					},
					AppendAction: corev3.HeaderValueOption_APPEND_IF_EXISTS_OR_ADD,
				})
				p.logger.Debug("adding header",
					zap.String("name", action.HeaderName),
					zap.String("value", value),
				)
			}

		case routes.ActionTypeHeaderRemove:
			if action.HeaderName != "" {
				removeHeaders = append(removeHeaders, action.HeaderName)
				p.logger.Debug("removing header",
					zap.String("name", action.HeaderName),
				)
			}
		}
	}

	// Add authority headers
	setHeaders = append(setHeaders,
		&corev3.HeaderValueOption{
			Header: &corev3.HeaderValue{
				Key:   ":authority",
				Value: finalAuthority,
			},
			AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
		},
		&corev3.HeaderValueOption{
			Header: &corev3.HeaderValue{
				Key:   "Host",
				Value: finalAuthority,
			},
			AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
		},
	)

	// Add path rewrite if path was changed
	if finalPath != vars.path {
		setHeaders = append(setHeaders, &corev3.HeaderValueOption{
			Header: &corev3.HeaderValue{
				Key:   ":path",
				Value: finalPath,
			},
			AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
		})
	}

	resp := &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_RequestHeaders{
			RequestHeaders: &extprocv3.HeadersResponse{
				Response: &extprocv3.CommonResponse{
					ClearRouteCache: true,
					HeaderMutation: &extprocv3.HeaderMutation{
						SetHeaders:    setHeaders,
						RemoveHeaders: removeHeaders,
					},
				},
			},
		},
	}

	p.logger.Debug("sending forward response",
		zap.String("cluster", clusterName),
		zap.String("authority", finalAuthority),
		zap.String("path", finalPath),
		zap.Int("headers_set", len(setHeaders)),
		zap.Int("headers_removed", len(removeHeaders)),
	)

	return resp, reqCtx, nil
}

// extractFirstIP extracts the first IP from a comma-separated list (X-Forwarded-For)
func extractFirstIP(xff string) string {
	if xff == "" {
		return ""
	}
	parts := strings.Split(xff, ",")
	return strings.TrimSpace(parts[0])
}

// stripPort removes port from host:port string
func stripPort(host string) string {
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		// Check if this is an IPv6 address
		if strings.Contains(host, "]") {
			// IPv6 with port: [::1]:8080
			if bracketIdx := strings.LastIndex(host, "]"); bracketIdx < idx {
				return host[:idx]
			}
		} else if strings.Count(host, ":") == 1 {
			// IPv4 with port: 127.0.0.1:8080
			return host[:idx]
		}
	}
	return host
}

// splitPath splits a path into segments
func splitPath(path string) []string {
	// Remove query string
	if idx := strings.Index(path, "?"); idx != -1 {
		path = path[:idx]
	}
	// Split and filter empty segments
	parts := strings.Split(path, "/")
	segments := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			segments = append(segments, p)
		}
	}
	return segments
}

// substituteVariables replaces ${var} placeholders with actual values
func substituteVariables(value string, vars *requestVars) string {
	if vars == nil || value == "" {
		return value
	}

	result := value
	result = strings.ReplaceAll(result, "${client_ip}", vars.clientIP)
	result = strings.ReplaceAll(result, "${request_id}", vars.requestID)
	result = strings.ReplaceAll(result, "${host}", vars.host)
	result = strings.ReplaceAll(result, "${path}", vars.path)
	result = strings.ReplaceAll(result, "${method}", vars.method)
	result = strings.ReplaceAll(result, "${scheme}", vars.scheme)

	// Handle path segments: ${path.segment.N}
	for i, segment := range vars.pathSegments {
		placeholder := fmt.Sprintf("${path.segment.%d}", i)
		result = strings.ReplaceAll(result, placeholder, segment)
	}

	return result
}
