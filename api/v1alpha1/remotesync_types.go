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

// RemoteSyncSpec defines the desired state of RemoteSync
type RemoteSyncSpec struct {
	SyncSpec `json:",inline"`
	// ClusterRef is a reference to the cluster to apply definitions to
	// +required
	ClusterRef corev1.LocalObjectReference `json:"clusterRef,omitempty"`
}

// RemoteSyncStatus defines the observed state of RemoteSync
type RemoteSyncStatus struct {
	SyncStatus `json:",inline"`
}

// +kubebuilder:object:root=true

// RemoteSync is the Schema for the remotesyncs API
type RemoteSync struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RemoteSyncSpec   `json:"spec,omitempty"`
	Status RemoteSyncStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Cluster",type=string,JSONPath=`.spec.clusterRef.name`
// +kubebuilder:printcolumn:name="Revision",type=string,JSONPath=`.status.lastApplySource.revision`
// +kubebuilder:printcolumn:name="Last result",type=string,JSONPath=`.status.lastApplyResult`
// +kubebuilder:printcolumn:name="Last applied",type=string,JSONPath=`.status.lastApplyTime`
// +kubebuilder:printcolumn:name="Resources status",type=string,JSONPath=`.status.resourcesLeastStatus`

// RemoteSyncList contains a list of RemoteSync
type RemoteSyncList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RemoteSync `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RemoteSync{}, &RemoteSyncList{})
}
