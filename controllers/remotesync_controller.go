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
	"io/ioutil"
	"os"
	"path/filepath"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clusterv1alpha3 "sigs.k8s.io/cluster-api/api/v1alpha3"
	kcfg "sigs.k8s.io/cluster-api/util/kubeconfig"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	syncv1alpha1 "github.com/fluxcd/starling/api/v1alpha1"
)

// RemoteSyncReconciler reconciles a RemoteSync object
type RemoteSyncReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme

	mapper meta.RESTMapper
}

// +kubebuilder:rbac:groups=sync.fluxcd.io,resources=remotesyncs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sync.fluxcd.io,resources=remotesyncs/status,verbs=get;update;patch

func (r *RemoteSyncReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("sync", req.NamespacedName)

	var sync syncv1alpha1.RemoteSync
	if err := r.Get(ctx, req.NamespacedName, &sync); err != nil {
		// Nothing I can do here; if it's something other than not
		// found, let it be requeued.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// This makes a few of the procedures more uniform
	s := remoteSyncHelper{RemoteSyncReconciler: r, sync: &sync}

	// Check whether we need to actually run a sync. There are two conditions:
	//  - at least Interval has passed since the last sync
	//  - the source has changed
	now := time.Now().UTC()

	if ok, when := needsApply(&sync.Spec.SyncSpec, &sync.Status.SyncStatus, now); !ok {
		// May still want to update the resource statuses; see if it's
		// time to do that
		if ok, whenStatus := needsStatus(&sync.Status.SyncStatus, now); ok {
			s.populateResourcesStatus(ctx, now)
			if err := s.updateStatus(ctx); err != nil {
				return ctrl.Result{}, err
			}
		} else {
			// reschedule for the earliest of when apply or status needs
			// attention
			if whenStatus < when {
				when = whenStatus
			}
		}
		return ctrl.Result{RequeueAfter: when}, nil
	} // otherwise let it run on to do the sync

	// Find the cluster and get access to it

	var cluster clusterv1alpha3.Cluster
	clusterName := types.NamespacedName{
		Name:      sync.Spec.ClusterRef.Name,
		Namespace: sync.GetNamespace(),
	}
	if err := r.Get(ctx, clusterName, &cluster); err != nil {
		// If this RemoteSync refers to a missing cluster, it'll get
		// deleted at some point anyway.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Only treat a cluster as sync'able if it's provisioned and the
	// control plane is initialised and the infrastructure is ready;
	// this is as close an indication as you get for a cluster that it
	// is ready to be used, as far as I can tell.
	if !(cluster.Status.GetTypedPhase() == clusterv1alpha3.ClusterPhaseProvisioned &&
		cluster.Status.ControlPlaneInitialized &&
		cluster.Status.InfrastructureReady) {
		// This isn't an error, but we do want to try again at
		// some point. Watching clusters would be one way to do
		// this, but just looking again is good enough for now.
		log.V(debug).Info("cluster not ready", "cluster", clusterName)
		return ctrl.Result{RequeueAfter: clusterUnreadyRetryDelay}, nil
	}

	tmpdir, err := ioutil.TempDir("", "sync-")
	if err != nil {
		// failing to create a tmpdir qualifies as a proper error
		return ctrl.Result{}, err
	}
	defer os.RemoveAll(tmpdir)

	kubeconfig, err := kcfg.FromSecret(ctx, r.Client, clusterName)
	if err != nil {
		// If it can't fnd the kubeconfig, it can't proceed. Maybe
		// the cluster isn't ready yet. In any case, it's not a
		// fatal problem; just retry in a bit.
		log.V(debug).Info("failed to get kubeconfig secret for cluster", "error", err, "cluster", clusterName)
		return ctrl.Result{RequeueAfter: transitoryErrorRetryDelay}, nil
	}
	kubeconfigPath := filepath.Join(tmpdir, "kubeconfig")
	if err := ioutil.WriteFile(kubeconfigPath, kubeconfig, os.FileMode(0400)); err != nil {
		// If it can't write to the filesystem, that _is_ a problem
		return ctrl.Result{}, err
	}
	applyArgs := []string{"--kubeconfig", kubeconfigPath}

	return apply(ctx, log, s, tmpdir, applyArgs, now)
}

func (r *RemoteSyncReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.mapper = mgr.GetRESTMapper()
	return ctrl.NewControllerManagedBy(mgr).
		For(&syncv1alpha1.RemoteSync{}).
		Complete(r)
}

// ---

type remoteSyncHelper struct {
	*RemoteSyncReconciler
	sync *syncv1alpha1.RemoteSync
}

func (r remoteSyncHelper) spec() syncv1alpha1.SyncSpec {
	return r.sync.Spec.SyncSpec
}

func (r remoteSyncHelper) modifyStatus(modify func(*syncv1alpha1.SyncStatus)) {
	modify(&r.sync.Status.SyncStatus)
}

func (r remoteSyncHelper) getDependencyStatus(ctx context.Context, name string) (syncv1alpha1.SyncStatus, error) {
	var s syncv1alpha1.RemoteSync
	err := r.Get(ctx, types.NamespacedName{
		Namespace: r.sync.GetNamespace(),
		Name:      name,
	}, &s)
	return s.Status.SyncStatus, err
}

func (r remoteSyncHelper) updateStatus(ctx context.Context) error {
	return r.Status().Update(ctx, r.sync)
}

func (r remoteSyncHelper) populateResourcesStatus(ctx context.Context, now time.Time) {
	updateResourcesStatus(ctx, r, r.mapper, &r.sync.Status.SyncStatus, now)
}
