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
	"fmt"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clusterv1alpha3 "sigs.k8s.io/cluster-api/api/v1alpha3"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	sourcev1alpha1 "github.com/fluxcd/source-controller/api/v1alpha1"
	syncv1alpha1 "github.com/fluxcd/starling/api/v1alpha1"
)

// SyncGroupReconciler reconciles a SyncGroup object. This means,
// essentially, making sure that a Sync object exists and has the
// correct spec, for every cluster selected by the SyncGroup.
type SyncGroupReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

const syncOwnerKey = ".metadata.controller"

// +kubebuilder:rbac:groups=sync.fluxcd.io,resources=syncgroups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sync.fluxcd.io,resources=syncgroups/status,verbs=get;update;patch

func (r *SyncGroupReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("syncgroup", req.NamespacedName)

	var syncgroup syncv1alpha1.SyncGroup
	if err := r.Get(ctx, req.NamespacedName, &syncgroup); err != nil {
		if client.IgnoreNotFound(err) != nil {
			log.Error(err, "failed to get syncgroup")
			return ctrl.Result{RequeueAfter: retryDelay}, err
		}
		// Not found; must have been deleted
		return ctrl.Result{}, nil
	}

	var syncs syncv1alpha1.SyncList
	if err := r.List(ctx, &syncs, client.InNamespace(req.Namespace), client.MatchingFields{syncOwnerKey: req.Name}); err != nil {
		log.Error(err, "listing syncs owned by this syncgroup")
		return ctrl.Result{RequeueAfter: retryDelay}, err
	}

	// I have a list of syncs that are owned by this syncgroup. Now to
	// go compare it with the syncs I _should_ have, and delete or
	// create as appropriate.

	// When the selector is missing entirely, there's just the one
	// possible target -- the local cluster -- so there should be
	// exactly one Sync owned by the SyncGroup.
	if syncgroup.Spec.Selector == nil {
		switch len(syncs.Items) {
		case 0:
			newSync, err := r.createSync(ctx, req.NamespacedName, nil)
			if err != nil {
				log.Error(err, "failed to create sync for syncgroup")
				return ctrl.Result{RequeueAfter: retryDelay}, err
			}
			log.Info("created Sync", "name", newSync.Name)
		case 1:
			// perfect
		default:
			// More than one -- how did that happen? Well anyway, delete
			// all but one
			for _, sync := range syncs.Items[1:] {
				if err := r.Delete(ctx, &sync, client.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil {
					log.Error(err, "failed to delete extraneous Sync", "sync", sync.Name)
					return ctrl.Result{RequeueAfter: retryDelay}, err
				}
				log.Info("deleted Sync", "name", sync.Name)
			}
		}
		return ctrl.Result{}, nil
	}

	// If the selector is not empty, I need to compare against the
	// clusters that match the selector.
	var clusters clusterv1alpha3.ClusterList

	selector, err := metav1.LabelSelectorAsSelector(syncgroup.Spec.Selector)
	if err != nil {
		log.Error(err, fmt.Sprintf("could not make selector from %v", syncgroup.Spec.Selector))
		return ctrl.Result{}, err
	}

	if err := r.List(ctx, &clusters, &client.ListOptions{
		LabelSelector: selector,
	}); err != nil {
		log.Error(err, "failed to list selected clusters")
		return ctrl.Result{RequeueAfter: retryDelay}, err
	}

	clustersToSync := map[string]struct{}{}
	var extraSyncs []*syncv1alpha1.Sync

	for _, c := range clusters.Items {
		clustersToSync[c.Name] = struct{}{}
	}
	for _, s := range syncs.Items {
		if s.Spec.Cluster == nil {
			extraSyncs = append(extraSyncs, &s)
			continue
		}
		if _, ok := clustersToSync[s.Spec.Cluster.Name]; ok {
			delete(clustersToSync, s.Spec.Cluster.Name)
		} else {
			extraSyncs = append(extraSyncs, &s)
		}
	}

	for _, s := range extraSyncs {
		if err := r.Delete(ctx, s, client.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil {
			log.Error(err, "failed to delete Sync")
			return ctrl.Result{RequeueAfter: retryDelay}, err
		}
		log.Info("deleted Sync", "name", s.Name)
	}

	for name := range clustersToSync {
		newSync, err := r.createSync(ctx, req.NamespacedName, &corev1.LocalObjectReference{
			Name: name,
		})
		if err != nil {
			log.Error(err, "failed to create Sync")
			return ctrl.Result{RequeueAfter: retryDelay}, err
		}
		log.Info("created Sync", "name", newSync.Name)
	}

	return ctrl.Result{}, nil
}

func (r *SyncGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(&syncv1alpha1.Sync{}, syncOwnerKey, func(obj runtime.Object) []string {
		sync := obj.(*syncv1alpha1.Sync)
		owner := metav1.GetControllerOf(sync)
		if owner == nil {
			return nil
		}
		if owner.APIVersion != syncv1alpha1.GroupVersion.String() ||
			owner.Kind != "SyncGroup" {
			return nil
		}
		return []string{owner.Name}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&syncv1alpha1.SyncGroup{}).
		Complete(r)
}

func (r *SyncGroupReconciler) createSync(ctx context.Context, nsname types.NamespacedName, clusterRef *corev1.LocalObjectReference) (*syncv1alpha1.Sync, error) {
	var sync syncv1alpha1.Sync
	sync.Namespace = nsname.Namespace
	if clusterRef != nil {
		sync.Name = nsname.Name + "-cluster-" + clusterRef.Name
		sync.Spec.Cluster = clusterRef
	} else {
		sync.Name = nsname.Name + "-local"
	}

	var syncgroup syncv1alpha1.SyncGroup
	if err := r.Get(ctx, nsname, &syncgroup); err != nil {
		return nil, err
	}

	// TODO should this really be the controller reference? That is
	// supposedly for pointing at the controller. Other things seem to
	// work this way.
	if err := ctrl.SetControllerReference(&syncgroup, &sync, r.Scheme); err != nil {
		return nil, err
	}

	if url := syncgroup.Spec.Source.URL; url != nil {
		sync.Spec.URL = *url
	} else if ref := syncgroup.Spec.Source.GitRepository; ref != nil {
		var gitrepo sourcev1alpha1.GitRepository
		if err := r.Get(ctx, types.NamespacedName{
			Namespace: nsname.Namespace,
			Name:      ref.Name,
		}, &gitrepo); err != nil {
			return nil, err
		}
		// Start wherever the git repo is up to
		artifact := gitrepo.GetArtifact()
		if artifact == nil {
			return nil, fmt.Errorf("artifact for referenced git repository %q is not ready", gitrepo.Name)
		}
		sync.Spec.URL = artifact.URL
	}

	sync.Spec.Paths = syncgroup.Spec.Source.Paths
	sync.Spec.Interval = syncgroup.Spec.Interval

	return &sync, r.Create(ctx, &sync)
}
