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

type SyncSource struct {
	// URL is a url for downloading a zipfile or tarball of the
	// package to sync
	URL string `json:"url"`
	// Revision identifies the commit from which the URL is
	// generated. This accompanies the URL so that it can be
	// explicitly recorded in the status.
	// +optional
	Revision string `json:"revision"`
	// Paths gives the paths to include in the sync. If using a
	// kustomization, there should be only one, ending in
	// 'kustomization.yaml'. If missing, the root directory will be
	// used.
	// +optional
	Paths []string `json:"paths,omitempty"`
}

// Equiv returns true if the supplied SyncSource is equivalent to this
// SyncSource. Equivalence is weaker than `Equal` -- it just means
// "are these interchangeable when used to sync".
func (src1 *SyncSource) Equiv(src2 *SyncSource) bool {
	if src2 == nil {
		return false
	}
	if src1.URL != src2.URL || src1.Revision != src2.Revision {
		return false
	}
	// Having the same paths in a different order counts as different;
	// a more sophisticated predicate would sort (copies), clean the
	// paths, and compare those.
	if len(src1.Paths) != len(src2.Paths) {
		return false
	}
	for i, p := range src1.Paths {
		if p != src2.Paths[i] {
			return false
		}
	}
	return true
}

// SyncSpec defines the desired state of Sync
type SyncSpec struct {
	// Source is the location from which to get configuration to sync.
	// +required
	Source SyncSource `json:"source"`
	// Interval is the target period for reapplying the config to the
	// cluster. Syncs may be processed slower than this, depending on
	// load; or may occur more often if the sync in question is
	// updated.
	// +required
	Interval metav1.Duration `json:"interval"`
	// Cluster is a reference to the cluster to apply definitions to
	// +optional
	Cluster *corev1.LocalObjectReference `json:"cluster,omitempty"`
}

// ApplyResult is a type for recording the outcome of an attempted
// sync.
type ApplyResult string

// Until I parse the output of kubectl apply, it's success or failure
const (
	ApplySuccess ApplyResult = "success"
	ApplyFail    ApplyResult = "fail"
)

// SyncStatus defines the observed state of Sync
type SyncStatus struct {
	// LastApplySource records the source that was set last time a
	// sync was attempted.
	// +optional
	LastApplySource *SyncSource `json:"lastApplySource,omitempty"`
	// LastAppliedTime records the last time a sync was attempted
	// +optional
	LastApplyTime *metav1.Time `json:"lastApplyTime,omitempty"`
	// LastApplyResult records the outcome of the last sync attempt
	// +optional
	LastApplyResult ApplyResult `json:"lastApplyResult,omitempty"`
	// ObservedGeneration (from metadata.generation) is the generation
	// last observed by the controller. This is used so the controller
	// can know whether it needs to react to a change, or simply
	// update the status.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Last result",type=string,JSONPath=`.status.lastApplyResult`
// +kubebuilder:printcolumn:name="Last applied",type=string,JSONPath=`.status.lastApplyTime`

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
