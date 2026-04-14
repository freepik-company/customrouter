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
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const metricsNamespace = "customrouter"

var (
	requestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Name:      "requests_total",
			Help:      "Total number of requests processed by the external processor.",
		},
		[]string{"route_found"},
	)

	requestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: metricsNamespace,
			Name:      "request_duration_seconds",
			Help:      "Histogram of request processing duration in seconds.",
			Buckets:   []float64{0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1},
		},
		[]string{"route_found"},
	)

	routeMatchesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Name:      "route_matches_total",
			Help:      "Total number of route matches by match type.",
		},
		[]string{"match_type"},
	)

	routeNotFoundTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Name:      "route_not_found_total",
			Help:      "Total number of requests where no matching route was found.",
		},
	)

	processingErrorsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Name:      "processing_errors_total",
			Help:      "Total number of errors during request processing.",
		},
	)
)

func init() {
	prometheus.MustRegister(
		requestsTotal,
		requestDuration,
		routeMatchesTotal,
		routeNotFoundTotal,
		processingErrorsTotal,
	)
}

// MetricsHandler returns an HTTP handler for Prometheus metrics.
func MetricsHandler() http.Handler {
	return promhttp.Handler()
}
