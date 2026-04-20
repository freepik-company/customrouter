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

package customhttproute

import (
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/freepik-company/customrouter/api/v1alpha1"
	"github.com/freepik-company/customrouter/internal/controller"
	ef "github.com/freepik-company/customrouter/internal/controller/envoyfilter"
)

func newRouteWithCatchAll(namespace, name string, hostnames []string) v1alpha1.CustomHTTPRoute {
	return v1alpha1.CustomHTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec: v1alpha1.CustomHTTPRouteSpec{
			Hostnames: hostnames,
			CatchAllRoute: &v1alpha1.CatchAllBackendRef{
				BackendRef: v1alpha1.BackendRef{Name: "backend", Namespace: namespace, Port: 80},
			},
		},
	}
}

func newEPAWithCatchAll(namespace, name string, hostnames []string) v1alpha1.ExternalProcessorAttachment {
	return v1alpha1.ExternalProcessorAttachment{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec: v1alpha1.ExternalProcessorAttachmentSpec{
			CatchAllRoute: &v1alpha1.CatchAllRouteConfig{
				Hostnames:  hostnames,
				BackendRef: v1alpha1.BackendRef{Name: "epa-backend", Namespace: namespace, Port: 80},
			},
		},
	}
}

func TestEvaluateCatchAllProgrammed_NotConfigured(t *testing.T) {
	route := &v1alpha1.CustomHTTPRoute{}
	got := ef.EvaluateCatchAllProgrammed(route, &v1alpha1.CustomHTTPRouteList{}, &v1alpha1.ExternalProcessorAttachmentList{})
	if got.Programmed || got.Reason != controller.ConditionReasonCatchAllNotConfigured {
		t.Errorf("expected NotConfigured, got %+v", got)
	}
}

func TestEvaluateCatchAllProgrammed_NoEPA(t *testing.T) {
	route := newRouteWithCatchAll("ns", "r", []string{"a.com"})
	routeList := &v1alpha1.CustomHTTPRouteList{Items: []v1alpha1.CustomHTTPRoute{route}}
	got := ef.EvaluateCatchAllProgrammed(&route, routeList, &v1alpha1.ExternalProcessorAttachmentList{})
	if got.Programmed || got.Reason != controller.ConditionReasonCatchAllNoEPA {
		t.Errorf("expected NoExternalProcessor, got %+v", got)
	}
}

func TestEvaluateCatchAllProgrammed_Programmed(t *testing.T) {
	route := newRouteWithCatchAll("ns", "r", []string{"a.com", "b.com"})
	routeList := &v1alpha1.CustomHTTPRouteList{Items: []v1alpha1.CustomHTTPRoute{route}}
	epa := v1alpha1.ExternalProcessorAttachment{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "epa"}}
	epaList := &v1alpha1.ExternalProcessorAttachmentList{Items: []v1alpha1.ExternalProcessorAttachment{epa}}

	got := ef.EvaluateCatchAllProgrammed(&route, routeList, epaList)
	if !got.Programmed || got.Reason != controller.ConditionReasonCatchAllProgrammed {
		t.Fatalf("expected Programmed, got %+v", got)
	}
	if len(got.Hostnames) != 2 {
		t.Errorf("expected 2 programmed hostnames, got %d", len(got.Hostnames))
	}
}

func TestEvaluateCatchAllProgrammed_OverriddenByEPA(t *testing.T) {
	route := newRouteWithCatchAll("ns", "r", []string{"a.com"})
	routeList := &v1alpha1.CustomHTTPRouteList{Items: []v1alpha1.CustomHTTPRoute{route}}
	epa := newEPAWithCatchAll("ns", "epa", []string{"a.com"})
	epaList := &v1alpha1.ExternalProcessorAttachmentList{Items: []v1alpha1.ExternalProcessorAttachment{epa}}

	got := ef.EvaluateCatchAllProgrammed(&route, routeList, epaList)
	if got.Programmed || got.Reason != controller.ConditionReasonCatchAllOverriddenByEPA {
		t.Errorf("expected OverriddenByEPA, got %+v", got)
	}
}

