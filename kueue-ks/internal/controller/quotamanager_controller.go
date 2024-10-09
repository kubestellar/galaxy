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
	"encoding/json"
	"fmt"

	clustermetrics "kubestellar/galaxy/clustermetrics/api/v1alpha1"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
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
	Scheme                    *runtime.Scheme
	WorkerClusters            map[string]clustermetrics.ClusterMetrics
	ClusterQueue              string
	DefaultResourceFlavorName string
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
	log.Info("Reconciler", "Cluster", clusterMetrics.Name)

	return ctrl.Result{}, nil
}
func (r *ClusterMetricsReconciler) ReconcileClusterQueueFlavorQuota(ctx context.Context, clusterMetrics clustermetrics.ClusterMetrics) error {
	log := log.FromContext(ctx)

	cachedClusterMetrics, found := r.WorkerClusters[clusterMetrics.Name]
	if !found {
		r.WorkerClusters[clusterMetrics.Name] = clusterMetrics
	} else {
		cachedClusterMetrics.Status.Nodes = clusterMetrics.Status.Nodes
	}

	clusterQueue := v1beta1.ClusterQueue{}
	qq := types.NamespacedName{
		Name: r.ClusterQueue,
	}
	// Get Kueue's clusterqueue
	if err := r.Client.Get(ctx, qq, &clusterQueue); err != nil {
		fmt.Printf("unable to get clusterqueue: %s", err)
		return err
	}
	update := false
	// clusterqueue may have multiple resource flavors and code below deals
	// with each separately
	flavorMap := map[string]map[string]*resource.Quantity{}

	// iterate over cluster queue's flavors
	for _, flavor := range clusterQueue.Spec.ResourceGroups[0].Flavors {
		log.Info("clusterqueue", "resource flavor", flavor.Name)
		// check if the flavor exists in local cache if not allocate memory for it
		flavorResourceMap, found := flavorMap[string(flavor.Name)]
		if !found {
			flavorMap[string(flavor.Name)] = map[string]*resource.Quantity{}
			flavorMap[string(flavor.Name)] = make(map[string]*resource.Quantity, 0)
			flavorResourceMap = flavorMap[string(flavor.Name)]
		}

		for _, clusterMetrics := range r.WorkerClusters {
			for _, node := range clusterMetrics.Status.Nodes {
				log.Info("--------------------", "cluster", clusterMetrics.Name, "Node", node.Name)
				// checks if resource flavor matches node label(s)
				matches, _ := r.nodeMatchesFlavor(ctx, node, flavor.Name)
				if matches {
					log.Info("", "Node", node.Name, "Matches Flavor", flavor.Name)
					r.addResourceQty(ctx, node, flavorResourceMap)
					// tag node assigned to a specific flavor. Untagged nodes will
					// be handled separately and their quota will be used for
					// default flavor
					node.Labels["assigned-to-flavor"] = "true"
				}
			}
		}

		if len(flavorResourceMap) > 0 {
			for inx, resourceQuota := range flavor.Resources {
				resourceQuantity, found := flavorResourceMap[resourceQuota.Name.String()]
				if !found {
					continue
				}

				log.Info("", "flavor", flavor.Name, "resource", resourceQuota.Name, "resourceQuantity.Value()", resourceQuantity.Value(), "resourceQuota.NominalQuota", resourceQuota.NominalQuota.Value())
				// check if resource quota has changed
				if resourceQuantity.Value() > resourceQuota.NominalQuota.Value() {
					delta := resourceQuantity
					delta.Sub(resourceQuota.NominalQuota)
					flavor.Resources[inx].NominalQuota.Add(*delta)
					update = true
					log.Info("", "resource", resourceQuota.Name, "new clusterqueue flavor nominal quota", flavor.Resources[inx].NominalQuota)
				}
			}
		}

	}

	for _, flavor := range clusterQueue.Spec.ResourceGroups[0].Flavors {
		if string(flavor.Name) != r.DefaultResourceFlavorName {
			continue
		}
		log.Info("-----", "Total Clusters", len(r.WorkerClusters))
		// compute new quotas from all nodes in all clusters
		for _, clusterMetrics := range r.WorkerClusters {
			log.Info("-----", "cluster", clusterMetrics.Name, "Node Count", len(clusterMetrics.Status.Nodes))
			for _, node := range clusterMetrics.Status.Nodes {
				_, assigned := node.Labels["assigned-to-flavor"]
				if assigned {
					log.Info("-----", "cluster", clusterMetrics.Name, "Skipping Assigned Node", node.Name)
					continue
				}
				log.Info("-----", "cluster", clusterMetrics.Name, "Unassigned Node", node.Name, "flavorMap.size", len(flavorMap))
				flavorResourceMap, found := flavorMap[string(r.DefaultResourceFlavorName)]
				if !found {
					log.Info("-----", "Node", node.Name, "Creating Map for", flavor.Name)
					flavorMap[r.DefaultResourceFlavorName] = map[string]*resource.Quantity{}
					flavorMap[r.DefaultResourceFlavorName] = make(map[string]*resource.Quantity, 0)
					flavorResourceMap = flavorMap[r.DefaultResourceFlavorName]
				}

				log.Info("-----", "Node", node.Name, "Handling Default Flavor", flavor.Name)
				r.addResourceQty(ctx, node, flavorResourceMap)
				log.Info("-----", "resource map size", len(flavorResourceMap))
			}
		}

		defaultFlavorResourceMap := flavorMap[r.DefaultResourceFlavorName]

		if len(defaultFlavorResourceMap) > 0 {
			for inx, resourceQuota := range flavor.Resources {
				resourceQuantity, found := defaultFlavorResourceMap[resourceQuota.Name.String()]
				if !found {
					continue
				}

				log.Info("", "flavor", r.DefaultResourceFlavorName, "resource", resourceQuota.Name, "resourceQuantity.Value()", resourceQuantity.Value(), "resourceQuota.NominalQuota", resourceQuota.NominalQuota.Value())
				// check if resource quota has changed
				if resourceQuantity.Value() > resourceQuota.NominalQuota.Value() {
					delta := resourceQuantity
					delta.Sub(resourceQuota.NominalQuota)
					flavor.Resources[inx].NominalQuota.Add(*delta)
					update = true
					log.Info("", "resource", resourceQuota.Name, "new clusterqueue flavor nominal quota", flavor.Resources[inx].NominalQuota)
				}
			}
		}
	}

	jsonData, err := json.MarshalIndent(flavorMap, "", "  ")
	if err != nil {
		return nil
	}
	log.Info("", "flavorMap", string(jsonData))

	if update {
		log.Info("Updating ClusterQueue")

		if err := r.Client.Update(ctx, &clusterQueue); err != nil {
			fmt.Printf("unable to Update clusterqueue: %s", err)
			return err
		}
		f1 := clusterQueue.Spec.ResourceGroups[0].Flavors[0]
		log.Info("Updated ClusterQueue", "flavor", f1.Name, "resource", f1.Resources[0].Name, "value", f1.Resources[0].NominalQuota)
		log.Info("Updated ClusterQueue", "flavor", f1.Name, "resource", f1.Resources[1].Name, "value", f1.Resources[1].NominalQuota)
	}
	return nil
}
func (r *ClusterMetricsReconciler) updateClusterQueueFlavorQuota(ctx context.Context, flavor v1beta1.FlavorQuotas, resourceMap map[string]*resource.Quantity) bool {
	log := log.FromContext(ctx)
	if len(resourceMap) > 0 {
		for inx, resourceQuota := range flavor.Resources {
			resourceQuantity, found := resourceMap[resourceQuota.Name.String()]
			if !found {
				continue
			}

			log.Info("", "flavor", flavor.Name, "resource", resourceQuota.Name, "resourceQuantity.Value()", resourceQuantity.Value(), "resourceQuota.NominalQuota", resourceQuota.NominalQuota.Value())
			// check if resource quota has changed
			if resourceQuantity.Value() > resourceQuota.NominalQuota.Value() {
				delta := resourceQuantity
				delta.Sub(resourceQuota.NominalQuota)
				flavor.Resources[inx].NominalQuota.Add(*delta)
				log.Info("", "resource", resourceQuota.Name, "new clusterqueue flavor nominal quota", flavor.Resources[inx].NominalQuota)
				return true
			}
		}
	}
	return false
}
func (r *ClusterMetricsReconciler) addResourceQty(ctx context.Context, node clustermetrics.NodeInfo, resourceMap map[string]*resource.Quantity) {
	log := log.FromContext(ctx)
	for nodeResource, quantity := range node.AllocatableResources {
		if resourceMap[nodeResource.String()] == nil {
			resourceMap[nodeResource.String()] = resource.NewQuantity(0, resource.BinarySI)
		}
		resourceMap[nodeResource.String()].Add(quantity)
		log.Info("addResourceQty", "Node", node.Name, "Increasing Resource", nodeResource.String(), "value", resourceMap[nodeResource.String()].Value()) //quantity)
	}
}
func (r *ClusterMetricsReconciler) nodeMatchesFlavor(ctx context.Context, node clustermetrics.NodeInfo, flavorName v1beta1.ResourceFlavorReference) (bool, error) {
	log := log.FromContext(ctx)
	flavor := v1beta1.ResourceFlavor{}
	fn := types.NamespacedName{
		Name: string(flavorName),
	}
	if err := r.Client.Get(ctx, fn, &flavor); err != nil {
		log.Info("nodeMatchesFlavor", "unable to get resource flavor", string(flavorName), "Error:", err)
		return false, err
	}

	matchCount := 0
	if flavor.Spec.NodeLabels != nil {
		for flavorLabel, flavorLabelValue := range flavor.Spec.NodeLabels {
			log.Info("nodeMatchesFlavor", "node", node.Name, "flavorLabel", flavorLabel, "flavorLabelValue", flavorLabelValue)
			for nodeLabel, nodeLabelValue := range node.Labels {
				if nodeLabel == flavorLabel && nodeLabelValue == flavorLabelValue {
					matchCount++
					break
				}
			}
		}
		log.Info("nodeMatchesFlavor", "matchCount", matchCount, "map size", len(flavor.Spec.NodeLabels))
		if matchCount == len(flavor.Spec.NodeLabels) {
			return true, nil
		}
	}
	return false, nil
}
func (r *ClusterMetricsReconciler) managedClusterEvent(e event.GenericEvent, obj client.Object) []reconcile.Request {

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterMetricsReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&clustermetrics.ClusterMetrics{}).
		Complete(r)
}
