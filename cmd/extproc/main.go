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

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/freepik-company/customrouter/internal/extproc"
)

func main() {
	config := extproc.DefaultServerConfig()
	var debug bool
	var kubeconfig string

	// Basic flags
	flag.StringVar(&config.Addr, "addr", config.Addr, "The address to listen on for gRPC connections")
	flag.StringVar(&config.TargetName, "target-name", config.TargetName,
		"The target name to filter ConfigMaps (must match spec.targetRef.name in CustomHTTPRoute)")
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to kubeconfig file (optional, uses in-cluster config if not set)")
	flag.BoolVar(&debug, "debug", false, "Enable debug logging")
	flag.BoolVar(&config.AccessLogEnabled, "access-log", config.AccessLogEnabled, "Enable access logging")
	flag.StringVar(&config.RoutesNamespace, "routes-configmap-namespace", config.RoutesNamespace,
		"Namespace to read route ConfigMaps from (empty = all namespaces)")

	// gRPC server configuration flags
	flag.IntVar(&config.MaxRecvMsgSize, "grpc-max-recv-msg-size", config.MaxRecvMsgSize, "Maximum message size the server can receive (bytes)")
	flag.IntVar(&config.MaxSendMsgSize, "grpc-max-send-msg-size", config.MaxSendMsgSize, "Maximum message size the server can send (bytes)")
	flag.Func("grpc-max-concurrent-streams", "Maximum number of concurrent streams per connection (default 1000)", func(s string) error {
		var v uint64
		_, err := fmt.Sscanf(s, "%d", &v)
		if err != nil {
			return err
		}
		config.MaxConcurrentStreams = uint32(v)
		return nil
	})
	flag.DurationVar(&config.KeepaliveTime, "grpc-keepalive-time", config.KeepaliveTime, "Time after which server pings client if no activity")
	flag.DurationVar(&config.KeepaliveTimeout, "grpc-keepalive-timeout", config.KeepaliveTimeout, "Time server waits for activity after keepalive ping")
	flag.DurationVar(&config.MaxConnectionIdle, "grpc-max-connection-idle", config.MaxConnectionIdle, "Maximum time a connection may be idle before being closed")
	flag.DurationVar(&config.MaxConnectionAge, "grpc-max-connection-age", config.MaxConnectionAge, "Maximum time a connection may exist before being closed")
	flag.DurationVar(&config.MaxConnectionAgeGrace, "grpc-max-connection-age-grace", config.MaxConnectionAgeGrace, "Grace period after max-connection-age before forcibly closing")

	flag.Parse()

	// Setup logger
	logConfig := zap.NewProductionConfig()
	logConfig.EncoderConfig.TimeKey = "timestamp"
	logConfig.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	if debug {
		logConfig.Level = zap.NewAtomicLevelAt(zapcore.DebugLevel)
	}
	logger, err := logConfig.Build()
	if err != nil {
		panic(err)
	}
	defer logger.Sync()

	// Create Kubernetes client
	var k8sConfig *rest.Config
	if kubeconfig != "" {
		k8sConfig, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			logger.Fatal("failed to build kubeconfig", zap.Error(err))
		}
	} else {
		k8sConfig, err = rest.InClusterConfig()
		if err != nil {
			logger.Fatal("failed to get in-cluster config", zap.Error(err))
		}
	}

	k8sClient, err := kubernetes.NewForConfig(k8sConfig)
	if err != nil {
		logger.Fatal("failed to create Kubernetes client", zap.Error(err))
	}
	config.K8sClient = k8sClient

	// Create context that cancels on SIGTERM/SIGINT
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		logger.Info("received shutdown signal")
		cancel()
	}()

	// Create and start server
	server, err := extproc.NewServer(config, logger)
	if err != nil {
		logger.Fatal("failed to create server", zap.Error(err))
	}

	if err := server.Start(ctx); err != nil {
		logger.Fatal("server error", zap.Error(err))
	}
}
