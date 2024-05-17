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

package controller

import (
	"context"
	"time"

	"golang.org/x/time/rate"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"

	galaxyv1alpha1 "kubestellar/galaxy/clustermetrics/api/v1alpha1"
)

// ClusterMetricsReconciler reconciles a ClusterMetrics object
type ClusterMetricsReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	MetricsName string
}

//+kubebuilder:rbac:groups=galaxy.kubestellar.io,resources=clustermetrics,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=galaxy.kubestellar.io,resources=clustermetrics/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=galaxy.kubestellar.io,resources=clustermetrics/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the ClusterMetrics object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.17.3/pkg/reconcile
func (r *ClusterMetricsReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("reconciling cluster metrics")

	// Fetch the clusterMetrics instance
	// note that there is only one instance per cluster with fixed name. If not found, nothing is done
	obj := &galaxyv1alpha1.ClusterMetrics{}
	err := r.Client.Get(ctx, types.NamespacedName{Namespace: "", Name: r.MetricsName}, obj)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("no metrics CR found. Expected:", "metricsName", r.MetricsName)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	cMetrics := obj.DeepCopy()

	// get nodes
	nodeList := &corev1.NodeList{}
	err = r.Client.List(ctx, nodeList, &client.ListOptions{})
	if err != nil {
		return ctrl.Result{}, err
	}

	cMetrics.Status.Nodes = []galaxyv1alpha1.NodeInfo{}
	for _, node := range nodeList.Items {
		nodeInfo := galaxyv1alpha1.NodeInfo{}
		nodeInfo.Name = node.Name

		// Get Allocatable resources for the node
		nodeInfo.AllocatableResources = corev1.ResourceList{}
		for resource, quantity := range node.Status.Allocatable {
			nodeInfo.AllocatableResources[resource] = quantity
		}

		// List all pods running on the node
		podList := &corev1.PodList{}
		fieldSelector := &client.MatchingFields{
			"spec.nodeName": node.Name,
			"status.phase":  "Running",
		}
		err = r.Client.List(ctx, podList, fieldSelector)
		if err != nil {
			return ctrl.Result{}, err
		}

		// Initialize total resources maps for requests and limits
		totalResourceRequests := make(map[corev1.ResourceName]resource.Quantity)
		totalResourceLimits := make(map[corev1.ResourceName]resource.Quantity)

		for _, pod := range podList.Items {
			for _, container := range pod.Spec.Containers {
				addResources(&totalResourceRequests, container.Resources.Requests)
				addResources(&totalResourceLimits, container.Resources.Limits)
			}
		}

		// compute total for allocated resource requests and limits
		nodeInfo.AllocatedResourceRequests = corev1.ResourceList{}
		for resourceName, totalQuantity := range totalResourceRequests {
			nodeInfo.AllocatedResourceRequests[resourceName] = totalQuantity
		}

		nodeInfo.AllocatedResourceLimits = corev1.ResourceList{}
		for resourceName, totalQuantity := range totalResourceLimits {
			nodeInfo.AllocatedResourceLimits[resourceName] = totalQuantity
		}
		cMetrics.Status.Nodes = append(cMetrics.Status.Nodes, nodeInfo)
	}

	err = r.Client.Status().Update(ctx, cMetrics, &client.SubResourceUpdateOptions{})

	return ctrl.Result{}, err
}

func addResources(totalResources *map[corev1.ResourceName]resource.Quantity, resourceList corev1.ResourceList) {
	for resourceName, quantity := range resourceList {
		if total, exists := (*totalResources)[resourceName]; exists {
			total.Add(quantity)
			(*totalResources)[resourceName] = total
		} else {
			(*totalResources)[resourceName] = quantity.DeepCopy()
		}
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterMetricsReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Setup a rate limiter with a low frequency of updates (e.g., one every 10 s)
	qps := rate.Limit(1.0 / 10.0) // Queries per second
	burst := 1                    // Maximum burst for concurrency
	bucketRateLimiter := workqueue.NewMaxOfRateLimiter(
		workqueue.NewItemExponentialFailureRateLimiter(5*time.Millisecond, 1000*time.Second),
		&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(qps, burst)},
	)
	return ctrl.NewControllerManagedBy(mgr).
		For(&galaxyv1alpha1.ClusterMetrics{}).
		Watches(&corev1.Pod{}, &handler.EnqueueRequestForObject{}).
		Watches(&corev1.Node{}, &handler.EnqueueRequestForObject{}).
		WithOptions(controller.Options{RateLimiter: bucketRateLimiter}).
		Complete(r)
}
