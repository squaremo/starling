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

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!  NOTE: json
// tags are required.  Any new fields you add must have json tags for
// the fields to be serialized.

// SyncSpec defines the desired state of Sync
type SyncSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// URL is a url for downloading a zipfile or tarball of the package to sync
	URL string `json:"url"`
	// Paths gives the paths to include in the sync. If using a
	// kustomization, there should be only one, ending in
	// 'kustomization.yaml'. If missing, the root directory will be
	// used.
	// +optional
	Paths []string `json:"paths"`

	// Interval is the target period for reapplying the config to the
	// cluster. Syncs may be processed slower than this, depending on
	// load; or may occur more often if the sync in question is
	// updated.
	Interval metav1.Duration `json:"interval"`
	// Cluster is a reference to the cluster to apply definitions to
	// +optional
	Cluster *corev1.LocalObjectReference `json:"cluster"`
}

// SyncStatus defines the observed state of Sync
type SyncStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Revision records the SHA1 of the commit that is synced to.
	Revision string `json:"revision"`
}

// +kubebuilder:object:root=true

// Sync is the Schema for the syncs API
type Sync struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SyncSpec   `json:"spec,omitempty"`
	Status SyncStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SyncList contains a list of Sync
type SyncList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Sync `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Sync{}, &SyncList{})
}
