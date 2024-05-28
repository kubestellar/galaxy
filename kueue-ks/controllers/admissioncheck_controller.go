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

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	kueue "sigs.k8s.io/kueue/apis/kueue/v1beta1"
)

// AdmissionCheckReconciler reconciles a AdmissionCheck object
type AdmissionCheckReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

const (
	// AdmissionCheckActive indicates that the controller of the admission check is
	// ready to evaluate the checks states
	AdmissionCheckActive string = "Active"

	// AdmissionChecksSingleInstanceInClusterQueue indicates if the AdmissionCheck should be the only
	// one managed by the same controller (as determined by the controllerName field) in a ClusterQueue.
	// Having multiple AdmissionChecks managed by the same controller where at least one has this condition
	// set to true will cause the ClusterQueue to be marked as Inactive.
	AdmissionChecksSingleInstanceInClusterQueue string = "SingleInstanceInClusterQueue"
)
const (
	ControllerName        = "kubestellar.io/ks-kueue"
	SingleInstanceReason  = "KubestellarKueue"
	SingleInstanceMessage = "only one KubestellarKueue managed admission check can be used in one ClusterQueue"
)

//+kubebuilder:rbac:groups=kueue.x-k8s.io.galaxy.kubestellar.io,resources=admissionchecks,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=kueue.x-k8s.io.galaxy.kubestellar.io,resources=admissionchecks/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=kueue.x-k8s.io.galaxy.kubestellar.io,resources=admissionchecks/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the AdmissionCheck object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.12.1/pkg/reconcile
func (r *AdmissionCheckReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	ac := &kueue.AdmissionCheck{}
	if err := r.Client.Get(ctx, req.NamespacedName, ac); err != nil || ac.Spec.ControllerName != ControllerName {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	log.Info("Reconcile AdmissionCheck - AC Name:" + ac.Name)
	for key, value := range ac.GetLabels() {
		fmt.Printf("Label %s:%s\n", key, value)
	}
	for key1, value1 := range ac.GetAnnotations() {
		fmt.Printf("Annotation %s:%s\n", key1, value1)
	}
	newCondition := metav1.Condition{
		Type:    kueue.AdmissionCheckActive,
		Status:  metav1.ConditionTrue,
		Reason:  "Active",
		Message: "The admission check is active",
	}
	needsUpdate := false
	oldCondition := apimeta.FindStatusCondition(ac.Status.Conditions, kueue.AdmissionCheckActive)
	if !cmpConditionState(oldCondition, &newCondition) {
		apimeta.SetStatusCondition(&ac.Status.Conditions, newCondition)
		needsUpdate = true
	}
	if !apimeta.IsStatusConditionTrue(ac.Status.Conditions, AdmissionChecksSingleInstanceInClusterQueue) {
		apimeta.SetStatusCondition(&ac.Status.Conditions, metav1.Condition{
			Type:    AdmissionChecksSingleInstanceInClusterQueue,
			Status:  metav1.ConditionTrue,
			Reason:  SingleInstanceReason,
			Message: SingleInstanceMessage,
		})
		needsUpdate = true
	}

	if needsUpdate {
		err := r.Client.Status().Update(ctx, ac)
		if err != nil {
			log.V(2).Error(err, "Updating check condition", "newCondition", newCondition)
		}
		return reconcile.Result{}, err
	}
	return ctrl.Result{}, nil
}
func cmpConditionState(a, b *metav1.Condition) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Status == b.Status && a.Reason == b.Reason && a.Message == b.Message
}

// SetupWithManager sets up the controller with the Manager.
func (r *AdmissionCheckReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
	    For(&kueue.AdmissionCheck{}).
		Complete(r)
}
