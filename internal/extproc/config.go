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
	"time"

	"k8s.io/client-go/kubernetes"
)

// ServerConfig holds gRPC server configuration options
type ServerConfig struct {
	// Address to listen on
	Addr string

	// TargetName is the target external processor name to filter ConfigMaps
	// Only ConfigMaps with label customrouter.freepik.com/target=<TargetName> will be loaded
	TargetName string

	// K8sClient is the Kubernetes client for reading ConfigMaps
	K8sClient kubernetes.Interface

	// MaxRecvMsgSize is the maximum message size the server can receive (bytes)
	MaxRecvMsgSize int

	// MaxSendMsgSize is the maximum message size the server can send (bytes)
	MaxSendMsgSize int

	// MaxConcurrentStreams is the maximum number of concurrent streams per connection
	MaxConcurrentStreams uint32

	// KeepaliveTime is the time after which if the server doesn't see any activity
	// it pings the client to see if the transport is still alive
	KeepaliveTime time.Duration

	// KeepaliveTimeout is the time the server waits for activity after a keepalive ping
	KeepaliveTimeout time.Duration

	// MaxConnectionIdle is the maximum time a connection may be idle before being closed
	MaxConnectionIdle time.Duration

	// MaxConnectionAge is the maximum time a connection may exist before being closed
	MaxConnectionAge time.Duration

	// MaxConnectionAgeGrace is an additive period after MaxConnectionAge after which
	// the connection will be forcibly closed
	MaxConnectionAgeGrace time.Duration

	// AccessLogEnabled enables access logging
	AccessLogEnabled bool

	// RoutesNamespace restricts ConfigMap loading to a specific namespace.
	// Empty string means all namespaces (backward compatible).
	RoutesNamespace string
}

// DefaultServerConfig returns a ServerConfig with production-ready defaults
func DefaultServerConfig() *ServerConfig {
	return &ServerConfig{
		Addr:                  ":9001",
		TargetName:            "",
		MaxRecvMsgSize:        4 * 1024 * 1024,  // 4MB
		MaxSendMsgSize:        4 * 1024 * 1024,  // 4MB
		MaxConcurrentStreams:  1000,             // High concurrency for ext_proc
		KeepaliveTime:         30 * time.Second, // Ping every 30s if idle
		KeepaliveTimeout:      10 * time.Second, // Wait 10s for ping response
		MaxConnectionIdle:     5 * time.Minute,  // Close idle connections after 5m
		MaxConnectionAge:      30 * time.Minute, // Force reconnect after 30m for load balancing
		MaxConnectionAgeGrace: 10 * time.Second, // Grace period for in-flight requests
		AccessLogEnabled:      true,
	}
}
