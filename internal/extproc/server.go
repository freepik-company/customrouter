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
	"context"
	"fmt"
	"net"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/freepik-company/customrouter/pkg/routes"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"
)

// Server wraps the gRPC server for the external processor
type Server struct {
	grpcServer *grpc.Server
	processor  *Processor
	loader     *routes.K8sLoader
	logger     *zap.Logger
	config     *ServerConfig
}

// NewServer creates a new extproc server with the given configuration
func NewServer(config *ServerConfig, logger *zap.Logger) (*Server, error) {
	if config == nil {
		return nil, fmt.Errorf("config is required")
	}

	if config.K8sClient == nil {
		return nil, fmt.Errorf("K8sClient is required")
	}

	if config.TargetName == "" {
		return nil, fmt.Errorf("TargetName is required")
	}

	loader := routes.NewK8sLoader(config.K8sClient, routes.K8sLoaderConfig{
		TargetName: config.TargetName,
	})

	// Initial load
	if err := loader.Load(); err != nil {
		return nil, fmt.Errorf("failed to load routes from ConfigMaps: %w", err)
	}

	processor := NewProcessor(loader, logger, config.AccessLogEnabled)

	// Configure gRPC server options for production
	grpcOpts := []grpc.ServerOption{
		grpc.MaxRecvMsgSize(config.MaxRecvMsgSize),
		grpc.MaxSendMsgSize(config.MaxSendMsgSize),
		grpc.MaxConcurrentStreams(config.MaxConcurrentStreams),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:              config.KeepaliveTime,
			Timeout:           config.KeepaliveTimeout,
			MaxConnectionIdle: config.MaxConnectionIdle,
			MaxConnectionAge:  config.MaxConnectionAge,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             5,    // Minimum time between pings from client
			PermitWithoutStream: true, // Allow pings even when no active streams
		}),
	}

	grpcServer := grpc.NewServer(grpcOpts...)
	extprocv3.RegisterExternalProcessorServer(grpcServer, processor)

	// Register health service
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(grpcServer, healthServer)

	// Register reflection for debugging
	reflection.Register(grpcServer)

	return &Server{
		grpcServer: grpcServer,
		processor:  processor,
		loader:     loader,
		logger:     logger,
		config:     config,
	}, nil
}

// Start starts the gRPC server and watches for config changes
func (s *Server) Start(ctx context.Context) error {
	// Start watching for ConfigMap changes
	if err := s.loader.Watch(func(config *routes.RoutesConfig) {
		s.logger.Info("routes configuration reloaded from ConfigMaps",
			zap.Int("hosts", len(config.Hosts)),
		)
	}); err != nil {
		s.logger.Warn("failed to start ConfigMap watcher", zap.Error(err))
	}

	listener, err := net.Listen("tcp", s.config.Addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.config.Addr, err)
	}

	s.logger.Info("starting extproc server",
		zap.String("addr", s.config.Addr),
		zap.String("target_name", s.config.TargetName),
		zap.Int("max_recv_msg_size", s.config.MaxRecvMsgSize),
		zap.Int("max_send_msg_size", s.config.MaxSendMsgSize),
		zap.Uint32("max_concurrent_streams", s.config.MaxConcurrentStreams),
		zap.Duration("keepalive_time", s.config.KeepaliveTime),
		zap.Duration("keepalive_timeout", s.config.KeepaliveTimeout),
		zap.Duration("max_connection_idle", s.config.MaxConnectionIdle),
		zap.Duration("max_connection_age", s.config.MaxConnectionAge),
		zap.Bool("access_log_enabled", s.config.AccessLogEnabled),
	)

	// Handle graceful shutdown
	go func() {
		<-ctx.Done()
		s.logger.Info("shutting down extproc server")
		s.grpcServer.GracefulStop()
		s.loader.Close()
	}()

	return s.grpcServer.Serve(listener)
}

// Stop stops the gRPC server
func (s *Server) Stop() {
	s.grpcServer.GracefulStop()
	s.loader.Close()
}
