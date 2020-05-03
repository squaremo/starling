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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clusterv1alpha3 "sigs.k8s.io/cluster-api/api/v1alpha3"
	kcfg "sigs.k8s.io/cluster-api/util/kubeconfig"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	syncv1alpha1 "github.com/fluxcd/starling/api/v1alpha1"
)

const retryDelay = 20 * time.Second
const debug = 1
const trace = 2

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

	// If the sync refers to a cluster, check that the cluster
	// exists. If not, I can't do anything here.
	if sync.Spec.Cluster != nil {
		var cluster clusterv1alpha3.Cluster
		if err := r.Get(ctx, types.NamespacedName{
			Name:      sync.Spec.Cluster.Name,
			Namespace: sync.GetNamespace(),
		}, &cluster); err != nil {
			// If this Sync refers to a missing cluster, it'll get
			// deleted at some point anyway.
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
	}

	// Attempt to fetch the URL of the package being synced. Many of
	// the possible failures here will be transitory, so in general,
	// failures will be logged and and a retry attempted after a
	// delay. TODO most if not all should result in a status update.

	response, err := http.Get(sync.Spec.URL)
	if err != nil {
		log.V(debug).Info("failed to fetch package URL", "error", err, "url", sync.Spec.URL)
		return ctrl.Result{RequeueAfter: retryDelay}, nil
	}

	if response.StatusCode != http.StatusOK {
		log.V(debug).Info("response was not HTTP 200; will retry", "code", response.StatusCode)
		return ctrl.Result{RequeueAfter: retryDelay}, nil
	}

	log.V(debug).Info("got package file", "url", sync.Spec.URL)
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
		log.Error(err, "error expanding archive", "url", sync.Spec.URL)
		return ctrl.Result{}, nil
	}

	applyArgs := []string{"apply"}

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
			return ctrl.Result{RequeueAfter: retryDelay}, nil
		}
		kubeconfigPath := filepath.Join(tmpdir, "kubeconfig")
		if err := ioutil.WriteFile(kubeconfigPath, kubeconfig, os.FileMode(0400)); err != nil {
			// If it can't write to the filesystem, that _is_ a problem
			return ctrl.Result{}, err
		}
		applyArgs = append(applyArgs, "--kubeconfig", kubeconfigPath)
	}

	if len(sync.Spec.Paths) == 0 {
		applyArgs = append(applyArgs, "-f", sourcedir)
	}
	for _, path := range sync.Spec.Paths {
		// FIXME guard against parent paths
		applyArgs = append(applyArgs, "-f", filepath.Join(sourcedir, path))
	}

	cmd := exec.CommandContext(ctx, "kubectl", applyArgs...)
	out, err := cmd.CombinedOutput()
	// kubectl can exit with non-zero because it couldn't connect, or
	// because it didn't apply things cleanly, and those are different
	// kinds of problem here. For now, just log the result. Later,
	// it'll go in the status.
	log.Info("kubectl apply result", "exit-code", cmd.ProcessState.ExitCode(), "output", string(out))

	return ctrl.Result{RequeueAfter: sync.Spec.Interval.Duration}, nil
}

func (r *SyncReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&syncv1alpha1.Sync{}).
		Complete(r)
}

// ---

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
