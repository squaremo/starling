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
	"k8s.io/apimachinery/pkg/labels"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// SyncGroupSpec defines a group of clusters to be synced with the
// sync specification provided
type SyncGroupSpec struct {
	// Selector gives the set of clusters to which to sync the given
	// source, as a label selector. If missing, a Sync will be created
	// for the local cluster. If _empty_, all clusters are selected.
	// +optional
	Selector *metav1.LabelSelector `json:"selector,omitempty"`
	// Source gives the source of the package to sync.
	// +required
	Source Source `json:"source"`
	// Interval is the perion on which syncs should be run.
	// +required
	Interval metav1.Duration `json:"interval"`
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

type SyncSummary struct {
	// Total gives the total number of Syncs owned by this
	// SyncGroup
	Total int `json:"syncTotal"`
	// Updated gives the number of Syncs that have been updated
	// since last syncing
	Updated int `json:"updated"`
	// Fail gives the number of Syncs that have completed and failed
	Fail int `json:"fail"`
	// Success gives the number of Syncs that have complete and
	// succeeded
	Success int `json:"success"`
}

// Register a sync depending on its spec and status
func (sum *SyncSummary) Count(sync *Sync) {
	if sync.Status.LastApplySource == nil ||
		!sync.Status.LastApplySource.Equiv(&sync.Spec.Source) {
		sum.Updated++
		return
	}
	switch sync.Status.LastApplyResult {
	case ApplySuccess:
		sum.Success++
	case ApplyFail:
		sum.Fail++
	default: // if neither success nor fail, but it has a
		// LastApplySource .. unknown, assume it'll be resolved at
		// some point
		sum.Updated++
	}
}

// Calculate the total from the other fields, since its value depends
// on those.
func (sum *SyncSummary) CalcTotal() {
	sum.Total = sum.Updated + sum.Fail + sum.Success
}

// SyncGroupStatus defines the observed state of SyncGroup
type SyncGroupStatus struct {
	// Summary gives the counts of Syncs in
	// various states
	// +optional
	Summary *SyncSummary `json:"summary,omitempty"`
	// ObservedSource gives the configuraton source, as last seen by
	// the controller. NB this is a SyncSource, since it encodes the
	// actual revision etc. that will be rolled out to Sync objects.
	// +optional
	ObservedSource *SyncSource `json:"observedSource,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Revision",type=string,JSONPath=`.status.observedSource.revision`
// +kubebuilder:printcolumn:name="Updated",type=string,JSONPath=`.status.summary.updated`
// +kubebuilder:printcolumn:name="Succeeded",type=string,JSONPath=`.status.summary.success`
// +kubebuilder:printcolumn:name="Failed",type=string,JSONPath=`.status.summary.fail`

// SyncGroup is the Schema for the syncgroups API
type SyncGroup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SyncGroupSpec   `json:"spec,omitempty"`
	Status SyncGroupStatus `json:"status,omitempty"`
}

func (sg *SyncGroup) Selector() (labels.Selector, error) {
	return metav1.LabelSelectorAsSelector(sg.Spec.Selector)
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
