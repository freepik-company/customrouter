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

package extproc

import (
	"fmt"
	"net/url"
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
	clientIP     string
	requestID    string
	host         string
	path         string
	method       string
	scheme       string
	pathSegments []string
}

// processRequestHeaders handles incoming request headers and determines routing
func (p *Processor) processRequestHeaders(headers *extprocv3.HttpHeaders, streamCtx *streamContext) (*extprocv3.ProcessingResponse, *requestContext, error) {
	reqCtx := &requestContext{
		startTime: time.Now(),
	}
	vars := &requestVars{}
	// Headers lowercased for case-insensitive matching by RouteHeaderMatch.
	requestHeaders := map[string]string{}
	// Query params are case-sensitive (RFC 3986).
	requestQueryParams := map[string]string{}

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

			// Store non-pseudo-headers for match evaluation (case-insensitive).
			if len(h.Key) > 0 && h.Key[0] != ':' {
				requestHeaders[strings.ToLower(h.Key)] = value
			}

			switch h.Key {
			case ":authority":
				reqCtx.authority = value
				vars.host = value
			case ":path":
				reqCtx.path = stripQueryString(value)
				vars.path = value
				vars.pathSegments = splitPath(value)
				requestQueryParams = extractQueryParams(value)
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
	route := p.routeFinder.FindRoute(reqCtx.authority, routes.RequestMatch{
		Path:        reqCtx.path,
		Method:      reqCtx.method,
		Headers:     requestHeaders,
		QueryParams: requestQueryParams,
	})
	if route == nil {
		p.logger.Debug("no matching route found",
			zap.String("host", reqCtx.authority),
			zap.String("path", reqCtx.path),
		)
		reqCtx.routeFound = false
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_RequestHeaders{
				RequestHeaders: &extprocv3.HeadersResponse{
					Response: &extprocv3.CommonResponse{
						HeaderMutation: &extprocv3.HeaderMutation{
							RemoveHeaders: []string{"x-customrouter-cluster"},
						},
					},
				},
			},
		}, reqCtx, nil
	}

	// Populate request context with route match info
	reqCtx.routeFound = true
	reqCtx.matchedBackend = route.Backend
	reqCtx.matchedPattern = route.Path
	reqCtx.matchedType = route.Type
	reqCtx.matchedPriority = route.Priority

	// Stash the matched route so processResponseHeaders can apply response-side
	// header mutations when Envoy reports back.
	streamCtx.matchedRoute = route

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
			return p.buildRedirectResponse(action, route, vars, reqCtx)
		}
	}

	// Build forwarding response with header mutations
	return p.buildForwardResponse(route, vars, reqCtx)
}

