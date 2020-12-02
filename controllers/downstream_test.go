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
	"fmt"
	"path/filepath"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha3"
	"sigs.k8s.io/cluster-api/util/kubeconfig"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// This has funcs for creating a downstream cluster, useful for
// testing remote/proxy syncs.

// makeDownstreamEnv constructs an envtest Environment, and creates a
// Cluster object in the regular env pointing to it. It returns the
// downstream environment, the cluster object, the secret with
// connection creds, and a client for the downstream environment. The
// downstream environment has the CRDs already installed.
func makeDownstreamEnv() (*envtest.Environment, *clusterv1.Cluster, *corev1.Secret, client.Client) {
	By("bootstrapping downstream cluster environment")
	// usually there's a registration process whereby the
	// downstream cluster gets set up with the CRDs, controllers
	// and whatnot. To test proxy syncing on its own, I'll assume
	// that's all been done.
	downstreamEnv := &envtest.Environment{
		CRDDirectoryPaths: []string{filepath.Join("..", "config", "crd", "bases")},
	}

	downstreamCfg, err := downstreamEnv.Start()
	Expect(err).ToNot(HaveOccurred())
	Expect(downstreamCfg).ToNot(BeNil())

	// this is for the test to verify things are correct in the
	// downstream cluster; therefore we do want to be able to
	// understand the various CRDs.
	downstreamK8sClient, err := client.New(downstreamCfg, client.Options{Scheme: scheme.Scheme})
	Expect(downstreamK8sClient).ToNot(BeNil())
	Expect(err).ToNot(HaveOccurred())

	// TODO details of the cluster
	cluster := &clusterv1.Cluster{}
	cluster.Name = "downstream"
	cluster.Namespace = "default"
	Expect(k8sClient.Create(context.Background(), cluster)).To(Succeed())

	// For creating a secret:
	// https://github.com/kubernetes-sigs/cluster-api/blob/e5b02bdbce6c32b4dc062e9e1f14f8ccd16e8952/util/kubeconfig/kubeconfig.go#L109
	config := kubeconfigFromEndpoint("downstream", downstreamEnv.ControlPlane.APIURL().String())
	clusterSecretData, err := clientcmd.Write(*config)
	Expect(err).ToNot(HaveOccurred())

	clusterSecret := kubeconfig.GenerateSecret(cluster, clusterSecretData)
	Expect(k8sClient.Create(context.Background(), clusterSecret)).To(Succeed())

	return downstreamEnv, cluster, clusterSecret, downstreamK8sClient
}

func kubeconfigFromEndpoint(clusterName, endpoint string) *api.Config {
	username := fmt.Sprintf("%s-admin", clusterName)
	contextName := fmt.Sprintf("%s@%s", username, clusterName)
	return &api.Config{
		Clusters: map[string]*api.Cluster{
			clusterName: {
				Server: endpoint,
			},
		},
		Contexts: map[string]*api.Context{
			contextName: {
				Cluster:  clusterName,
				AuthInfo: username,
			},
		},
		AuthInfos: map[string]*api.AuthInfo{
			username: {},
		},
		CurrentContext: contextName,
	}
}
