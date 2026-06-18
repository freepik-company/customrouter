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
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
)

func routesConfigMap() *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "customrouter-routes-default-0",
			Namespace: "default",
			Labels: map[string]string{
				configMapManagedByLabel: configMapManagedByValue,
				configMapTargetLabel:    "default",
			},
		},
		Data: map[string]string{
			routesDataKey: `{"version":1,"hosts":{"a.com":[{"path":"/","type":"prefix","backend":"svc:80"}]}}`,
		},
	}
}

// countingClient returns a fake clientset that counts ConfigMap list calls,
// which is the per-rebuild work signal (each Load lists once).
func countingClient() (*fake.Clientset, *int32) {
	cs := fake.NewSimpleClientset(routesConfigMap())
	var lists int32
	cs.PrependReactor("list", "configmaps", func(clienttesting.Action) (bool, k8sruntime.Object, error) {
		atomic.AddInt32(&lists, 1)
		return false, nil, nil // fall through to the tracker's real list
	})
	return cs, &lists
}

// TestReloadLoopCoalescesBursts asserts that a burst of change signals collapses
// into at most a couple of rebuilds when a debounce window is set, instead of
// one rebuild per signal. This is the protection against the ConfigMap churn
// pinning CPU when many ConfigMaps change rapidly.
func TestReloadLoopCoalescesBursts(t *testing.T) {
	cs, lists := countingClient()
	l := NewK8sLoader(cs, K8sLoaderConfig{TargetName: "default", ReloadDebounce: 80 * time.Millisecond})
	defer func() { _ = l.Close() }()

	go l.reloadLoop()

	// 50 change events spread over ~50ms — well inside a single 80ms window.
	for i := 0; i < 50; i++ {
		l.signalReload()
		time.Sleep(time.Millisecond)
	}
	time.Sleep(250 * time.Millisecond)

	n := atomic.LoadInt32(lists)
	if n == 0 {
		t.Fatal("expected at least one rebuild after signalling")
	}
	if n > 3 {
		t.Fatalf("expected burst of 50 signals to coalesce into <=3 rebuilds, got %d", n)
	}
}

// TestReloadLoopRebuildsWithoutDebounce asserts the loop still rebuilds when no
// debounce is configured (backward-compatible behaviour), and that the buffered
// signal channel keeps it from blocking the watch goroutine.
func TestReloadLoopRebuildsWithoutDebounce(t *testing.T) {
	cs, lists := countingClient()
	l := NewK8sLoader(cs, K8sLoaderConfig{TargetName: "default", ReloadDebounce: 0})
	defer func() { _ = l.Close() }()

	go l.reloadLoop()

	l.signalReload()
	deadline := time.After(2 * time.Second)
	for atomic.LoadInt32(lists) == 0 {
		select {
		case <-deadline:
			t.Fatal("reload loop did not rebuild within 2s")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// TestSignalReloadNeverBlocks verifies the non-blocking signal: many rapid
// signals with no consumer running must not deadlock (the buffered slot
// absorbs the first, the rest are dropped).
func TestSignalReloadNeverBlocks(t *testing.T) {
	cs, _ := countingClient()
	l := NewK8sLoader(cs, K8sLoaderConfig{TargetName: "default"})
	defer func() { _ = l.Close() }()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			l.signalReload()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("signalReload blocked")
	}
}