func TestEvaluateCatchAllProgrammed_OverriddenByRoute(t *testing.T) {
	winner := newRouteWithCatchAll("ns", "a-winner", []string{"a.com"})
	loser := newRouteWithCatchAll("ns", "z-loser", []string{"a.com"})
	routeList := &v1alpha1.CustomHTTPRouteList{Items: []v1alpha1.CustomHTTPRoute{winner, loser}}
	epa := v1alpha1.ExternalProcessorAttachment{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "epa"}}
	epaList := &v1alpha1.ExternalProcessorAttachmentList{Items: []v1alpha1.ExternalProcessorAttachment{epa}}

	got := ef.EvaluateCatchAllProgrammed(&loser, routeList, epaList)
	if got.Programmed || got.Reason != controller.ConditionReasonCatchAllOverriddenByRoute {
		t.Errorf("expected OverriddenByRoute for loser, got %+v", got)
	}

	gotWinner := ef.EvaluateCatchAllProgrammed(&winner, routeList, epaList)
	if !gotWinner.Programmed || gotWinner.Reason != controller.ConditionReasonCatchAllProgrammed {
		t.Errorf("expected Programmed for winner, got %+v", gotWinner)
	}
}

func TestEvaluateCatchAllProgrammed_MixedLossesFavorEPAReason(t *testing.T) {
	// route loses "a.com" to another route and "b.com" to an EPA → OverriddenByEPA wins.
	loser := newRouteWithCatchAll("ns", "z-loser", []string{"a.com", "b.com"})
	winner := newRouteWithCatchAll("ns", "a-winner", []string{"a.com"})
	routeList := &v1alpha1.CustomHTTPRouteList{Items: []v1alpha1.CustomHTTPRoute{winner, loser}}
	epa := newEPAWithCatchAll("ns", "epa", []string{"b.com"})
	epaList := &v1alpha1.ExternalProcessorAttachmentList{Items: []v1alpha1.ExternalProcessorAttachment{epa}}

	got := ef.EvaluateCatchAllProgrammed(&loser, routeList, epaList)
	if got.Programmed || got.Reason != controller.ConditionReasonCatchAllOverriddenByEPA {
		t.Errorf("expected OverriddenByEPA with mixed losses, got %+v", got)
	}
}

func TestUpdateConditionCatchAllProgrammed_TrueAndFalse(t *testing.T) {
	r := &CustomHTTPRouteReconciler{}
	object := &v1alpha1.CustomHTTPRoute{ObjectMeta: metav1.ObjectMeta{Generation: 7}}

	r.UpdateConditionCatchAllProgrammed(object, ef.CatchAllProgrammedStatus{
		Programmed: true,
		Reason:     controller.ConditionReasonCatchAllProgrammed,
	})
	cond := meta.FindStatusCondition(object.Status.Conditions, v1alpha1.ConditionTypeCatchAllProgrammed)
	if cond == nil {
		t.Fatal("expected condition to be set")
	}
	if cond.Status != metav1.ConditionTrue {
		t.Errorf("expected ConditionTrue, got %s", cond.Status)
	}
	if cond.Reason != controller.ConditionReasonCatchAllProgrammed {
		t.Errorf("expected reason Programmed, got %s", cond.Reason)
	}
	if cond.Message != controller.ConditionReasonCatchAllProgrammedMessage {
		t.Errorf("expected Programmed message, got %q", cond.Message)
	}
	if cond.ObservedGeneration != 7 {
		t.Errorf("expected observedGeneration=7, got %d", cond.ObservedGeneration)
	}

	r.UpdateConditionCatchAllProgrammed(object, ef.CatchAllProgrammedStatus{
		Reason: controller.ConditionReasonCatchAllOverriddenByRoute,
	})
	cond = meta.FindStatusCondition(object.Status.Conditions, v1alpha1.ConditionTypeCatchAllProgrammed)
	if cond.Status != metav1.ConditionFalse {
		t.Errorf("expected ConditionFalse after transition, got %s", cond.Status)
	}
	if cond.Reason != controller.ConditionReasonCatchAllOverriddenByRoute {
		t.Errorf("expected reason OverriddenByRoute, got %s", cond.Reason)
	}
}

func TestCatchAllMessageFor_AllReasons(t *testing.T) {
	cases := map[string]string{
		controller.ConditionReasonCatchAllProgrammed:        controller.ConditionReasonCatchAllProgrammedMessage,
		controller.ConditionReasonCatchAllNotConfigured:     controller.ConditionReasonCatchAllNotConfiguredMessage,
		controller.ConditionReasonCatchAllNoEPA:             controller.ConditionReasonCatchAllNoEPAMessage,
		controller.ConditionReasonCatchAllOverriddenByEPA:   controller.ConditionReasonCatchAllOverriddenByEPAMessage,
		controller.ConditionReasonCatchAllOverriddenByRoute: controller.ConditionReasonCatchAllOverriddenByRouteMessage,
	}
	for reason, want := range cases {
		if got := catchAllMessageFor(reason); got != want {
			t.Errorf("reason %q: expected %q, got %q", reason, want, got)
		}
	}
	if got := catchAllMessageFor("Unknown"); got != "" {
		t.Errorf("unknown reason should return empty string, got %q", got)
	}
}
