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
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/freepik-company/customrouter/api/v1alpha1"
	ef "github.com/freepik-company/customrouter/internal/controller/envoyfilter"
)

// reconcileCatchAllFromRoutes aggregates catchAllRoute declarations from all CustomHTTPRoutes,
// merges them with each EPA's own catchAllRoute, and generates the catchall EnvoyFilter.
// The EnvoyFilter is owned by the EPA (not the CustomHTTPRoute) to avoid race conditions
// between the two controllers.
func (r *CustomHTTPRouteReconciler) reconcileCatchAllFromRoutes(
	ctx context.Context,
	routeList *v1alpha1.CustomHTTPRouteList,
) error {
	logger := log.FromContext(ctx)

	entries := ef.CollectCatchAllEntries(routeList)

	epaList := &v1alpha1.ExternalProcessorAttachmentList{}
	if err := r.List(ctx, epaList); err != nil {
		return fmt.Errorf("failed to list ExternalProcessorAttachments: %w", err)
	}

	if len(epaList.Items) == 0 {
		if len(entries) > 0 {
			logger.Info("CustomHTTPRoutes declare catchAllRoute but no ExternalProcessorAttachment exists, skipping catchall EnvoyFilter")
		}
		return nil
	}

	for i := range epaList.Items {
		epa := &epaList.Items[i]

		merged := ef.MergeCatchAllEntries(entries, epa)

		if len(merged) == 0 {
			key := types.NamespacedName{
				Name:      epa.Name + ef.CatchAllFilterSuffix,
				Namespace: epa.Namespace,
			}
			if err := ef.DeleteEnvoyFilter(ctx, r.Client, key); err != nil {
				return err
			}
			continue
		}

		envoyFilter, err := ef.BuildCatchAllEnvoyFilter(epa, merged)
		if err != nil {
			return fmt.Errorf("failed to build catch-all EnvoyFilter for EPA %s/%s: %w",
				epa.Namespace, epa.Name, err)
		}

		if err := ef.UpsertUnstructured(ctx, r.Client, envoyFilter); err != nil {
			return fmt.Errorf("failed to reconcile catch-all EnvoyFilter for EPA %s/%s: %w",
				epa.Namespace, epa.Name, err)
		}

		logger.Info("Catch-all EnvoyFilter reconciled from CustomHTTPRoutes",
			"epa", epa.Name,
			"namespace", epa.Namespace,
			"hostnameCount", len(merged))
	}

	return nil
}
