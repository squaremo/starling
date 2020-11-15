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

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clusterv1alpha3 "sigs.k8s.io/cluster-api/api/v1alpha3"
	"sigs.k8s.io/cluster-api/controllers/remote"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	syncv1alpha1 "github.com/fluxcd/starling/api/v1alpha1"
)

// ProxySyncReconciler reconciles a ProxySync object
type ProxySyncReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=sync.fluxcd.io,resources=proxysyncs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sync.fluxcd.io,resources=proxysyncs/status,verbs=get;update;patch

func (r *ProxySyncReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("proxysync", req.NamespacedName)

	var sync syncv1alpha1.ProxySync
	if err := r.Get(ctx, req.NamespacedName, &sync); err != nil {
		// Nothing I can do here; if it's something other than not
		// found, let it be requeued.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Find the cluster and get access to it
	var cluster clusterv1alpha3.Cluster
	clusterName := types.NamespacedName{
		Name:      sync.Spec.ClusterRef.Name,
		Namespace: sync.GetNamespace(),
	}
	if err := r.Get(ctx, clusterName, &cluster); err != nil {
		// If this ProxySync refers to a missing cluster, it'll get
		// deleted at some point anyway.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// FIXME Either check that the cluster is itself ready, or verify that this method does so
	remoteClient, err := remote.NewClusterClient(ctx, r, clusterName, r.Scheme)
	if err != nil {
		// TODO It's worth distinguishing between cluster not ready,
		// and couldn't connect. For now, just back off.
		return ctrl.Result{}, err
	}

	log.V(1).Info("remote cluster connected", "cluster", clusterName)

	var counterpart syncv1alpha1.Sync
	counterpart.Name = sync.GetName()
	counterpart.Namespace = sync.GetNamespace()
	op, err := controllerutil.CreateOrUpdate(ctx, remoteClient, &counterpart, func() error {
		counterpart.Spec = sync.Spec.Template.Spec
		return nil
	})
	if err != nil {
		return ctrl.Result{}, err // let it back-off
	}

	// TODO maintain conditions re creating, etc.

	switch op {
	case controllerutil.OperationResultUpdated:
		// TODO probably more complicated that this
		sync.Status.SyncStatus = counterpart.Status
	}

	if err = r.Status().Update(ctx, &sync); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *ProxySyncReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&syncv1alpha1.ProxySync{}).
		Complete(r)
}
