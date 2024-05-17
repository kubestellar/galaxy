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

package scheduler

import (
	cmv1alpha1 "kubestellar/galaxy/clustermetrics/api/v1alpha1"

	corev1 "k8s.io/api/core/v1"
)

type MultiClusterScheduler interface {
	// Given a list of pod specs, a list of clusters with node metrics for each
	// filter, rank and select the cluster with top rank
	SelectCluster(podSpecList []*corev1.PodSpec, clusterMetricsList *cmv1alpha1.ClusterMetricsList) string
}
