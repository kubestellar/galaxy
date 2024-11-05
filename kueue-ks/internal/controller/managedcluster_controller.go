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
	"errors"
	"fmt"

	ksv1alpha1 "github.com/kubestellar/kubestellar/api/control/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	clustermetrics "kubestellar/galaxy/clustermetrics/api/v1alpha1"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	ocmclusterv1 "open-cluster-management.io/api/cluster/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// ManagedClusterReconciler reconciles a ManagedCluster object
type ManagedClusterReconciler struct {
	client.Client
	ClientSet           *kubernetes.Clientset
	Scheme              *runtime.Scheme
	Its1Client          client.Client
	Wds1Client          client.Client
	KubeflexClient      client.Client
	ClusterMetricsImage string
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
		if apierrors.IsNotFound(err) {
			// ManagedCluster object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		logger.Error(err, "Failed to get ManagedCluster")
		return ctrl.Result{}, err
	}

	_, found := Clusters[managedCluster.Name]
	if !found {
		fmt.Printf(" >>>>Got ManagedCluster %s\n", managedCluster.Name)
		Clusters[managedCluster.Name] = managedCluster.Name

		bindingPolicy := ksv1alpha1.BindingPolicy{}
		namespacedName := types.NamespacedName{
			Name: managedCluster.Name,
		}
		if err := r.KubeflexClient.Get(ctx, namespacedName, &bindingPolicy); err != nil {
			// create BindingPolicy object per appwrapper
			if apierrors.IsNotFound(err) {
				if err := r.createBindingPolicyForCluster(ctx, managedCluster.Name, r.KubeflexClient); err != nil {
					return ctrl.Result{}, err
				}
			}
		}

		bindingPolicyWds1 := ksv1alpha1.BindingPolicy{}
		namespacedNameWds1 := types.NamespacedName{
			Name: managedCluster.Name,
		}
		if err := r.Wds1Client.Get(ctx, namespacedNameWds1, &bindingPolicyWds1); err != nil {
			// create BindingPolicy object per appwrapper
			if apierrors.IsNotFound(err) {
				logger.Info(">>>>Reconcile - creating BP for new cluster in wds1", "Cluster", managedCluster.Name)
				if err := r.createBindingPolicyForCluster(ctx, managedCluster.Name, r.Wds1Client); err != nil {
					return ctrl.Result{}, err
				}
			}
		} else {
			logger.Info(">>>>Reconcile - BP exists for new cluster in wds1", "Cluster", managedCluster.Name)
		}

		if err := r.createClusterMetrics(ctx, managedCluster); err != nil {
			logger.Error(err, "Failed to create ClusterMetrics in kubeflex context")
			return ctrl.Result{}, err
		}
		dep := &appsv1.Deployment{}
		fmt.Printf(" >>>>Getting Deployment object using namespaceName: %v\n", namespacedName)
		if err := r.Wds1Client.Get(ctx, namespacedName, dep); err != nil {
			if apierrors.IsNotFound(err) {
				if err := r.createClusterMetricsDeployment(ctx, managedCluster.Name); err != nil {
					logger.Error(err, "Failed to create clustermetrics deployment in wds1 context")
					return ctrl.Result{}, err
				}
			}
		}

	}

	return ctrl.Result{}, nil
}

