/*
Copyright 2026.

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

package customhttproute

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/freepik-company/customrouter/api/v1alpha1"
)

const (
	hostACom  = "a.com"
	svcEPASvc = "epa-svc"
)

func TestCollectCatchAllEntries_Empty(t *testing.T) {
	routeList := &v1alpha1.CustomHTTPRouteList{}
	entries := collectCatchAllEntries(routeList)
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestCollectCatchAllEntries_NoCatchAll(t *testing.T) {
	routeList := &v1alpha1.CustomHTTPRouteList{
		Items: []v1alpha1.CustomHTTPRoute{
			{
				Spec: v1alpha1.CustomHTTPRouteSpec{
					Hostnames: []string{"example.com"},
					Rules: []v1alpha1.Rule{
						{Matches: []v1alpha1.PathMatch{{Path: "/"}}},
					},
				},
			},
		},
	}
	entries := collectCatchAllEntries(routeList)
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestCollectCatchAllEntries_SingleRoute(t *testing.T) {
	routeList := &v1alpha1.CustomHTTPRouteList{
		Items: []v1alpha1.CustomHTTPRoute{
			{
				Spec: v1alpha1.CustomHTTPRouteSpec{
					Hostnames: []string{"example.com", "www.example.com"},
					CatchAllRoute: &v1alpha1.CatchAllBackendRef{
						BackendRef: v1alpha1.BackendRef{
							Name:      "web",
							Namespace: "default",
							Port:      80,
						},
					},
					Rules: []v1alpha1.Rule{
						{Matches: []v1alpha1.PathMatch{{Path: "/"}}},
					},
				},
			},
		},
	}
	entries := collectCatchAllEntries(routeList)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Hostname != "example.com" {
		t.Errorf("expected first hostname to be example.com, got %s", entries[0].Hostname)
	}
	if entries[1].Hostname != "www.example.com" {
		t.Errorf("expected second hostname to be www.example.com, got %s", entries[1].Hostname)
	}
	if entries[0].BackendRef.Name != "web" {
		t.Errorf("expected backendRef name to be web, got %s", entries[0].BackendRef.Name)
	}
}

func TestCollectCatchAllEntries_MultipleRoutes(t *testing.T) {
	routeList := &v1alpha1.CustomHTTPRouteList{
		Items: []v1alpha1.CustomHTTPRoute{
			{
				Spec: v1alpha1.CustomHTTPRouteSpec{
					Hostnames: []string{hostACom},
					CatchAllRoute: &v1alpha1.CatchAllBackendRef{
						BackendRef: v1alpha1.BackendRef{Name: "svc-a", Namespace: "ns-a", Port: 80},
					},
					Rules: []v1alpha1.Rule{
						{Matches: []v1alpha1.PathMatch{{Path: "/"}}},
					},
				},
			},
			{
				Spec: v1alpha1.CustomHTTPRouteSpec{
					Hostnames: []string{"b.com"},
					CatchAllRoute: &v1alpha1.CatchAllBackendRef{
						BackendRef: v1alpha1.BackendRef{Name: "svc-b", Namespace: "ns-b", Port: 8080},
					},
					Rules: []v1alpha1.Rule{
						{Matches: []v1alpha1.PathMatch{{Path: "/"}}},
					},
				},
			},
		},
	}
	entries := collectCatchAllEntries(routeList)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Hostname != hostACom || entries[0].BackendRef.Name != "svc-a" {
		t.Errorf("unexpected first entry: %+v", entries[0])
	}
	if entries[1].Hostname != "b.com" || entries[1].BackendRef.Name != "svc-b" {
		t.Errorf("unexpected second entry: %+v", entries[1])
	}
}

func TestCollectCatchAllEntries_SkipsDeleting(t *testing.T) {
	now := metav1.NewTime(time.Now())
	routeList := &v1alpha1.CustomHTTPRouteList{
		Items: []v1alpha1.CustomHTTPRoute{
			{
				ObjectMeta: metav1.ObjectMeta{
					DeletionTimestamp: &now,
				},
				Spec: v1alpha1.CustomHTTPRouteSpec{
					Hostnames: []string{"deleting.com"},
					CatchAllRoute: &v1alpha1.CatchAllBackendRef{
						BackendRef: v1alpha1.BackendRef{Name: "svc", Namespace: "ns", Port: 80},
					},
					Rules: []v1alpha1.Rule{
						{Matches: []v1alpha1.PathMatch{{Path: "/"}}},
					},
				},
			},
		},
	}
	entries := collectCatchAllEntries(routeList)
	if len(entries) != 0 {
		t.Errorf("expected 0 entries (route being deleted), got %d", len(entries))
	}
}

func TestCollectCatchAllEntries_DuplicateHostnameLastWins(t *testing.T) {
	routeList := &v1alpha1.CustomHTTPRouteList{
		Items: []v1alpha1.CustomHTTPRoute{
			{
				Spec: v1alpha1.CustomHTTPRouteSpec{
					Hostnames: []string{"example.com"},
					CatchAllRoute: &v1alpha1.CatchAllBackendRef{
						BackendRef: v1alpha1.BackendRef{Name: "svc-1", Namespace: "ns", Port: 80},
					},
					Rules: []v1alpha1.Rule{
						{Matches: []v1alpha1.PathMatch{{Path: "/"}}},
					},
				},
			},
			{
				Spec: v1alpha1.CustomHTTPRouteSpec{
					Hostnames: []string{"example.com"},
					CatchAllRoute: &v1alpha1.CatchAllBackendRef{
						BackendRef: v1alpha1.BackendRef{Name: "svc-2", Namespace: "ns", Port: 80},
					},
					Rules: []v1alpha1.Rule{
						{Matches: []v1alpha1.PathMatch{{Path: "/"}}},
					},
				},
			},
		},
	}
	entries := collectCatchAllEntries(routeList)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry (deduplicated), got %d", len(entries))
	}
	if entries[0].BackendRef.Name != "svc-2" {
		t.Errorf("expected last-wins for duplicate hostname, got %s", entries[0].BackendRef.Name)
	}
}

func TestMergeWithEPACatchAll_OnlyRoutes(t *testing.T) {
	routeEntries := []CatchAllEntry{
		{Hostname: hostACom, BackendRef: v1alpha1.BackendRef{Name: "svc-a", Namespace: "ns", Port: 80}},
	}
	epa := &v1alpha1.ExternalProcessorAttachment{}

	merged := mergeWithEPACatchAll(routeEntries, epa)
	if len(merged) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(merged))
	}
	if merged[0].Hostname != hostACom {
		t.Errorf("expected a.com, got %s", merged[0].Hostname)
	}
}

func TestMergeWithEPACatchAll_OnlyEPA(t *testing.T) {
	epa := &v1alpha1.ExternalProcessorAttachment{
		Spec: v1alpha1.ExternalProcessorAttachmentSpec{
			CatchAllRoute: &v1alpha1.CatchAllRouteConfig{
				Hostnames:  []string{"epa.com"},
				BackendRef: v1alpha1.BackendRef{Name: svcEPASvc, Namespace: "ns", Port: 80},
			},
		},
	}

	merged := mergeWithEPACatchAll(nil, epa)
	if len(merged) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(merged))
	}
	if merged[0].BackendRef.Name != svcEPASvc {
		t.Errorf("expected epa-svc, got %s", merged[0].BackendRef.Name)
	}
}

func TestMergeWithEPACatchAll_EPAOverrides(t *testing.T) {
	routeEntries := []CatchAllEntry{
		{Hostname: "shared.com", BackendRef: v1alpha1.BackendRef{Name: "route-svc", Namespace: "ns", Port: 80}},
		{Hostname: "route-only.com", BackendRef: v1alpha1.BackendRef{Name: "route-svc", Namespace: "ns", Port: 80}},
	}
	epa := &v1alpha1.ExternalProcessorAttachment{
		Spec: v1alpha1.ExternalProcessorAttachmentSpec{
			CatchAllRoute: &v1alpha1.CatchAllRouteConfig{
				Hostnames:  []string{"shared.com", "epa-only.com"},
				BackendRef: v1alpha1.BackendRef{Name: svcEPASvc, Namespace: "ns", Port: 80},
			},
		},
	}

	merged := mergeWithEPACatchAll(routeEntries, epa)
	if len(merged) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(merged))
	}

	entryMap := make(map[string]string)
	for _, e := range merged {
		entryMap[e.Hostname] = e.BackendRef.Name
	}

	if entryMap["shared.com"] != svcEPASvc {
		t.Errorf("EPA should override route for shared.com, got %s", entryMap["shared.com"])
	}
	if entryMap["route-only.com"] != "route-svc" {
		t.Errorf("route-only.com should keep route backend, got %s", entryMap["route-only.com"])
	}
	if entryMap["epa-only.com"] != svcEPASvc {
		t.Errorf("epa-only.com should have EPA backend, got %s", entryMap["epa-only.com"])
	}
}

func TestMergeWithEPACatchAll_Empty(t *testing.T) {
	epa := &v1alpha1.ExternalProcessorAttachment{}
	merged := mergeWithEPACatchAll(nil, epa)
	if len(merged) != 0 {
		t.Errorf("expected 0 entries, got %d", len(merged))
	}
}

func TestMergeWithEPACatchAll_Sorted(t *testing.T) {
	routeEntries := []CatchAllEntry{
		{Hostname: "z.com", BackendRef: v1alpha1.BackendRef{Name: "svc", Namespace: "ns", Port: 80}},
		{Hostname: hostACom, BackendRef: v1alpha1.BackendRef{Name: "svc", Namespace: "ns", Port: 80}},
		{Hostname: "m.com", BackendRef: v1alpha1.BackendRef{Name: "svc", Namespace: "ns", Port: 80}},
	}
	epa := &v1alpha1.ExternalProcessorAttachment{}

	merged := mergeWithEPACatchAll(routeEntries, epa)
	if len(merged) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(merged))
	}
	if merged[0].Hostname != hostACom || merged[1].Hostname != "m.com" || merged[2].Hostname != "z.com" {
		t.Errorf("expected sorted order, got: %s, %s, %s", merged[0].Hostname, merged[1].Hostname, merged[2].Hostname)
	}
}
