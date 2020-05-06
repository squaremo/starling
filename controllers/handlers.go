/*
Copyright 2020 Michael Bridgen <mikeb@squaremobius.net>

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

package controllers

import (
	"context"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	syncv1alpha1 "github.com/fluxcd/starling/api/v1alpha1"
)

// This implements handler.EventHandlers to track 3rd party things
// coming and going, so I can trigger reconciles for the SyncGroup
// objects that refer to them.
//
//     https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/handler?tab=doc#EnqueueRequestsFromMapFunc

// syncGroupsForCluster calculates the SyncGroup objects that select a
// particular cluster. Whether the cluster has been created, deleted,
// or updated (in which case this is called with the old object and
// new object consecutively), any SyncGroup objects that select it may
// need to be reconciled.
func (r *SyncGroupReconciler) syncGroupsForCluster(obj handler.MapObject) []reconcile.Request {
	ctx := context.Background()
	var syncgroups syncv1alpha1.SyncGroupList
	if err := r.List(ctx, &syncgroups, client.InNamespace(obj.Meta.GetNamespace())); err != nil {
		return nil
	}

	var requests []reconcile.Request
	for _, syncgroup := range syncgroups.Items {
		name := types.NamespacedName{
			Name:      syncgroup.GetName(),
			Namespace: syncgroup.GetNamespace(),
		}
		if syncgroup.Spec.Selector == nil {
			r.Log.V(trace).Info("SyncGroup has nil selector, skipping", "name", name)
			continue
		}
		selector, err := syncgroup.Selector()
		if err != nil {
			r.Log.V(debug).Info("SyncGroup has invalid selector", "name", name)
			continue
		}
		if selector.Matches(labels.Set(obj.Meta.GetLabels())) {
			requests = append(requests, reconcile.Request{
				NamespacedName: name,
			})
		}
	}
	return requests
}