func (r *ManagedClusterReconciler) createNamespace(context context.Context, namespace string) error {

	labels := map[string]string{"app.kubernetes.io/name": "clustermetrics"}
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   namespace,
			Labels: labels,
		},
	}
	if err := r.Wds1Client.Create(context, ns); err != nil {
		return err
	}
	return nil
}
func (r *ManagedClusterReconciler) createClusterMetrics(context context.Context, managedCluster ocmclusterv1.ManagedCluster) error {
	logger := log.FromContext(context)

	logger.Info(">>>>createClusterMetrics", "Cluster", managedCluster.Name)
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

			if err := r.KubeflexClient.Create(context, &cm); err != nil {
				return err
			}
		} else {
			return err
		}
	}
	return nil

}
func (r *ManagedClusterReconciler) createBindingPolicyForCluster(ctx context.Context, clusterName string, client client.Client) error {
	if clusterName == "" {
		return errors.New("cluster name is missing ")
	}
	fmt.Printf(">>>>>>>>>>>>>> createBindingPolicyForCluster Cluster %s\n", clusterName)
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
						//MatchLabels: labels,
						//MatchLabels: map[string]string{"targetCluster": clusterName},
						MatchLabels: map[string]string{"kubestellar.io/cluster": clusterName},
					},
				},
			},
			CreateOnly: true,
		},
	}

	bindingPolicy := ksv1alpha1.BindingPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: clusterName,
		},
		Spec: ksv1alpha1.BindingPolicySpec{
			ClusterSelectors:           clusterSelector,
			Downsync:                   downSync,
			WantSingletonReportedState: true,
		},
	}

	fmt.Printf(">>>>>>>>>>>>>> Creating BindingPolicy for ClusterMetrics %s\n", clusterName)

	bindingPolicy.Name = clusterName
	if err := client.Create(ctx, &bindingPolicy); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return nil
		}
		return err
	}
	return nil
}

func (r *ManagedClusterReconciler) createClusterMetricsDeployment(ctx context.Context, cluster string) error {
	labels := map[string]string{
		"control-plane":          "controller-manager",
		"kubestellar.io/cluster": cluster,
	}

	allowEscalation := new(bool)
	*allowEscalation = false
	nonRoot := new(bool)
	*nonRoot = true
	replicas := new(int32)
	*replicas = 1
	termination := new(int64)
	*termination = 10
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "clustermetrics-controller-manager-" + cluster,
			Namespace: "clustermetrics-system",
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"control-plane": "controller-manager",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"control-plane": "controller-manager",
					},
					Annotations: map[string]string{
						"kubectl.kubernetes.io/default-container": "manager",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "kube-rbac-proxy",
							Image: "gcr.io/kubebuilder/kube-rbac-proxy:v0.16.0",
							Args: []string{
								"--secure-listen-address=0.0.0.0:8443",
								"--upstream=http://127.0.0.1:8080/",
								"--logtostderr=true",
								"--v=0",
							},
							Ports: []corev1.ContainerPort{
								{
									Name:          "https",
									Protocol:      "TCP",
									ContainerPort: 8443,
								},
							},
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("128Mi"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("5m"),
									corev1.ResourceMemory: resource.MustParse("64Mi"),
								},
							},
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: allowEscalation,
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{
										"ALL",
									},
								},
							},
						},
						{
							Name:  "manager",
							Image: r.ClusterMetricsImage,
							Args: []string{
								"--health-probe-bind-address=:8081",
								"--metrics-bind-address=127.0.0.1:8080",
								"--leader-elect",
								"--metrics-name=" + cluster,
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Port: intstr.FromInt32(8081),
										Path: "/healthz",
									},
								},
							},
							Ports: []corev1.ContainerPort{
								{
									Name:          "https",
									Protocol:      "TCP",
									ContainerPort: 8443,
								},
							},
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("128Mi"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("5m"),
									corev1.ResourceMemory: resource.MustParse("64Mi"),
								},
							},
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: allowEscalation,
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{
										"ALL",
									},
								},
							},
						},
					},
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: nonRoot,
					},
					ServiceAccountName:            "clustermetrics-controller-manager",
					TerminationGracePeriodSeconds: termination,
				},
			},
		},
	}
	if err := r.Wds1Client.Create(ctx, deployment); err != nil {
		return err
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ManagedClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	Clusters = make(map[string]string)

	return ctrl.NewControllerManagedBy(mgr).
		For(&ocmclusterv1.ManagedCluster{}).
		Complete(r)
}