// buildRedirectResponse creates an immediate redirect response
func (p *Processor) buildRedirectResponse(action routes.RouteAction, route *routes.Route, vars *requestVars, reqCtx *requestContext) (*extprocv3.ProcessingResponse, *requestContext, error) {
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
	} else if shouldReplacePrefixMatchForRedirect(action, route) {
		// Strip the matched PathPrefix from the request path and append the
		// remaining suffix to the redirect path (Gateway API ReplacePrefixMatch).
		suffix := strings.TrimPrefix(vars.path, route.Path)
		// Handle trailing-slash route matching path without slash:
		// e.g. route.Path="/old-api/", vars.path="/old-api"
		if suffix == vars.path && strings.HasSuffix(route.Path, "/") {
			suffix = strings.TrimPrefix(vars.path, strings.TrimSuffix(route.Path, "/"))
		}
		path = joinRedirectPath(path, suffix)
	}

	// Build port string
	portStr := ""
	if action.RedirectPort > 0 {
		// Only include port if non-standard
		if (scheme != "http" || action.RedirectPort != 80) &&
			(scheme != "https" || action.RedirectPort != 443) {
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
								Key:      "location",
								RawValue: []byte(redirectURL),
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
				Key:      "x-original-authority",
				RawValue: []byte(reqCtx.authority),
			},
		},
		{
			Header: &corev3.HeaderValue{
				Key:      "x-customrouter-matched-path",
				RawValue: []byte(route.Path),
			},
		},
		{
			Header: &corev3.HeaderValue{
				Key:      "x-customrouter-matched-type",
				RawValue: []byte(route.Type),
			},
		},
	}

	var removeHeaders []string

	// Apply actions from the route
	for _, action := range route.Actions {
		switch action.Type {
		case routes.ActionTypeRewrite:
			if action.RewritePath != "" {
				rewrittenBase := substituteVariables(action.RewritePath, vars)
				if shouldReplacePrefixMatch(action, route, rewrittenBase) {
					suffix := strings.TrimPrefix(vars.path, route.Path)
					// Handle trailing-slash route matching path without slash:
					// e.g. route.Path="/audio/download/", vars.path="/audio/download"
					if suffix == vars.path && strings.HasSuffix(route.Path, "/") {
						suffix = strings.TrimPrefix(vars.path, strings.TrimSuffix(route.Path, "/"))
					}
					finalPath = rewrittenBase + suffix
				} else {
					finalPath = rewrittenBase
				}
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
						Key:      action.HeaderName,
						RawValue: []byte(value),
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
						Key:      action.HeaderName,
						RawValue: []byte(value),
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

	// Only rewrite authority/host if explicitly requested via RewriteHostname action
	// Otherwise, keep the original authority so Istio can match the virtual host correctly
	if finalAuthority != route.Backend {
		setHeaders = append(setHeaders,
			&corev3.HeaderValueOption{
				Header: &corev3.HeaderValue{
					Key:      ":authority",
					RawValue: []byte(finalAuthority),
				},
				AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
			},
			&corev3.HeaderValueOption{
				Header: &corev3.HeaderValue{
					Key:      "host",
					RawValue: []byte(finalAuthority),
				},
				AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
			},
		)
	}

	// Add path rewrite if path was changed
	if finalPath != vars.path {
		// Preserve the original path so Istio/Envoy access logs can show it.
		// The default log format reads %REQ(X-ENVOY-ORIGINAL-PATH?:PATH)%.
		setHeaders = append(setHeaders, &corev3.HeaderValueOption{
			Header: &corev3.HeaderValue{
				Key:      "x-envoy-original-path",
				RawValue: []byte(vars.path),
			},
			AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
		})
		setHeaders = append(setHeaders, &corev3.HeaderValueOption{
			Header: &corev3.HeaderValue{
				Key:      ":path",
				RawValue: []byte(finalPath),
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

// processResponseHeaders applies the matched route's response-side header
// mutations, if any. When no route matched in the request phase (streamCtx is
// empty or the route has no response-header actions), returns a no-op response
// so Envoy can continue forwarding the upstream response unchanged.
func (p *Processor) processResponseHeaders(streamCtx *streamContext) *extprocv3.ProcessingResponse {
	if streamCtx == nil || streamCtx.matchedRoute == nil {
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_ResponseHeaders{
				ResponseHeaders: &extprocv3.HeadersResponse{},
			},
		}
	}

	var setHeaders []*corev3.HeaderValueOption
	var removeHeaders []string
	for _, action := range streamCtx.matchedRoute.Actions {
		switch action.Type {
		case routes.ActionTypeResponseHeaderSet:
			if action.HeaderName == "" {
				continue
			}
			setHeaders = append(setHeaders, &corev3.HeaderValueOption{
				Header: &corev3.HeaderValue{
					Key:      action.HeaderName,
					RawValue: []byte(action.Value),
				},
				AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
			})
		case routes.ActionTypeResponseHeaderAdd:
			if action.HeaderName == "" {
				continue
			}
			setHeaders = append(setHeaders, &corev3.HeaderValueOption{
				Header: &corev3.HeaderValue{
					Key:      action.HeaderName,
					RawValue: []byte(action.Value),
				},
				AppendAction: corev3.HeaderValueOption_APPEND_IF_EXISTS_OR_ADD,
			})
		case routes.ActionTypeResponseHeaderRemove:
			if action.HeaderName == "" {
				continue
			}
			removeHeaders = append(removeHeaders, action.HeaderName)
		}
	}

	if len(setHeaders) == 0 && len(removeHeaders) == 0 {
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_ResponseHeaders{
				ResponseHeaders: &extprocv3.HeadersResponse{},
			},
		}
	}

	p.logger.Debug("applying response header mutations",
		zap.Int("set", len(setHeaders)),
		zap.Int("remove", len(removeHeaders)),
	)

	return &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_ResponseHeaders{
			ResponseHeaders: &extprocv3.HeadersResponse{
				Response: &extprocv3.CommonResponse{
					HeaderMutation: &extprocv3.HeaderMutation{
						SetHeaders:    setHeaders,
						RemoveHeaders: removeHeaders,
					},
				},
			},
		},
	}
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

// stripQueryString extracts the path component from a request target by
// removing the query string and fragment. Per RFC 3986 §3.3, the path is
// terminated by the first "?" or "#" character, or by the end of the URI.
// Route matching should operate exclusively on the path component.
func stripQueryString(path string) string {
	if idx := strings.IndexAny(path, "?#"); idx != -1 {
		return path[:idx]
	}
	return path
}

// extractQueryParams returns a flat map of the first value observed for each
// query parameter name in the given ":path". Names are case-sensitive per
// RFC 3986. Returns an empty map when no query string is present.
// Invalid query strings are parsed on a best-effort basis.
func extractQueryParams(rawPath string) map[string]string {
	out := map[string]string{}
	idx := strings.Index(rawPath, "?")
	if idx == -1 || idx == len(rawPath)-1 {
		return out
	}
	query := rawPath[idx+1:]
	if hash := strings.Index(query, "#"); hash != -1 {
		query = query[:hash]
	}
	values, err := url.ParseQuery(query)
	if err != nil {
		return out
	}
	for k, v := range values {
		if len(v) > 0 {
			out[k] = v[0]
		}
	}
	return out
}

// splitPath splits a path into segments
func splitPath(path string) []string {
	// Remove query string and fragment (RFC 3986 §3.3)
	if idx := strings.IndexAny(path, "?#"); idx != -1 {
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

// joinRedirectPath appends the stripped suffix to the redirect basePath
// without producing a duplicate "/" between path segments, and without
// inserting a "/" in front of a query ("?") or fragment ("#") delimiter
// (RFC 3986 §3.3). The suffix comes from stripping the matched PathPrefix
// from vars.path, which includes query/fragment as-is.
func joinRedirectPath(basePath, suffix string) string {
	if suffix == "" {
		return basePath
	}
	switch suffix[0] {
	case '?', '#':
		return basePath + suffix
	case '/':
		if strings.HasSuffix(basePath, "/") {
			return basePath + suffix[1:]
		}
		return basePath + suffix
	default:
		if strings.HasSuffix(basePath, "/") {
			return basePath + suffix
		}
		return basePath + "/" + suffix
	}
}

// shouldReplacePrefixMatchForRedirect determines whether a redirect should strip the
// matched PathPrefix and append the remaining suffix to the redirect path.
// Strictly opt-in (preserves backwards-compatible behaviour): only active when
// the user explicitly sets replacePrefixMatch=true and the route is a PathPrefix.
func shouldReplacePrefixMatchForRedirect(action routes.RouteAction, route *routes.Route) bool {
	if action.RedirectReplacePrefixMatch == nil || !*action.RedirectReplacePrefixMatch {
		return false
	}
	return route.Type == routes.RouteTypePrefix
}

// shouldReplacePrefixMatch determines whether a rewrite should use prefix replacement.
// Explicit field takes precedence. Otherwise, convention: prefix rewrite for PathPrefix
// routes whose rewritePath contains no variables (${...}); full rewrite otherwise.
func shouldReplacePrefixMatch(action routes.RouteAction, route *routes.Route, _ string) bool {
	if action.RewriteReplacePrefixMatch != nil {
		return *action.RewriteReplacePrefixMatch
	}
	return route.Type == routes.RouteTypePrefix && !strings.Contains(action.RewritePath, "${")
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
