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

package routes

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
)

const (
	// configMapManagedByLabel is the label to identify ConfigMaps managed by customrouter
	configMapManagedByLabel = "app.kubernetes.io/managed-by"
	configMapManagedByValue = "customrouter-controller"

	// configMapTargetLabel is the label used to identify the target external processor
	configMapTargetLabel = "customrouter.freepik.com/target"

	// routesDataKey is the key in ConfigMap data that contains the routes JSON
	routesDataKey = "routes.json"
)

// K8sLoader loads and watches route configurations from Kubernetes ConfigMaps
type K8sLoader struct {
	client     kubernetes.Interface
	targetName string
	namespace  string

	config   *RoutesConfig
	mu       sync.RWMutex
	onChange func(*RoutesConfig)

	ctx    context.Context
	cancel context.CancelFunc
}

// K8sLoaderConfig holds configuration for the K8sLoader
type K8sLoaderConfig struct {
	// TargetName is the target external processor name to filter ConfigMaps
	// Only ConfigMaps with label customrouter.freepik.com/target=<TargetName> will be loaded
	TargetName string

	// Namespace restricts ConfigMap loading to a specific namespace.
	// Empty string means all namespaces (backward compatible).
	Namespace string
}

// NewK8sLoader creates a new Kubernetes ConfigMap loader
func NewK8sLoader(client kubernetes.Interface, config K8sLoaderConfig) *K8sLoader {
	ctx, cancel := context.WithCancel(context.Background())
	return &K8sLoader{
		client:     client,
		targetName: config.TargetName,
		namespace:  config.Namespace,
		config: &RoutesConfig{
			Version: 1,
			Hosts:   make(map[string][]Route),
		},
		ctx:    ctx,
		cancel: cancel,
	}
}

// Load loads all route ConfigMaps and merges them.
// It builds the new config without holding the lock, then swaps it in
// atomically so that FindRoute is never blocked on API calls.
func (l *K8sLoader) Load() error {
	config, err := l.buildConfig()
	if err != nil {
		return err
	}

	l.mu.Lock()
	l.config = config
	l.mu.Unlock()

	return nil
}

// buildConfig fetches and merges all ConfigMaps into a new RoutesConfig.
// This is done without holding any lock.
func (l *K8sLoader) buildConfig() (*RoutesConfig, error) {
	// List all ConfigMaps with our labels (managed-by and target)
	labelSelector := labels.SelectorFromSet(map[string]string{
		configMapManagedByLabel: configMapManagedByValue,
		configMapTargetLabel:    l.targetName,
	})

	configMaps, err := l.client.CoreV1().ConfigMaps(l.namespace).List(l.ctx, metav1.ListOptions{
		LabelSelector: labelSelector.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list ConfigMaps: %w", err)
	}

	// Sort by name for deterministic ordering. sort.Stable preserves the
	// input order for equal keys; with unique ConfigMap names this matches
	// sort.Slice, but the explicit guarantee is preferable here because the
	// merged routes feed downstream byte-identical comparisons.
	sort.SliceStable(configMaps.Items, func(i, j int) bool {
		return configMaps.Items[i].Name < configMaps.Items[j].Name
	})

	// Merge all ConfigMaps
	mergedConfig := &RoutesConfig{
		Version: 1,
		Hosts:   make(map[string][]Route),
	}

	for _, cm := range configMaps.Items {
		data, ok := cm.Data[routesDataKey]
		if !ok {
			continue
		}

		var config RoutesConfig
		if err := json.Unmarshal([]byte(data), &config); err != nil {
			return nil, fmt.Errorf("failed to parse ConfigMap %s: %w", cm.Name, err)
		}

		// Merge hosts
		for host, routes := range config.Hosts {
			if existing, ok := mergedConfig.Hosts[host]; ok {
				mergedConfig.Hosts[host] = append(existing, routes...)
			} else {
				mergedConfig.Hosts[host] = routes
			}
		}
	}

	// Sort routes for each host by priority
	for host := range mergedConfig.Hosts {
		SortRoutes(mergedConfig.Hosts[host])
	}

	// Compile regexes
	if err := mergedConfig.CompileRegexes(); err != nil {
		return nil, fmt.Errorf("failed to compile regexes: %w", err)
	}

	return mergedConfig, nil
}

// GetConfig returns the current routes configuration
func (l *K8sLoader) GetConfig() *RoutesConfig {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.config
}

// FindRoute finds the best matching route for a given host and request.
func (l *K8sLoader) FindRoute(host string, req RequestMatch) *Route {
	l.mu.RLock()
	defer l.mu.RUnlock()

	// Strip port from host if present
	if idx := strings.Index(host, ":"); idx != -1 {
		host = host[:idx]
	}

	routes, ok := l.config.Hosts[host]
	if !ok {
		return nil
	}

	// Routes are already sorted by priority, so first match wins
	for i := range routes {
		if routes[i].Match(req) {
			return &routes[i]
		}
	}

	return nil
}

// Watch starts watching ConfigMaps for changes
func (l *K8sLoader) Watch(onChange func(*RoutesConfig)) error {
	l.onChange = onChange

	go l.watchLoop()

	return nil
}

func (l *K8sLoader) watchLoop() {
	labelSelector := labels.SelectorFromSet(map[string]string{
		configMapManagedByLabel: configMapManagedByValue,
		configMapTargetLabel:    l.targetName,
	})

	for {
		select {
		case <-l.ctx.Done():
			return
		default:
		}

		watcher, err := l.client.CoreV1().ConfigMaps(l.namespace).Watch(l.ctx, metav1.ListOptions{
			LabelSelector: labelSelector.String(),
		})
		if err != nil {
			// Retry after a delay
			select {
			case <-l.ctx.Done():
				return
			case <-time.After(5 * time.Second):
				continue
			}
		}

		l.handleWatchEvents(watcher)
	}
}

func (l *K8sLoader) handleWatchEvents(watcher watch.Interface) {
	defer watcher.Stop()

	for {
		select {
		case <-l.ctx.Done():
			return
		case event, ok := <-watcher.ResultChan():
			if !ok {
				// Watch channel closed, need to restart
				return
			}

			switch event.Type {
			case watch.Added, watch.Modified, watch.Deleted:
				// Reload all ConfigMaps for this target
				if err := l.Load(); err == nil && l.onChange != nil {
					l.onChange(l.config)
				}

			case watch.Error:
				// Watch error, need to restart
				return
			}
		}
	}
}

// Close stops watching and releases resources
func (l *K8sLoader) Close() error {
	l.cancel()
	return nil
}
