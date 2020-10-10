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
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	kstatus "sigs.k8s.io/kustomize/kstatus/status"
	kwait "sigs.k8s.io/kustomize/kstatus/wait"

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

// This puts an order on status values, which I can use to calculate a
// _least_ status from a list of resources.
var statusRanks = map[kstatus.Status]int{
	kstatus.UnknownStatus:      0,
	syncv1alpha1.MissingStatus: 1,
	kstatus.FailedStatus:       2,
	kstatus.TerminatingStatus:  3,
	kstatus.InProgressStatus:   4,
	kstatus.CurrentStatus:      5,
}

// returns true if a is (strictly) less ready than b.
func lessReadyThan(a, b kstatus.Status) bool {
	return statusRanks[a] < statusRanks[b]
}

// kubectl commands return a List if there's more than one result, but
// a single item if there's one result. This makes it infuriatingly
// fiddly to get a uniform representation out with JSONPath. The
// simplest thing seems to be to output both, and ignore `List`
// entries when parsing.
const itemJSONPath = `{.apiVersion} {.kind} {.metadata.name} {.metadata.namespace}{"\n"}`
const outputJSONPath = `jsonpath=` + itemJSONPath + `{range .items[*]}` + itemJSONPath + `{end}`

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

	// Check whether we need to actually run a sync. There are two conditions:
	//  - at least Interval has passed since the last sync
	//  - the source has changed
	now := time.Now().UTC()
	if ok, when := needsApply(&sync, now); !ok {
		// May still want to update the resource statuses; see if it's
		// time to do that
		if ok, whenStatus := needsStatus(&sync, now); ok {
			r.updateResourcesStatus(ctx, &sync, now)
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

	// Check the dependencies of the sync; if they aren't ready, don't
	// bother with the rest.
	if pending, err := r.checkDependenciesReady(ctx, &sync); err != nil {
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

	tmpdir, err := ioutil.TempDir("", "sync-")
	if err != nil {
		// failing to create a tmpdir qualifies as a proper error
		return ctrl.Result{}, err
	}
	defer os.RemoveAll(tmpdir)

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

	applyArgs := []string{"apply", "-o", outputJSONPath}

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

		r.updateResourcesStatus(ctx, &sync, now)
	}

	if err = r.Status().Update(ctx, &sync); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: sync.Spec.Interval.Duration}, nil
}

func (r *SyncReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// TODO I have no idea if this is what you are supposed to do
	r.mapper = mgr.GetRESTMapper()
	return ctrl.NewControllerManagedBy(mgr).
		For(&syncv1alpha1.Sync{}).
		Complete(r)
}

// ---

// checkDependenciesReady looks at the dependencies of a Sync and sees
// if they are ready (have reached the given status). If not, it
// returns the list of dependencies not met yet.
func (r *SyncReconciler) checkDependenciesReady(ctx context.Context, sync *syncv1alpha1.Sync) ([]syncv1alpha1.Dependency, error) {
	deps := sync.Spec.Dependencies
	var pending []syncv1alpha1.Dependency
	for _, dep := range deps {
		var s syncv1alpha1.Sync
		if err := r.Get(ctx, types.NamespacedName{
			Namespace: sync.GetNamespace(),
			Name:      dep.Sync.Name,
		}, &s); err != nil {
			return nil, err
		}
		depStatus := s.Status.ResourcesLeastStatus
		if depStatus == nil || lessReadyThan(*depStatus, dep.RequiredStatus) {
			// TODO: this is where I'd append the transitive
			// dependencies, so that cycles could be detected.
			pending = append(pending, dep)
		}
	}
	return pending, nil
}

func (r *SyncReconciler) updateResourcesStatus(ctx context.Context, sync *syncv1alpha1.Sync, now time.Time) {
	resources := sync.Status.Resources
	ids := make([]kwait.KubernetesObject, len(resources), len(resources))
	for i, resource := range resources {
		ids[i] = resource
	}

	resolver := kwait.NewResolver(r, r.mapper, 0)
	results := resolver.FetchAndResolveObjects(ctx, ids)
	// TODO this ignores errors (they just result in 'Unknown'
	// anyway)
	leastStatus := kstatus.CurrentStatus
	for i, result := range results {
		r.Log.V(1).Info("fetched status", "resource", result.ResourceIdentifier, "result", result.Result, "error", result.Error)
		s := new(kstatus.Status)
		*s = result.Result.Status
		// https://github.com/kubernetes-sigs/kustomize/issues/2587
		// kstatus does not distinguish between Current (up to date)
		// and missing, other than in the (for humans) `Message`
		// field. Hence this brittle test:
		if result.Result.Message == "Resource does not exist" {
			*s = syncv1alpha1.MissingStatus
		}

		if lessReadyThan(*s, leastStatus) {
			leastStatus = *s
		}
		resources[i].Status = s
	}
	r.Log.V(debug).Info("fetched resources status", "resources", resources, "least", leastStatus)
	sync.Status.LastResourceStatusTime = &metav1.Time{Time: now}
	sync.Status.ResourcesLeastStatus = &leastStatus
}
