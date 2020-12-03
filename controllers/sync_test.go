/*
Copyright Michael Bridgen <mikeb@squaremobius.net> 2020

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
	"bytes"
	"compress/gzip"
	"context"
	"io"
	//	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	//	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"

	syncv1alpha1 "github.com/fluxcd/starling/api/v1alpha1"
	// +kubebuilder:scaffold:imports
)

var _ = Describe("syncing", func() {

	tarball := makeTarball("testdata/sync-repo")
	const repoPath = "/repo.tar.gz"

	var (
		repoSrv *httptest.Server
		repoURL string
	)

	BeforeEach(func() {
		repoSrv, repoURL = makeRepoSrv(repoPath, tarball)
	})

	AfterEach(func() {
		repoSrv.Close()
	})

	It("is serving the tarball at the path", func() {
		resp, err := http.Get(repoURL)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Header.Get("Content-Type")).To(Equal("application/gzip"))
	})

	Context("valid sync", func() {
		var (
			syncName types.NamespacedName
			sync     syncv1alpha1.Sync
		)

		ctx := context.Background()

		BeforeEach(func() {
			sync = syncv1alpha1.Sync{
				Spec: syncv1alpha1.SyncSpec{
					Source: syncv1alpha1.SyncSource{
						URL: repoURL,
					},
				},
			}
			//TODO make this and the namespace random, so tests are
			//isloated if there's a mistake in teardown.
			syncName = types.NamespacedName{
				Name:      "syncity",
				Namespace: "default",
			}
			sync.Name = syncName.Name
			sync.Namespace = syncName.Namespace
			Expect(k8sClient.Create(ctx, &sync)).To(Succeed())
		})

		It("reaches a ready state", func() {
			var s syncv1alpha1.Sync
			Eventually(func() bool {
				if err := k8sClient.Get(ctx, syncName, &s); err != nil {
					return false
				}
				return s.Status.LastApplyTime != nil
			}, timeout, interval).Should(BeTrue())
			Expect(s.Status.LastApplyResult).To(Equal(syncv1alpha1.ApplySuccess))
		})

		AfterEach(func() {
			Expect(k8sClient.Delete(ctx, &sync)).To(Succeed())
		})
	})
})

func makeTarball(path string) []byte {
	buf := &bytes.Buffer{}
	gzipped := gzip.NewWriter(buf)

	tw := tar.NewWriter(gzipped)
	filepath.Walk(path, func(file string, fi os.FileInfo, err error) error {
		if err != nil {
			panic(err)
		}
		if !fi.Mode().IsRegular() {
			return nil
		}
		hdr, err := tar.FileInfoHeader(fi, fi.Name())
		if err != nil {
			return err
		}
		// relativise the file's path
		hdr.Name = file[len(path)+1:]
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		f, err := os.Open(file)
		if err != nil {
			return err
		}
		if _, err := io.Copy(tw, f); err != nil {
			return err
		}
		f.Close()
		return nil
	})
	tw.Close()
	gzipped.Close()
	return buf.Bytes()
}

// makeRepoSrv constructs an HTTP server which will serve the tarball
// (given as bytes) at the known path. It returns the server, and the
// URL at which the tarball can be accessed.
func makeRepoSrv(path string, tarball []byte) (*httptest.Server, string) {
	repoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == path {
			w.Header().Set("Content-Type", "application/gzip")
			w.Write(tarball)
			return
		}
		w.WriteHeader(404)
		return
	}))
	repoURL := repoSrv.URL + path
	return repoSrv, repoURL
}
