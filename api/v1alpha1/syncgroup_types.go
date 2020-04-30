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

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// SyncGroupSpec defines a group of clusters to be synced with the
// sync specification provided
type SyncGroupSpec struct {
	// Source gives the source of the package to sync.
	// +required
	Source Source `json:"source"`
}

// Source contains the concrete source specification
type Source struct {
	// URL syncs clusters to the gzipped tarball or zip archive at the
	// given URL
	// +optional
	URL *string `json:"url,omitempty"`
	// GitRepository follows a git repository source by taking the URL
	// +optional
	GitRepository *corev1.LocalObjectReference `json:"gitRepository,omitempty"`
	// Paths gives the paths within the package to sync. An empty
	// value means sync the root directory.
	// +optional
	Paths []string `json:"paths,omitempty"`
}

// SyncGroupStatus defines the observed state of SyncGroup
type SyncGroupStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

// +kubebuilder:object:root=true

// SyncGroup is the Schema for the syncgroups API
type SyncGroup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SyncGroupSpec   `json:"spec,omitempty"`
	Status SyncGroupStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SyncGroupList contains a list of SyncGroup
type SyncGroupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SyncGroup `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SyncGroup{}, &SyncGroupList{})
}
