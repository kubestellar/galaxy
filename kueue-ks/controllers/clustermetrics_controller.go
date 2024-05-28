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
	//"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	v1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
)

// ClusterMetricsReconciler reconciles a ClusterMetrics object
type ClusterMetricsReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	WorkerClusters map[string]clustermetrics.ClusterMetrics
	ClusterQueue   string
}
//+kubebuilder:rbac:groups=galaxy.kubestellar.io.galaxy.kubestellar.io,resources=clustermetrics,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=galaxy.kubestellar.io.galaxy.kubestellar.io,resources=clustermetrics/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=galaxy.kubestellar.io.galaxy.kubestellar.io,resources=clustermetrics/finalizers,verbs=update

/*
func NewClusterMetricsReconciler(c client.Client, s *runtime.Scheme, q string, cfg *rest.Config) *ClusterMetricsReconciler {
	return &ClusterMetricsReconciler{
		Client:        c,
		Scheme:        s,
		WorkerClusters: make(map[string]clustermetrics.ClusterMetrics),
		ClusterQueue: q,
	}
}
*/
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
			//log.Info("%%%%%%%%%% ", "Cluster", cm.Name)
			if available["cpu"] == nil {
				available["cpu"] = resource.NewQuantity(0, resource.BinarySI)
			}
			available["cpu"].Add(*node.AllocatableResources.Cpu())

			if available["memory"] == nil {
				available["memory"] = resource.NewQuantity(0, resource.BinarySI)
			}
			available["memory"].Add(*node.AllocatableResources.Memory())
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
	//log.Info("Clusterqueue :::::::", "Resources", clusterQueue.Spec.ResourceGroups[0].Flavors[Default])
	queueNominalCpuCount := clusterQueue.Spec.ResourceGroups[0].Flavors[Default].Resources[0].NominalQuota
	if clusterQueue.Spec.ResourceGroups[0].Flavors[Default].Resources[0].Name == "cpu" {
		//	log.Info("Clusterqueue nominal  ---- CPU")
		if available["cpu"] != nil {
			//	log.Info("Clusterqueue nominal cpus ----",
			//		"", queueNominalCpuCount,
			//		"", queueNominalCpuCount.Format)
			if available["cpu"].Value() > queueNominalCpuCount.Value() {
				update = true
				delta := available["cpu"].DeepCopy()
				delta.Sub(queueNominalCpuCount)
				queueNominalCpuCount.Add(delta)
				//		log.Info("ClusterQueue New CPU Quota ----", "", queueNominalCpuCount.Value())
				clusterQueue.Spec.ResourceGroups[0].Flavors[Default].Resources[0].NominalQuota = queueNominalCpuCount
			}
		}
	}
	if clusterQueue.Spec.ResourceGroups[0].Flavors[Default].Resources[1].Name == "memory" {
		//	log.Info("Clusterqueue nominal  ---- MEMORY")
		queueNominalMemoryQuota := clusterQueue.Spec.ResourceGroups[0].Flavors[Default].Resources[1].NominalQuota //.ScaledValue(resource.Giga)

		if available["memory"] != nil {
			//		log.Info("Clusterqueue nominal memory ----",
			//			"", queueNominalMemoryQuota,
			//			"", queueNominalMemoryQuota.Format)
			if available["memory"].ScaledValue(resource.Kilo) > queueNominalMemoryQuota.ScaledValue(resource.Kilo) {
				update = true
				delta := available["memory"].DeepCopy()
				delta.Sub(queueNominalMemoryQuota)
				queueNominalMemoryQuota.Add(delta)
				//			log.Info("ClusterQueue New Memory Quota ----", "", queueNominalMemoryQuota)
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
