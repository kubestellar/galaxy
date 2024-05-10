/*
Copyright 2024 The KubeStellar Authors.

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

// ClusterMetricsSpec defines the desired state of ClusterMetrics
type ClusterMetricsSpec struct {
}

// ClusterMetricsStatus defines the observed state of ClusterMetrics
type ClusterMetricsStatus struct {
	Nodes []NodeInfo `json:"nodes,omitempty"`
}

// NodeInfo provide info at node level
type NodeInfo struct {
	Name                      string              `json:"name,omitempty"`
	AllocatableResources      corev1.ResourceList `json:"allocatableResources,omitempty"`
	AllocatedResourceRequests corev1.ResourceList `json:"allocatedResourceRequests,omitempty"`
	AllocatedResourceLimits   corev1.ResourceList `json:"allocatedResourceLimits,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// ClusterMetrics is the Schema for the clustermetrics API
// +kubebuilder:resource:scope=Cluster,shortName={cmet,cmets}
type ClusterMetrics struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterMetricsSpec   `json:"spec,omitempty"`
	Status ClusterMetricsStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// ClusterMetricsList contains a list of ClusterMetrics
type ClusterMetricsList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterMetrics `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterMetrics{}, &ClusterMetricsList{})
}
