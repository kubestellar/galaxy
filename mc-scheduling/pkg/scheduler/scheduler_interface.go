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
