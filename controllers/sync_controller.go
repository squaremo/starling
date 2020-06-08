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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clusterv1alpha3 "sigs.k8s.io/cluster-api/api/v1alpha3"
	kcfg "sigs.k8s.io/cluster-api/util/kubeconfig"
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

const debug = 1
const trace = 2

const outputJSONPath = `jsonpath={range .items[*]}{.apiVersion} {.kind} {.metadata.name} {.metadata.namespace}{"\n"}{end}`

// SyncReconciler reconciles a Sync object
type SyncReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
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
		return ctrl.Result{RequeueAfter: when}, nil
		// otherwise let it run on to do the sync
	}

	// If the sync refers to a cluster, check that the cluster
	// exists. If not, I can't do anything here.
	if sync.Spec.Cluster != nil {
		var cluster clusterv1alpha3.Cluster
		clusterName := types.NamespacedName{
			Name:      sync.Spec.Cluster.Name,
			Namespace: sync.GetNamespace(),
		}
		if err := r.Get(ctx, clusterName, &cluster); err != nil {
			// If this Sync refers to a missing cluster, it'll get
			// deleted at some point anyway.
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}

		// Only treat it as sync'able if it's provisioned and the
		// control plane is initialised and the infrastructure is
		// ready; this is as close an indication as you get for a
		// cluster that it is ready to be used, as far as I can tell.
		if !(cluster.Status.GetTypedPhase() == clusterv1alpha3.ClusterPhaseProvisioned &&
			cluster.Status.ControlPlaneInitialized &&
			cluster.Status.InfrastructureReady) {
			// This isn't an error, but we do want to try again at
			// some point. Watching clusters would be one way to do
			// this, but just looking again is good enough for now.
			log.V(debug).Info("cluster not ready", "cluster", clusterName)
			return ctrl.Result{RequeueAfter: clusterUnreadyRetryDelay}, nil
		}
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

	if sync.Spec.Cluster != nil {
		clusterName := types.NamespacedName{
			Name:      sync.Spec.Cluster.Name,
			Namespace: sync.GetNamespace(),
		}
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
		sync.Status.LastResourceStatusTime = &metav1.Time{Time: now}
	}

	if err = r.Status().Update(ctx, &sync); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: sync.Spec.Interval.Duration}, nil
}

func (r *SyncReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&syncv1alpha1.Sync{}).
		Complete(r)
}

// ---

// needsApply calculates whether a sync needs to run right now, and if
// not, how long until it does.
func needsApply(sync *syncv1alpha1.Sync, now time.Time) (bool, time.Duration) {
	if sync.Status.LastApplySource == nil ||
		sync.Status.LastApplyTime == nil ||
		!(&sync.Spec.Source).Equiv(sync.Status.LastApplySource) {
		return true, 0
	}
	when := sync.Spec.Interval.Duration - now.Sub(sync.Status.LastApplyTime.Time)
	if when < time.Second { // close enough to not bother requeueing
		return true, 0
	}
	return false, when
}

// untargzip unpacks a gzipped-tarball. It uses a logger to report
// unexpected problems; mostly it will just return the error, on the
// basis that next time might yield a different result.
func untargzip(body io.Reader, tmpdir string, log logr.Logger) error {
	unzip, err := gzip.NewReader(body)
	if err != nil {
		return err
	}

	tr := tar.NewReader(unzip)
	numberOfFiles := 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break // End of archive
		}
		if err != nil {
			return err
		}

		// TODO symlinks, probably

		info := hdr.FileInfo()
		path := filepath.Join(tmpdir, hdr.Name)

		if info.IsDir() {
			// we don't need to create these since they will correspond to tmpdir
			if hdr.Name == "/" || hdr.Name == "./" {
				continue
			}
			if err = os.MkdirAll(path, info.Mode()&os.ModePerm); err != nil {
				log.Error(err, "failed to create directory while unpacking tarball", "path", path, "name", hdr.Name)
				return err
			}
			continue
		}

		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode())
		if err != nil {
			log.Error(err, "failed to create file while unpacking tarball", "path", path)
			return err
		}
		if _, err = io.Copy(f, tr); err != nil {
			log.Error(err, "failed to write file contents while unpacking tarball", "path", path)
			return err
		}
		_ = f.Close()
		numberOfFiles++
	}

	log.V(debug).Info("unpacked tarball", "tmpdir", tmpdir, "file-count", numberOfFiles)
	return nil
}

// unzip unpacks a ZIP archive. It follows the same logging rationale
// as untargzip.
func unzip(body io.Reader, tmpdir string, log logr.Logger) error {
	// The zip reader needs random access (that is
	// io.ReaderAt). Rather than try to do some tricky on-demand
	// buffering, I'm going to just read the whole lot into a
	// bytes.Buffer.
	buf := new(bytes.Buffer)
	size, err := io.Copy(buf, body)
	if err != nil {
		return err
	}

	zipReader, err := zip.NewReader(bytes.NewReader(buf.Bytes()), size)
	if err != nil {
		return err
	}
	numberOfFiles := 0
	for _, file := range zipReader.File {
		name := file.FileHeader.Name
		// FIXME check for valid paths
		path := filepath.Join(tmpdir, name)

		if strings.HasSuffix(name, "/") {
			if err := os.MkdirAll(path, os.FileMode(0700)); err != nil {
				log.Error(err, "failed to create directory from zip", "path", path)
				return err
			}
			continue
		}

		content, err := file.Open()
		if err != nil {
			log.Error(err, "failed to open file in zip", "path", path)
			return err
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, os.FileMode(0600))
		if err != nil {
			log.Error(err, "failed to create file while unpacking zip", "path", path)
			return err
		}
		if _, err = io.Copy(f, content); err != nil {
			log.Error(err, "failed to write file contents while unpacking zip", "path", path)
			return err
		}
		_ = f.Close()
		content.Close()
		numberOfFiles++
	}

	log.V(debug).Info("unpacked zip", "tmpdir", tmpdir, "file-count", numberOfFiles)
	return nil
}
