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
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	syncv1alpha1 "github.com/fluxcd/starling/api/v1alpha1"
)

// controller-runtime treats errors very seriously and prints a big
// stack trace and so on. In some cases, there's a problem that will
// probably go away, no big deal; for those, I'll just log it and
// RequeueAfter, rather than returning an error. (This misses out on
// the backoff behaviour, but so be it).

const clusterUnreadyRetryDelay = 20 * time.Second
const transitoryErrorRetryDelay = 20 * time.Second
const dependenciesUnreadyRetryDelay = 20 * time.Second

const statusInterval = 20 * time.Second

const debug = 1
const trace = 2

// SyncReconciler reconciles a Sync object
type SyncReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
	mapper meta.RESTMapper
}

// +kubebuilder:rbac:groups=sync.fluxcd.io,resources=syncs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sync.fluxcd.io,resources=syncs/status,verbs=get;update;patch

func (r *SyncReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("sync", req.NamespacedName)

	var sync syncv1alpha1.Sync
	if err := r.Get(ctx, req.NamespacedName, &sync); err != nil {
		// Nothing I can do here; if it's something other than not
		// found, let it be requeued.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	s := localSyncHelper{SyncReconciler: r, sync: &sync}

	// Check whether we need to actually run a sync. There are two conditions:
	//  - at least Interval has passed since the last sync
	//  - the source has changed
	now := time.Now().UTC()
	if ok, when := needsApply(&sync.Spec, &sync.Status, now); !ok {
		// May still want to update the resource statuses; see if it's
		// time to do that
		if ok, whenStatus := needsStatus(&sync.Status, now); ok {
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

	tmpdir, err := ioutil.TempDir("", "sync-")
	if err != nil {
		// failing to create a tmpdir qualifies as a proper error
		return ctrl.Result{}, err
	}
	defer os.RemoveAll(tmpdir)

	return apply(ctx, log, s, tmpdir, nil, now)
}

func (r *SyncReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// TODO I have no idea if this is what you are supposed to do
	r.mapper = mgr.GetRESTMapper()
	return ctrl.NewControllerManagedBy(mgr).
		For(&syncv1alpha1.Sync{}).
		Complete(r)
}

// ---

type localSyncHelper struct {
	*SyncReconciler
	sync *syncv1alpha1.Sync
}

func (r localSyncHelper) spec() syncv1alpha1.SyncSpec {
	return r.sync.Spec
}

func (r localSyncHelper) modifyStatus(modify func(*syncv1alpha1.SyncStatus)) {
	modify(&r.sync.Status)
}

func (r localSyncHelper) getDependencyStatus(ctx context.Context, name string) (syncv1alpha1.SyncStatus, error) {
	var s syncv1alpha1.Sync
	err := r.Get(ctx, types.NamespacedName{
		Namespace: r.sync.GetNamespace(),
		Name:      name,
	}, &s)
	return s.Status, err
}

func (r localSyncHelper) updateStatus(ctx context.Context) error {
	return r.Status().Update(ctx, r.sync)
}

func (r localSyncHelper) populateResourcesStatus(ctx context.Context, now time.Time) {
	updateResourcesStatus(ctx, r, r.mapper, &r.sync.Status, now)
}
