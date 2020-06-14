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
	kstatus "sigs.k8s.io/kustomize/kstatus/status"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!  NOTE: json
// tags are required.  Any new fields you add must have json tags for
// the fields to be serialized.

const (
	// MissingStatus records the fact of an expected resource not
	// being present in the cluster. This is not at present in the
	// range for status reported by kstatus, but it is a case I want
	// to distinguish here.
	MissingStatus kstatus.Status = "Missing"
)

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

type Dependency struct {
	// RequiredStatus is the status needed to be reported by the
	// dependency before this sync can proceed.
	// +required
	RequiredStatus kstatus.Status `json:"requiredStatus"`
	// Sync is a pointer to another sync.
	// +required
	Sync *corev1.LocalObjectReference `json:"sync"`
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
	// Dependencies gives a list of the dependency relations this sync
	// has. These must be satisfied for this sync to be applied.
	Dependencies []Dependency `json:"dependencies,omitempty"`
}

// ApplyResult is a type for recording the outcome of an attempted
// sync.
type ApplyResult string

// Until I parse the output of kubectl apply, it's success or failure
const (
	ApplySuccess ApplyResult = "success"
	ApplyFail    ApplyResult = "fail"
)

// ResourceStatus gives the identity and summary status of a resource.
type ResourceStatus struct {
	// NB: this provides GroupVersionKind()
	*metav1.TypeMeta `json:",inline"`
	Namespace        string          `json:"namespace,omitempty"`
	Name             string          `json:"name"`
	Status           *kstatus.Status `json:"status,omitempty"`
}

func (r ResourceStatus) GetNamespace() string {
	return r.Namespace
}

func (r ResourceStatus) GetName() string {
	return r.Name
}

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

	// LastStatusTime records the last time the applied resources were
	// successfully scanned for their status.
	// +optional
	LastResourceStatusTime *metav1.Time `json:"lastResourceStatusTime,omitempty"`
	// Resources gives the identity and summary status of each
	// resource applied by this sync.
	// +optional
	Resources []ResourceStatus `json:"resources,omitempty"`
	// ResourcesLeastStatus gives an aggreate status for the resources
	// synced, by using the _least_ ready of the individual statuses.
	// +optional
	ResourcesLeastStatus *kstatus.Status `json:"resourcesLeastStatus,omitempty"`
	// PendingDependencies is a list of dependencies that are not yet
	// satisfied. Aside from being informative this lets the
	// controller see if there is a cycle.
	PendingDependencies []Dependency `json:"pendingDependencies,omitempty"`
}

// https://github.com/kubernetes-sigs/kustomize/blob/master/kstatus/wait/wait.go#L28

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Revision",type=string,JSONPath=`.status.lastApplySource.revision`
// +kubebuilder:printcolumn:name="Last result",type=string,JSONPath=`.status.lastApplyResult`
// +kubebuilder:printcolumn:name="Last applied",type=string,JSONPath=`.status.lastApplyTime`
// +kubebuilder:printcolumn:name="Resources status",type=string,JSONPath=`.status.resourcesLeastStatus`

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
