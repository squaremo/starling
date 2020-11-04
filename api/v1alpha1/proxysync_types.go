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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ProxySyncSpec defines the desired state of ProxySync
type ProxySyncSpec struct {
	// Template gives the sync to run in the remote cluster
	// +required
	Template SyncTemplate `json:"template"`
	// ClusterRef is a reference to the cluster in which to run the
	// sync defined by the template.
	// +required
	ClusterRef corev1.LocalObjectReference `json:"clusterRef"`
}

type SyncTemplate struct {
	// Spec gives the specification of the sync to run in the remote cluster
	// +required
	Spec SyncSpec `json:"spec"`
}

// ProxySyncStatus defines the observed state of ProxySync
type ProxySyncStatus struct {
	SyncStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Revision",type=string,JSONPath=`.status.lastApplySource.revision`
// +kubebuilder:printcolumn:name="Last result",type=string,JSONPath=`.status.lastApplyResult`
// +kubebuilder:printcolumn:name="Last applied",type=string,JSONPath=`.status.lastApplyTime`
// +kubebuilder:printcolumn:name="Resources status",type=string,JSONPath=`.status.resourcesLeastStatus`

// ProxySync is the Schema for the proxysyncs API
type ProxySync struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ProxySyncSpec   `json:"spec,omitempty"`
	Status ProxySyncStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ProxySyncList contains a list of ProxySync
type ProxySyncList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ProxySync `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ProxySync{}, &ProxySyncList{})
}
