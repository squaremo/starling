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
	"context"
	"net/http/httptest"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha3"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	syncv1alpha1 "github.com/fluxcd/starling/api/v1alpha1"
	// +kubebuilder:scaffold:imports
)

var _ = Describe("remote syncing", func() {

	var (
		downstreamK8sClient client.Client
		downstreamEnv       *envtest.Environment
		cluster             *clusterv1.Cluster
		clusterSecret       *corev1.Secret
	)

	tarball := makeTarball("testdata/sync-repo")
	const repoPath = "/syncthis.tgz"

	var (
		repoSrv *httptest.Server
		repoURL string
	)

	BeforeEach(func() {
		downstreamEnv, cluster, clusterSecret, downstreamK8sClient = makeDownstreamEnv()
		repoSrv, repoURL = makeRepoSrv(repoPath, tarball)
	})

	AfterEach(func() {
		By("stopping repo server")
		repoSrv.Close()

		By("removing cluster records")
		Expect(k8sClient.Delete(context.Background(), cluster)).To(Succeed())
		Expect(k8sClient.Delete(context.Background(), clusterSecret)).To(Succeed())

		By("tearing down the test environment")
		err := downstreamEnv.Stop()
		Expect(err).ToNot(HaveOccurred())
	})

	It("syncs the repo contents to the downstream", func() {
		// make a remote object in the mgmt cluster
		remoteSync := syncv1alpha1.RemoteSync{
			Spec: syncv1alpha1.RemoteSyncSpec{
				SyncSpec: syncv1alpha1.SyncSpec{
					Source: syncv1alpha1.SyncSource{
						URL: repoURL,
					},
				},
				ClusterRef: corev1.LocalObjectReference{Name: cluster.Name},
			},
		}
		remoteSync.Name = "test-remote-sync"
		remoteSync.Namespace = "default"
		Expect(k8sClient.Create(context.Background(), &remoteSync)).To(Succeed())

		Eventually(func() bool {
			var cfm corev1.ConfigMap
			err := downstreamK8sClient.Get(context.Background(), types.NamespacedName{
				Name:      "config", // known to be in the fixture
				Namespace: "default",
			}, &cfm)
			if err != nil {
				return false
			}
			return true
		}, timeout, interval).Should(BeTrue())
	})
})
