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
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

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
const sourceRefKey = ".spec.source.gitRepository"

// +kubebuilder:rbac:groups=sync.fluxcd.io,resources=syncgroups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sync.fluxcd.io,resources=syncgroups/status,verbs=get;update;patch

func (r *SyncGroupReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("syncgroup", req.NamespacedName)

	var syncgroup syncv1alpha1.SyncGroup
	if err := r.Get(ctx, req.NamespacedName, &syncgroup); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Now, get a list of syncs that are owned by this syncgroup,
	// compare it with the syncs I _should_ have, and delete or create
	// as appropriate.
	var syncs syncv1alpha1.SyncList
	if err := r.List(ctx, &syncs, client.InNamespace(req.Namespace), client.MatchingFields{syncOwnerKey: req.Name}); err != nil {
		log.Error(err, "listing syncs owned by this syncgroup")
		return ctrl.Result{}, err
	}

	// --- update the status

	// Construct a summary, which will get filled out and put in the
	// status
	summary := &syncv1alpha1.SyncSummary{}
	for _, s := range syncs.Items {
		summary.Count(&s)
	}
	summary.CalcTotal()
	syncgroup.Status.Summary = summary
	if err := r.Status().Update(ctx, &syncgroup); err != nil {
		return ctrl.Result{}, err
	}

	// --- creating, deleting, updating syncs

	// To know whether a given sync has to be updated, speculatively
	// create a new sync spec based on the current state
	spec := syncv1alpha1.SyncSpec{}

	if url := syncgroup.Spec.Source.URL; url != nil {
		spec.Source.URL = *url
	} else if ref := syncgroup.Spec.Source.GitRepository; ref != nil {
		var gitrepo sourcev1alpha1.GitRepository
		if err := r.Get(ctx, types.NamespacedName{
			Namespace: req.NamespacedName.Namespace,
			Name:      ref.Name,
		}, &gitrepo); err != nil {
			return ctrl.Result{}, err
		}
		// Start wherever the git repo is up to
		artifact := gitrepo.GetArtifact()
		if artifact == nil {
			log.V(debug).Info("GitRepository artifact is not ready", "name", gitrepo.Name)
			return ctrl.Result{}, nil
		}
		spec.Source.URL = artifact.URL
		spec.Source.Revision = artifact.Revision
	}
	spec.Source.Paths = syncgroup.Spec.Source.Paths
	spec.Interval = syncgroup.Spec.Interval

	// Write the newly calculated Source to the status, so it can be
	// observed
	syncgroup.Status.ObservedSource = &spec.Source
	if err := r.Status().Update(ctx, &syncgroup); err != nil {
		return ctrl.Result{}, err
	}

	// When the selector is missing entirely, there's just the one
	// possible target -- the local cluster -- so there should be
	// exactly one Sync owned by the SyncGroup.
	if syncgroup.Spec.Selector == nil {
		if len(syncs.Items) == 0 {
			newSync, err := r.createSync(ctx, &syncgroup, req.NamespacedName, spec, nil)
			if err != nil {
				log.Error(err, "failed to create local Sync for SyncGroup")
				return ctrl.Result{}, err
			}
			log.Info("created local Sync", "name", newSync.Name)
		} else {
			// We'll save the first one that has the right spec and ditch the rest.
			var syncsToDelete []syncv1alpha1.Sync
			for i := range syncs.Items {
				if specEquiv(&spec, &syncs.Items[i].Spec) {
					syncsToDelete = append(syncsToDelete, syncs.Items[i+1:]...)
					break
				}
				syncsToDelete = append(syncsToDelete, syncs.Items[i])
			}
			// none match; pick the first and update it, and delete the rest
			if len(syncsToDelete) == len(syncs.Items) {
				syncToKeep := syncsToDelete[0]
				syncsToDelete = syncsToDelete[1:]
				syncToKeep.Spec = spec
				if err := r.Update(ctx, &syncToKeep); err != nil {
					log.Error(err, "failed to update local Sync")
					// this is a problem, but it would be good to
					// attempt to delete the other syncs too, so run
					// on...
				}
			}

			// More than one -- how did that happen? Well anyway, away
			// with them
			for _, sync := range syncsToDelete {
				if err := r.Delete(ctx, &sync, client.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil {
					log.Error(err, "failed to delete extra local Sync", "sync", sync.Name)
					// Best effort, for the minute
					continue
				}
				log.V(debug).Info("deleted extra local Sync", "name", sync.Name)
			}
		}
		return ctrl.Result{}, nil
	}

	// If the selector is not empty, I need to compare against the
	// clusters that match the selector.
	var clusters clusterv1alpha3.ClusterList

	selector, err := syncgroup.Selector()
	if err != nil {
		log.Error(err, fmt.Sprintf("could not make selector from %v", syncgroup.Spec.Selector))
		return ctrl.Result{}, err
	}

	if err := r.List(ctx, &clusters, &client.ListOptions{
		LabelSelector: selector,
	}); err != nil {
		log.Error(err, "failed to list selected clusters")
		return ctrl.Result{}, err
	}

	okSyncs, extraSyncs, clustersToSync := partitionSyncs(syncs.Items, clusters.Items)

	for _, s := range extraSyncs {
		if err := r.Delete(ctx, s, client.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil {
			log.Error(err, "failed to delete Sync")
			// Best effort, for the minute
			continue
		}
		log.V(debug).Info("deleted extra remote Sync", "name", s.Name)
	}

	// Here is where I'd put rolling update logic: within the maximum
	// allowed number of in-progress syncs, give new syncs and (some)
	// okSpecs the new spec. For now I'll just update any Sync that's
	// different.

	for _, sync := range okSyncs {
		if !specEquiv(&sync.Spec, &spec) {
			// I have to be careful not to upset the clusterRef, which
			// is _not_ part of the newSpec constructed above, since
			// it's copied into all the syncs created/updated.
			clusterRef := sync.Spec.Cluster
			sync.Spec = spec
			sync.Spec.Cluster = clusterRef
			if err := r.Update(ctx, sync); err != nil {
				log.Error(err, "failed to update spec of existing sync", "name", sync.GetName())
				// Best effort, as with other things; it'll come round
				// again
				continue
			}
			log.V(debug).Info("updated Sync with new spec", "name", sync.GetName())
		}
	}

	// Lastly, these clusters are missing syncs; create one for each,
	// with the new spec. This may _also_ be subject to the rolling
	// upgrade policy in the future.

	for name := range clustersToSync {
		newSync, err := r.createSync(ctx, &syncgroup, req.NamespacedName, spec, &corev1.LocalObjectReference{
			Name: name,
		})
		if err != nil {
			log.Error(err, "failed to create Sync")
			return ctrl.Result{}, err
		}
		log.Info("created remote Sync", "name", newSync.Name)
	}

	return ctrl.Result{}, nil
}

func (r *SyncGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// This sets up an index on the owner (a SyncGroup) of Sync
	// objects. This is so we can list them easily when it comes to
	// reconcile a SyncGroup.
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

	// Index the git repository object that a SyncGroup refers to, if any
	if err := mgr.GetFieldIndexer().IndexField(&syncv1alpha1.SyncGroup{}, sourceRefKey, func(obj runtime.Object) []string {
		syncgroup := obj.(*syncv1alpha1.SyncGroup)
		if ref := syncgroup.Spec.Source.GitRepository; ref != nil {
			return []string{ref.Name}
		}
		return nil
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&syncv1alpha1.SyncGroup{}).
		// any time a Sync changes, reconcile its owner SyncGroup
		Owns(&syncv1alpha1.Sync{}).
		// when a GitRepository changes, reconcile any SyncGroups that
		// refer to it
		Watches(&source.Kind{Type: &sourcev1alpha1.GitRepository{}},
			&handler.EnqueueRequestsFromMapFunc{
				ToRequests: handler.ToRequestsFunc(r.syncGroupsForGitRepo),
			}).
		Watches(&source.Kind{Type: &clusterv1alpha3.Cluster{}},
			&handler.EnqueueRequestsFromMapFunc{
				ToRequests: handler.ToRequestsFunc(r.syncGroupsForCluster),
			}).
		Complete(r)
}

// syncGroupsForGitRepo gets all the SyncGroup objects that use a
// particular GitRepository object, so that they can keep up to date
// with it. This requires the index for field sourceRefKey to have
// been added.
func (r SyncGroupReconciler) syncGroupsForGitRepo(obj handler.MapObject) []reconcile.Request {
	ctx := context.Background()
	var syncgroups syncv1alpha1.SyncGroupList
	if err := r.List(ctx, &syncgroups, client.InNamespace(obj.Meta.GetNamespace()), client.MatchingFields{sourceRefKey: obj.Meta.GetName()}); err != nil {
		r.Log.Error(err, "failed to list SyncGroups for GitRepository", "name", types.NamespacedName{
			Name:      obj.Meta.GetName(),
			Namespace: obj.Meta.GetNamespace(),
		})
		return nil
	}
	reqs := make([]reconcile.Request, len(syncgroups.Items), len(syncgroups.Items))
	for i := range syncgroups.Items {
		reqs[i].NamespacedName.Name = syncgroups.Items[i].GetName()
		reqs[i].NamespacedName.Namespace = syncgroups.Items[i].GetNamespace()
	}
	return reqs
}

func (r *SyncGroupReconciler) createSync(ctx context.Context, syncgroup *syncv1alpha1.SyncGroup, nsname types.NamespacedName, spec syncv1alpha1.SyncSpec, clusterRef *corev1.LocalObjectReference) (*syncv1alpha1.Sync, error) {
	var sync syncv1alpha1.Sync
	sync.Namespace = nsname.Namespace
	sync.Spec = spec
	if clusterRef != nil {
		sync.Name = nsname.Name + "-cluster-" + clusterRef.Name
		sync.Spec.Cluster = clusterRef
	} else {
		sync.Name = nsname.Name + "-local"
	}

	// TODO should this really be the controller reference? That is
	// supposedly for pointing at the controller. Other things seem to
	// work this way.
	if err := ctrl.SetControllerReference(syncgroup, &sync, r.Scheme); err != nil {
		return nil, err
	}

	return &sync, r.Create(ctx, &sync)
}

func partitionSyncs(syncs []syncv1alpha1.Sync, clusters []clusterv1alpha3.Cluster) (ok, extra []*syncv1alpha1.Sync, missing map[string]struct{}) {
	// Figure out which clusters have a Sync attached to them, which
	// Syncs are extraneous, and which are just right.
	clustersToSync := map[string]struct{}{}
	var extraSyncs []*syncv1alpha1.Sync
	var okSyncs []*syncv1alpha1.Sync

	for _, c := range clusters {
		// Only include it as a live cluster if it's provisioned and
		// the control plane is ready and the infrastructure is ready;
		// this is as close an indication as you get for a cluster
		// that it is ready to be used, as far as I can tell.
		if c.Status.GetTypedPhase() == clusterv1alpha3.ClusterPhaseProvisioned &&
			c.Status.ControlPlaneReady && c.Status.InfrastructureReady {
			clustersToSync[c.Name] = struct{}{}
		}
	}
	for _, s := range syncs {
		if s.Spec.Cluster == nil {
			extraSyncs = append(extraSyncs, &s)
			continue
		}
		clusterName := s.Spec.Cluster.Name
		if _, ok := clustersToSync[clusterName]; ok {
			okSyncs = append(okSyncs, &s)
			delete(clustersToSync, clusterName)
		} else {
			extraSyncs = append(extraSyncs, &s)
		}
	}
	return okSyncs, extraSyncs, clustersToSync
}

// Is spec1 equivalent to spec2, modulo the cluster reference?
func specEquiv(spec1, spec2 *syncv1alpha1.SyncSpec) bool {
	return spec1.Interval == spec2.Interval &&
		(&spec1.Source).Equiv(&spec2.Source)
}
