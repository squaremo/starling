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
	"time"

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

const (
	timeout  = 20 * time.Second
	interval = time.Second
)

// The suite setup constructs a "cluster" which runs the syncing
// machinery. To test cross-cluster syncing, we need at least one
// other "cluster".
var _ = Describe("proxy syncing", func() {

	var (
		downstreamK8sClient client.Client
		downstreamEnv       *envtest.Environment
		cluster             *clusterv1.Cluster
		clusterSecret       *corev1.Secret
	)

	BeforeEach(func() {
		downstreamEnv, cluster, clusterSecret, downstreamK8sClient = makeDownstreamEnv()
	})

	AfterEach(func() {
		By("removing cluster records")
		Expect(k8sClient.Delete(context.Background(), cluster)).To(Succeed())
		Expect(k8sClient.Delete(context.Background(), clusterSecret)).To(Succeed())

		By("tearing down the test environment")
		err := downstreamEnv.Stop()
		Expect(err).ToNot(HaveOccurred())
	})

	It("makes a sync in the downstream", func() {
		// make a proxy object in the mgmt cluster
		proxySync := syncv1alpha1.ProxySync{
			Spec: syncv1alpha1.ProxySyncSpec{
				Template: syncv1alpha1.SyncTemplate{
					Spec: syncv1alpha1.SyncSpec{
						// It won't actually sync this; no sync controller in the downstream
						Source: syncv1alpha1.SyncSource{
							URL: "https://github.com/cuttlefacts/cuttlefacts-app.git",
						},
					},
				},
				ClusterRef: corev1.LocalObjectReference{Name: "downstream"},
			},
		}
		proxySync.Name = "test-proxy-sync"
		proxySync.Namespace = "default"
		Expect(k8sClient.Create(context.Background(), &proxySync)).To(Succeed())

		Eventually(func() bool {
			var sync syncv1alpha1.Sync
			err := downstreamK8sClient.Get(context.Background(), types.NamespacedName{
				Name:      "test-proxy-sync",
				Namespace: "default",
			}, &sync)
			if err != nil {
				return false
			}
			return sync.Spec.Source.URL == "https://github.com/cuttlefacts/cuttlefacts-app.git"
		}, timeout, interval).Should(BeTrue())
	})
})
