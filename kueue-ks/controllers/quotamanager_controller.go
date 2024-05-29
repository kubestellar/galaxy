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

package controllers

import (
	"context"
	"fmt"

	clustermetrics "kubestellar/galaxy/clustermetrics/api/v1alpha1"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	v1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
)

const (
	CPU    = "cpu"
	Memory = "memory"
)

// ClusterMetricsReconciler reconciles ClusterMetrics object from each cluster and updates
// global quota managed by kueue in a cluster queue. As new clusters join or new nodes
// are added this controller increases quota accordingly. Quota decreasing is not so
// straighforward. Although technically the decrease can be done, kueue will not preempt
// any jobs even if it is required due to reduced quota. The only way the decrease can
// work is to stop accepting new jobs, drain, decrease quota and open the gate for new
// jobs.
type ClusterMetricsReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	WorkerClusters map[string]clustermetrics.ClusterMetrics
	ClusterQueue   string
}

//+kubebuilder:rbac:groups=galaxy.kubestellar.io.galaxy.kubestellar.io,resources=clustermetrics,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=galaxy.kubestellar.io.galaxy.kubestellar.io,resources=clustermetrics/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=galaxy.kubestellar.io.galaxy.kubestellar.io,resources=clustermetrics/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the ClusterMetrics object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.12.1/pkg/reconcile
func (r *ClusterMetricsReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	clusterMetrics := clustermetrics.ClusterMetrics{}

	if err := r.Client.Get(ctx, req.NamespacedName, &clusterMetrics); err != nil {
		fmt.Printf("unable to get clusterinfo: %s", err)
		return ctrl.Result{}, err
	}
	log.Info("%%%%%%%%%% ", "Cluster", clusterMetrics.Name)
	cachedClusterMetrics, found := r.WorkerClusters[clusterMetrics.Name]
	if !found {
		r.WorkerClusters[clusterMetrics.Name] = clusterMetrics
	} else {
		cachedClusterMetrics.Status.Nodes = clusterMetrics.Status.Nodes
	}

	available := map[string]*resource.Quantity{}
	for _, cm := range r.WorkerClusters {
		for _, node := range cm.Status.Nodes {
			if available[CPU] == nil {
				available[CPU] = resource.NewQuantity(0, resource.BinarySI)
			}
			available[CPU].Add(*node.AllocatableResources.Cpu())

			if available[Memory] == nil {
				available[Memory] = resource.NewQuantity(0, resource.BinarySI)
			}
			available[Memory].Add(*node.AllocatableResources.Memory())
		}
	}
	clusterQueue := v1beta1.ClusterQueue{}
	qq := types.NamespacedName{
		Name: r.ClusterQueue,
	}
	if err := r.Client.Get(ctx, qq, &clusterQueue); err != nil {
		fmt.Printf("unable to get clusterqueue: %s", err)
		return ctrl.Result{}, err
	}
	Default := 0
	update := false
	queueNominalCpuCount := clusterQueue.Spec.ResourceGroups[0].Flavors[Default].Resources[0].NominalQuota
	if clusterQueue.Spec.ResourceGroups[0].Flavors[Default].Resources[0].Name == CPU {
		if available[CPU] != nil {
			if available[CPU].Value() > queueNominalCpuCount.Value() {
				update = true
				delta := available[CPU].DeepCopy()
				delta.Sub(queueNominalCpuCount)
				queueNominalCpuCount.Add(delta)
				clusterQueue.Spec.ResourceGroups[0].Flavors[Default].Resources[0].NominalQuota = queueNominalCpuCount
			}
		}
	}
	if clusterQueue.Spec.ResourceGroups[0].Flavors[Default].Resources[1].Name == Memory {
		queueNominalMemoryQuota := clusterQueue.Spec.ResourceGroups[0].Flavors[Default].Resources[1].NominalQuota //.ScaledValue(resource.Giga)

		if available[Memory] != nil {
			if available[Memory].ScaledValue(resource.Kilo) > queueNominalMemoryQuota.ScaledValue(resource.Kilo) {
				update = true
				delta := available[Memory].DeepCopy()
				delta.Sub(queueNominalMemoryQuota)
				queueNominalMemoryQuota.Add(delta)
				clusterQueue.Spec.ResourceGroups[0].Flavors[Default].Resources[1].NominalQuota = queueNominalMemoryQuota
			}
		}
	}
	if update {
		log.Info("Updating ClusterQueue")
		if err := r.Client.Update(ctx, &clusterQueue); err != nil {
			fmt.Printf("unable to Update clusterqueue: %s", err)
			return ctrl.Result{}, nil
		}

	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterMetricsReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&clustermetrics.ClusterMetrics{}).
		Complete(r)
}
