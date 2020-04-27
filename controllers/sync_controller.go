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
	"compress/gzip"
	"context"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	sourcev1alpha1 "github.com/fluxcd/source-controller/api/v1alpha1"
	syncv1alpha1 "github.com/fluxcd/starling/api/v1alpha1"
)

const retryDelay = 20 * time.Second

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
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	var src sourcev1alpha1.GitRepository
	sourceName := types.NamespacedName{
		Namespace: sync.GetNamespace(),
		Name:      sync.Spec.Source.Name,
	}
	if err := r.Get(ctx, sourceName, &src); err != nil {
		log.Error(err, "source not found", "source", sourceName)
		return ctrl.Result{RequeueAfter: retryDelay}, err
	}

	artifact := src.GetArtifact()
	if artifact == nil {
		log.Info("artifact not present in Source")
		return ctrl.Result{}, nil
	}

	response, err := http.Get(artifact.URL)
	if err != nil {
		log.Error(err, "failed to fetch artifact URL", "url", artifact.URL)
		return ctrl.Result{RequeueAfter: retryDelay}, err
	}

	if response.StatusCode != http.StatusOK {
		log.Info("response was not HTTP 200", "code", response.StatusCode)
		return ctrl.Result{RequeueAfter: retryDelay}, nil // FIXME this is going to spam isn't it
	}

	log.Info("got artifact", "url", artifact.URL)
	defer response.Body.Close()

	tmpdir, err := ioutil.TempDir("", "sync-")
	if err != nil {
		log.Error(err, "failed to create temp dir for expanded source")
		return ctrl.Result{RequeueAfter: retryDelay}, err
	}
	defer os.RemoveAll(tmpdir)

	unzip, err := gzip.NewReader(response.Body)
	if err != nil {
		log.Error(err, "not a gzip")
		return ctrl.Result{RequeueAfter: retryDelay}, err
	}

	tr := tar.NewReader(unzip)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break // End of archive
		}
		if err != nil {
			log.Error(err, "error while unpacking tarball")
			return ctrl.Result{RequeueAfter: retryDelay}, err
		}

		// TODO symlinks, probably

		info := hdr.FileInfo()
		path := filepath.Join(tmpdir, hdr.Name)

		if info.IsDir() {
			// we don't need to create these since they will correspond to tmpdir
			if hdr.Name == "/" || hdr.Name == "./" {
				continue
			}
			if err = os.Mkdir(path, info.Mode()); err != nil {
				log.Error(err, "failed to create directory while unpacking tarball", "path", path, "name", hdr.Name)
				return ctrl.Result{RequeueAfter: retryDelay}, err
			}
			continue
		}

		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode())
		if err != nil {
			log.Error(err, "failed to create file while unpacking tarball", "path", path)
			return ctrl.Result{RequeueAfter: retryDelay}, err
		}
		if _, err = io.Copy(f, tr); err != nil {
			log.Error(err, "failed to write file contents while unpacking tarball", "path", path)
			return ctrl.Result{RequeueAfter: retryDelay}, err
		}
	}

	log.Info("unpacked tarball", "tmpdir", tmpdir)

	return ctrl.Result{RequeueAfter: sync.Spec.Interval.Duration}, nil
}

func (r *SyncReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&syncv1alpha1.Sync{}).
		Complete(r)
}
