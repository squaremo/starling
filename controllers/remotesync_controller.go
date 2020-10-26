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
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

	// Check whether we need to actually run a sync. There are two conditions:
	//  - at least Interval has passed since the last sync
	//  - the source has changed
	now := time.Now().UTC()
	if ok, when := needsApply(&sync.Spec.SyncSpec, &sync.Status.SyncStatus, now); !ok {
		// May still want to update the resource statuses; see if it's
		// time to do that
		if ok, whenStatus := needsStatus(&sync.Status.SyncStatus, now); ok {
			updateResourcesStatus(ctx, r, r.mapper, &sync.Status.SyncStatus, now)
			if err := r.Status().Update(ctx, &sync); err != nil {
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

	applyArgs := []string{"apply", "-o", outputJSONPath}

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
	applyArgs = append(applyArgs, "--kubeconfig", kubeconfigPath)

	// Check the dependencies of the sync; if they aren't ready, don't
	// bother with the rest.
	if pending, err := checkDependenciesReady(ctx, func(name string) (syncv1alpha1.SyncStatus, error) {
		var s syncv1alpha1.RemoteSync
		err := r.Get(ctx, types.NamespacedName{
			Namespace: sync.GetNamespace(),
			Name:      name,
		}, &s)
		return s.Status.SyncStatus, err
	}, sync.Spec.Dependencies); err != nil {
		return ctrl.Result{}, err
	} else if len(pending) > 0 {
		log.Info("dependencies not ready", "pending", pending)
		sync.Status.PendingDependencies = pending
		if err := r.Status().Update(ctx, &sync); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: dependenciesUnreadyRetryDelay}, nil
	}

	// Attempt to fetch the URL of the package being synced. Many of
	// the possible failures here will be transitory, so in general,
	// failures will be logged and and a retry attempted after a
	// delay. TODO most if not all should result in a status update.

	url := sync.Spec.Source.URL
	response, err := http.Get(url)
	if err != nil {
		log.V(debug).Info("failed to fetch package URL", "error", err, "url", url)
		return ctrl.Result{RequeueAfter: transitoryErrorRetryDelay}, nil
	}

	if response.StatusCode != http.StatusOK {
		log.V(debug).Info("response was not HTTP 200; will retry",
			"url", url,
			"code", response.StatusCode)
		return ctrl.Result{RequeueAfter: transitoryErrorRetryDelay}, nil
	}

	log.V(debug).Info("got package file", "url", url)
	defer response.Body.Close()

	sourcedir := filepath.Join(tmpdir, "source")

	contentType := response.Header.Get("Content-Type")
	switch contentType {
	case "application/zip":
		err = unzip(response.Body, sourcedir, log)
	case "application/gzip", "application/x-gzip":
		err = untargzip(response.Body, sourcedir, log)
	default:
		err = fmt.Errorf("unsupported content type %q", contentType)
	}

	if err != nil {
		// This is likely to be a configuration problem, rather than
		// transitory; for that reason, log it but don't retry until
		// something changes.
		log.Error(err, "error expanding archive", "url", url)
		return ctrl.Result{}, nil
	}

	if len(sync.Spec.Source.Paths) == 0 {
		applyArgs = append(applyArgs, "-f", sourcedir)
	}
	for _, path := range sync.Spec.Source.Paths {
		// FIXME guard against parent paths
		applyArgs = append(applyArgs, "-f", filepath.Join(sourcedir, path))
	}

	log.V(debug).Info("kubectl command", "args", applyArgs)
	cmd := exec.CommandContext(ctx, "kubectl", applyArgs...)
	outBytes, err := cmd.CombinedOutput()
	out := string(outBytes)

	result := syncv1alpha1.ApplySuccess
	if err != nil {
		result = syncv1alpha1.ApplyFail
	}

	// kubectl can exit with non-zero because it couldn't connect, or
	// because it didn't apply things cleanly, and those are different
	// kinds of problem here. For now, just log the result. Later,
	// it'll go in the status.
	log.Info("kubectl apply result", "exit-code", cmd.ProcessState.ExitCode(), "output", out)

	sync.Status.LastApplyTime = &metav1.Time{Time: now}
	sync.Status.LastApplyResult = result
	sync.Status.LastApplySource = &sync.Spec.Source
	sync.Status.ObservedGeneration = sync.Generation

	if result == syncv1alpha1.ApplySuccess {
		// parse output to get resources.
		var resources []syncv1alpha1.ResourceStatus
		outLines := strings.Split(out, "\n")
		for _, line := range outLines {
			if line == "" {
				continue
			}
			parts := strings.Split(line, " ")
			// Ignore List, since that's just a wrapper. See the
			// comment about the JSONPath format, at the top.
			if parts[1] == "List" {
				continue
			}
			resources = append(resources, syncv1alpha1.ResourceStatus{
				TypeMeta: &metav1.TypeMeta{
					APIVersion: parts[0],
					Kind:       parts[1],
				},
				Name:      parts[2],
				Namespace: parts[3],
			})
		}
		sync.Status.Resources = resources

		updateResourcesStatus(ctx, r, r.mapper, &sync.Status.SyncStatus, now)
	}

	if err = r.Status().Update(ctx, &sync); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: sync.Spec.Interval.Duration}, nil
}

func (r *RemoteSyncReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.mapper = mgr.GetRESTMapper()
	return ctrl.NewControllerManagedBy(mgr).
		For(&syncv1alpha1.RemoteSync{}).
		Complete(r)
}
