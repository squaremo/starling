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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

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

	response, err := http.Get(sync.Spec.URL)
	if err != nil {
		log.Error(err, "failed to fetch package URL", "url", sync.Spec.URL)
		return ctrl.Result{RequeueAfter: retryDelay}, err
	}

	if response.StatusCode != http.StatusOK {
		log.Info("response was not HTTP 200", "code", response.StatusCode)
		return ctrl.Result{RequeueAfter: retryDelay}, nil // FIXME this is going to spam isn't it
	}

	log.Info("got package file", "url", sync.Spec.URL)
	defer response.Body.Close()

	tmpdir, err := ioutil.TempDir("", "sync-")
	if err != nil {
		log.Error(err, "failed to create temp dir for expanded source")
		return ctrl.Result{RequeueAfter: retryDelay}, err
	}
	defer os.RemoveAll(tmpdir)

	contentType := response.Header.Get("Content-Type")
	switch contentType {
	case "application/zip":
		err = unzip(response.Body, tmpdir, log)
	case "application/gzip", "application/x-gzip":
		err = untargzip(response.Body, tmpdir, log)
	default:
		err = fmt.Errorf("unsupported content type %q", contentType)
		log.Error(err, "response contained unsupported archive format")
	}

	if err != nil {
		return ctrl.Result{RequeueAfter: retryDelay}, err
	}

	applyArgs := []string{"apply"}
	if len(sync.Spec.Paths) == 0 {
		applyArgs = append(applyArgs, "-f", tmpdir)
	}
	for _, path := range sync.Spec.Paths {
		// FIXME guard against parent paths
		applyArgs = append(applyArgs, "-f", filepath.Join(tmpdir, path))
	}

	cmd := exec.CommandContext(ctx, "kubectl", applyArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Error(err, "error running kubectl", "output", string(out))
		return ctrl.Result{RequeueAfter: retryDelay}, err
	}
	log.Info("kubectl apply output", "output", string(out))

	return ctrl.Result{RequeueAfter: sync.Spec.Interval.Duration}, nil
}

func (r *SyncReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&syncv1alpha1.Sync{}).
		Complete(r)
}

// ---

func untargzip(body io.Reader, tmpdir string, log logr.Logger) error {
	unzip, err := gzip.NewReader(body)
	if err != nil {
		log.Error(err, "not a gzip")
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
			log.Error(err, "error while unpacking tarball")
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
			if err = os.Mkdir(path, info.Mode()&os.ModePerm); err != nil {
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

	log.Info("unpacked tarball", "tmpdir", tmpdir, "file-count", numberOfFiles)
	return nil
}

func unzip(body io.Reader, tmpdir string, log logr.Logger) error {
	// The zip reader needs random access (that is
	// io.ReaderAt). Rather than try to do some tricky on-demand
	// buffering, I'm going to just read the whole lot into a
	// bytes.Buffer.
	buf := new(bytes.Buffer)
	size, err := io.Copy(buf, body)
	if err != nil {
		log.Error(err, "could not read from response body")
		return err
	}

	zipReader, err := zip.NewReader(bytes.NewReader(buf.Bytes()), size)
	if err != nil {
		log.Error(err, "could not read response as ZIP file")
		return err
	}
	numberOfFiles := 0
	for _, file := range zipReader.File {
		name := file.FileHeader.Name
		// FIXME check for valid paths
		path := filepath.Join(tmpdir, name)

		if strings.HasSuffix(name, "/") {
			if err := os.Mkdir(path, os.FileMode(0700)); err != nil {
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

	log.Info("unpacked zip", "tmpdir", tmpdir, "file-count", numberOfFiles)
	return nil
}
