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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
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

	var syncs syncv1alpha1.SyncList
	if err := r.List(ctx, &syncs, client.InNamespace(req.Namespace), client.MatchingFields{syncOwnerKey: req.Name}); err != nil {
		log.Error(err, "listing syncs owned by this syncgroup")
		return ctrl.Result{RequeueAfter: retryDelay}, err
	}

	// I have a list of syncs that are owned by this syncgroup. Now to
	// go compare it with the syncs I _should_ have, and delete or
	// create as appropriate.

	// At present, using a selector to target clusters is not
	// implemented. So there's just the one possible target -- the
	// local cluster -- so there should be exactly one Sync owned by
	// each syncgroup.
	switch len(syncs.Items) {
	case 0:
		if err := r.createSync(ctx, req.NamespacedName); err != nil {
			log.Error(err, "failed to create sync for syncgroup")
			return ctrl.Result{RequeueAfter: retryDelay}, err
		}
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
		}
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

func (r *SyncGroupReconciler) createSync(ctx context.Context, nsname types.NamespacedName) error {
	var sync syncv1alpha1.Sync
	sync.Namespace = nsname.Namespace
	sync.Name = nsname.Name + "-local" // TODO just while we have only local cluster

	var syncgroup syncv1alpha1.SyncGroup
	if err := r.Get(ctx, nsname, &syncgroup); err != nil {
		return err
	}

	if err := ctrl.SetControllerReference(&syncgroup, &sync, r.Scheme); err != nil {
		return err
	}

	if url := syncgroup.Spec.Source.URL; url != nil {
		sync.Spec.URL = *url
	} else if ref := syncgroup.Spec.Source.GitRepository; ref != nil {
		var gitrepo sourcev1alpha1.GitRepository
		if err := r.Get(ctx, types.NamespacedName{
			Namespace: nsname.Namespace,
			Name:      ref.Name,
		}, &gitrepo); err != nil {
			return err
		}
		// Start wherever the git repo is up to
		artifact := gitrepo.GetArtifact()
		if artifact == nil {
			return fmt.Errorf("artifact for referenced git repository %q is not ready", gitrepo.Name)
		}
		sync.Spec.URL = artifact.URL
	}

	sync.Spec.Paths = syncgroup.Spec.Source.Paths
	sync.Spec.Interval = syncgroup.Spec.Interval

	return r.Create(ctx, &sync)
}
