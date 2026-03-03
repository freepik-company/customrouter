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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// Loader loads and watches route configurations from files
type Loader struct {
	routesDir string
	config    *RoutesConfig
	mu        sync.RWMutex
	watcher   *fsnotify.Watcher
	onChange  func(*RoutesConfig)
}

// NewLoader creates a new routes loader
func NewLoader(routesDir string) *Loader {
	return &Loader{
		routesDir: routesDir,
		config: &RoutesConfig{
			Version: 1,
			Hosts:   make(map[string][]Route),
		},
	}
}

// Load loads all route configuration files from the directory
func (l *Loader) Load() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	mergedConfig := &RoutesConfig{
		Version: 1,
		Hosts:   make(map[string][]Route),
	}

	// Find all JSON files in the directory
	files, err := filepath.Glob(filepath.Join(l.routesDir, "*.json"))
	if err != nil {
		return fmt.Errorf("failed to glob routes directory: %w", err)
	}

	// Also check for routes.json directly (ConfigMap key)
	routesFile := filepath.Join(l.routesDir, "routes.json")
	if _, err := os.Stat(routesFile); err == nil {
		files = append(files, routesFile)
	}

	// Deduplicate files
	fileSet := make(map[string]bool)
	for _, f := range files {
		fileSet[f] = true
	}

	for file := range fileSet {
		data, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", file, err)
		}

		var config RoutesConfig
		if err := json.Unmarshal(data, &config); err != nil {
			return fmt.Errorf("failed to parse %s: %w", file, err)
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
		return fmt.Errorf("failed to compile regexes: %w", err)
	}

	l.config = mergedConfig
	return nil
}

// GetConfig returns the current routes configuration
func (l *Loader) GetConfig() *RoutesConfig {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.config
}

// FindRoute finds the best matching route for a given host and path
func (l *Loader) FindRoute(host, path string) *Route {
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
		if routes[i].Match(path) {
			return &routes[i]
		}
	}

	return nil
}

// Watch starts watching the routes directory for changes
func (l *Loader) Watch(onChange func(*RoutesConfig)) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}

	if err := watcher.Add(l.routesDir); err != nil {
		_ = watcher.Close()
		return fmt.Errorf("failed to watch directory: %w", err)
	}

	l.watcher = watcher
	l.onChange = onChange

	go l.watchLoop()

	return nil
}

func (l *Loader) watchLoop() {
	for {
		select {
		case event, ok := <-l.watcher.Events:
			if !ok {
				return
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove) != 0 {
				if strings.HasSuffix(event.Name, ".json") {
					if err := l.Load(); err == nil && l.onChange != nil {
						l.onChange(l.config)
					}
				}
			}
		case _, ok := <-l.watcher.Errors:
			if !ok {
				return
			}
		}
	}
}

// Close stops watching and releases resources
func (l *Loader) Close() error {
	if l.watcher != nil {
		return l.watcher.Close()
	}
	return nil
}
