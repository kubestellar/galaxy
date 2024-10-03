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
	"errors"
	"fmt"

	ksv1alpha1 "github.com/kubestellar/kubestellar/api/control/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	clustermetrics "kubestellar/galaxy/clustermetrics/api/v1alpha1"
	ocmclusterv1 "open-cluster-management.io/api/cluster/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// ManagedClusterReconciler reconciles a ManagedCluster object
type ManagedClusterReconciler struct {
	client.Client
	ClientSet      *kubernetes.Clientset
	Scheme         *runtime.Scheme
	Its1Client     client.Client
	KubeflexClient client.Client
}

var Clusters map[string]string

//+kubebuilder:rbac:groups=cluster.open-cluster-management.io.galaxy.kubestellar.io,resources=managedclusters,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=cluster.open-cluster-management.io.galaxy.kubestellar.io,resources=managedclusters/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=cluster.open-cluster-management.io.galaxy.kubestellar.io,resources=managedclusters/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the ManagedCluster object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.12.1/pkg/reconcile
func (r *ManagedClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the ManagedCluster object
	var managedCluster ocmclusterv1.ManagedCluster
	if err := r.Its1Client.Get(ctx, req.NamespacedName, &managedCluster); err != nil {
		//	if err := r.Client.Get(ctx, req.NamespacedName, &managedCluster); err != nil {
		if apierrors.IsNotFound(err) {
			// ManagedCluster object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		logger.Error(err, "Failed to get ManagedCluster")
		return ctrl.Result{}, err
	} else {
		fmt.Printf(" >>>>Got ManagedCluster %s\n", managedCluster.Name)
	}

	_, found := Clusters[managedCluster.Name]
	if !found {
		Clusters[managedCluster.Name] = managedCluster.Name

		if err := r.createClusterMetrics(ctx, managedCluster); err != nil {
			logger.Error(err, "Failed to create ClusterMetrics in kubeflex context")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *ManagedClusterReconciler) createClusterMetrics(context context.Context, managedCluster ocmclusterv1.ManagedCluster) error {
	fmt.Printf(">>>>createClusterMetrics %s\n", managedCluster.Name)
	clusterMetricsName := types.NamespacedName{
		Name: managedCluster.Name,
	}
	cm := &clustermetrics.ClusterMetrics{}
	if err := r.KubeflexClient.Get(context, clusterMetricsName, cm); err != nil {
		if apierrors.IsNotFound(err) {
			cm := clustermetrics.ClusterMetrics{
				Spec: clustermetrics.ClusterMetricsSpec{},
			}
			cm.Name = managedCluster.Name
			cm.Labels = map[string]string{"app.kubernetes.io/name": "clustermetrics", "kubestellar.io/cluster": managedCluster.Name}

			if err := r.createBindingPolicyForCluster(context, managedCluster.Name); err != nil {
				return err
			}

			return r.KubeflexClient.Create(context, &cm)
		} else {
			return err
		}
	}
	return nil

}
func (r *ManagedClusterReconciler) createBindingPolicyForCluster(ctx context.Context, clusterName string) error {
	if clusterName == "" {
		return errors.New("cluster name is missing ")
	}
	fmt.Printf(">>>>>>>>>>>>>> createBindingPolicyForCluster Cluster %s\n", clusterName)
	bindingPolicy := ksv1alpha1.BindingPolicy{}
	namespacedName := types.NamespacedName{
		Name: clusterName,
	}
	if err := r.KubeflexClient.Get(ctx, namespacedName, &bindingPolicy); err != nil {
		// create BindingPolicy object per appwrapper
		if apierrors.IsNotFound(err) {

			clusterSelector := []metav1.LabelSelector{
				{
					MatchLabels: map[string]string{
						KsLabelLocationGroupKey: KsLabelLocationGroup,
						KsLabelClusterNameKey:   clusterName,
					},
				},
			}
			downSync := []ksv1alpha1.DownsyncPolicyClause{ //DownsyncObjectTestAndStatusCollection{
				{
					DownsyncObjectTest: ksv1alpha1.DownsyncObjectTest{
						ObjectSelectors: []metav1.LabelSelector{
							{
								MatchLabels: map[string]string{"kubestellar.io/cluster": clusterName},
							},
						},
					},
				},
			}

			bindingPolicy := ksv1alpha1.BindingPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name: clusterName,
				},
				Spec: ksv1alpha1.BindingPolicySpec{
					ClusterSelectors: clusterSelector,
					Downsync:         downSync,
				},
			}

			fmt.Printf(">>>>>>>>>>>>>> Creating BindingPolicy for ClusterMetrics %s\n", clusterName)

			bindingPolicy.Name = clusterName
			if err := r.KubeflexClient.Create(ctx, &bindingPolicy); err != nil {
				if apierrors.IsAlreadyExists(err) {
					return nil
				}
				return err
			}
			return nil

		} else {
			fmt.Printf(">>>>>>>>>>>>>> Error While Creating BindingPolicy for ClusterMetrics %v\n", err)
			return err
		}
	} else {
		return err
	}

}

// SetupWithManager sets up the controller with the Manager.
func (r *ManagedClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	Clusters = make(map[string]string)

	return ctrl.NewControllerManagedBy(mgr).
		For(&ocmclusterv1.ManagedCluster{}).
		Complete(r)
}
