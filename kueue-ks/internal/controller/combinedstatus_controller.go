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
	scheduler "kubestellar/galaxy/mc-scheduling/pkg/scheduler"
	"strconv"
	"time"

	ksv1alpha1 "github.com/kubestellar/kubestellar/api/control/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/clock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	kueue "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	kueueClient "sigs.k8s.io/kueue/client-go/clientset/versioned"
)

// CombinedStatusReconciler reconciles a CombinedStatus object
type CombinedStatusReconciler struct {
	client.Client
	Scheme             *runtime.Scheme
	RestClient         rest.Interface
	RestMapper         meta.RESTMapper
	KueueClient        *kueueClient.Clientset
	DynamicClient      *dynamic.DynamicClient
	Scheduler          scheduler.MultiClusterScheduler
	Recorder           record.EventRecorder
	Clock              clock.Clock
	WorkloadReconciler *WorkloadReconciler
}

func NewCombinedStatusReconciler(c client.Client, kueueClient *kueueClient.Clientset, cfg *rest.Config, rm meta.RESTMapper, record record.EventRecorder) *CombinedStatusReconciler {
	return &CombinedStatusReconciler{
		Client:        c,
		RestMapper:    rm,
		DynamicClient: dynamic.NewForConfigOrDie(cfg),
		KueueClient:   kueueClient,
		Scheduler:     scheduler.NewDefaultScheduler(),
		Recorder:      record,
		Clock:         clock.RealClock{},
	}
}

const (
	TimedoutAwaitingPodsReady = "TimedoutAwaitingPodsReady"
)

//+kubebuilder:rbac:groups=control.kubestellar.io.galaxy.kubestellar.io,resources=combinedstatuses,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=control.kubestellar.io.galaxy.kubestellar.io,resources=combinedstatuses/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=control.kubestellar.io.galaxy.kubestellar.io,resources=combinedstatuses/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the CombinedStatus object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.16.3/pkg/reconcile
func (r *CombinedStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	log.Info("Reconcile CombinedStatus -----------------")

	cs := &ksv1alpha1.CombinedStatus{}
	if err := r.Client.Get(ctx, req.NamespacedName, cs); err != nil {
		if !errors.IsNotFound(err) {
			log.Error(err, "Error when fetching CombinedStatus object ")
		}
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	if resource, found := cs.Labels["status.kubestellar.io/resource"]; found {
		if resource == "jobs" {
			r.handleStatus(ctx, cs)
		}
	}

	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

func (r *CombinedStatusReconciler) handleStatus(ctx context.Context, cs *ksv1alpha1.CombinedStatus) {
	log := log.FromContext(ctx)

	if len(cs.Results) > 0 {
		if len(cs.Results[0].Rows) > 0 {
			if len(cs.Results[0].Rows[0].Columns) > 0 {
				if cs.Results[0].Rows[0].Columns[1].Object != nil {
					object := cs.Results[0].Rows[0].Columns[1].Object
					var statusObject batchv1.JobStatus
					err := json.Unmarshal(object.Raw, &statusObject)
					if err != nil {
						log.Error(err, "Error when extracting status object from CombinedStatus ")
					}
					log.Info("Remote Job", "Active:", statusObject.Active, "Ready:", statusObject.Ready, "StartTime:", statusObject.StartTime)

					wl, err := r.fetchKueueWorkloadForJob(ctx, cs) //jobNamespace, jobName)
					if err != nil {
						log.Error(err, "Unable to fetch Workload object")
						return
					}
					log.Info("Got Workload Object", "Name", wl.GetName())
					cluster := *cs.Results[0].Rows[0].Columns[0].String
					evict := r.updateWorkloadAndTestForEviction(ctx, statusObject.Active, *statusObject.Ready, cluster, wl)
					log.Info("Should evict due to PodsReady timeout", "action", evict)
					if evict {

					}

				} else {
					log.Info("status() - CombinedStatus.Results[0].Rows[0].Columns[0].Object is missing")
				}
			} else {
				log.Info("status() - CombinedStatus.Results[0].Rows[0].Columns is missing")
			}
		} else {
			log.Info("status() - CombinedStatus.Results[0].Rows is missing")
		}
	} else {
		log.Info("status() - CombinedStatus.Results is missing")
	}

}

func (r *CombinedStatusReconciler) handleUnstructuredStatus(ctx context.Context, cs *ksv1alpha1.CombinedStatus) {
	log := log.FromContext(ctx)

	if len(cs.Results) > 0 {
		if len(cs.Results[0].Rows) > 0 {
			if len(cs.Results[0].Rows[0].Columns) > 0 {
				if cs.Results[0].Rows[0].Columns[1].Object != nil {
					object := cs.Results[0].Rows[0].Columns[1].Object

					var statusObject map[string]interface{}
					err := json.Unmarshal(object.Raw, &statusObject)
					if err != nil {
						log.Error(err, "Error when extracting status object from CombinedStatus ")
					}
					fmt.Printf(">>> %v\n", statusObject)
					wl, err := r.fetchKueueWorkloadForJob(ctx, cs)
					if err != nil || wl == nil {
						log.Error(err, "Unable to fetch Workload object")
						return
					}
					activePods, readyPods := r.getActiveAndReady(ctx, statusObject)
					startTime := statusObject["startTime"]
					log.Info("Remote Job", "Active", activePods, "Ready", readyPods, "StartTime", startTime)
					cluster := *cs.Results[0].Rows[0].Columns[0].String
					evict := r.updateWorkloadAndTestForEviction(ctx, int32(activePods), int32(readyPods), cluster, wl)
					if evict {

					}
					fmt.Printf("Remote Job - Active: %d Ready: %d StartTime: %s ", activePods, readyPods, startTime)
				} else {
					log.Info("status() - CombinedStatus.Results[0].Rows[0].Columns[1].Object is missing")
				}
			} else {
				log.Info("status() - CombinedStatus.Results[0].Rows[0].Columns is missing")
			}
		} else {
			log.Info("status() - CombinedStatus.Results[0].Rows is missing")
		}
	} else {
		log.Info("status() - CombinedStatus.Results is missing")
	}
}

// Inside your controller's Reconcile function or a relevant handler
func (r *CombinedStatusReconciler) fetchKueueWorkloadForJob(ctx context.Context /*jobNamespace, jobName string*/, cs *ksv1alpha1.CombinedStatus) (*kueue.Workload, error) {
	log := log.FromContext(ctx)
	jobName, found := cs.Labels["status.kubestellar.io/name"]
	if !found {
		log.Info("CombinedStatus status.kubestellar.io/name label is missing", "combinedstatus name", cs.Name)
		return nil, nil
	}
	jobNamespace, found := cs.Labels["status.kubestellar.io/namespace"]
	if !found {
		log.Info("CombinedStatus status.kubestellar.io/name label is missing", "combinedstatus name", cs.Name)
		return nil, nil
	}
	workloads := &kueue.WorkloadList{}
	err := r.Client.List(ctx, workloads, &client.ListOptions{})
	if err != nil {
		log.Error(err, "Unable to list Workload objects")
		return nil, err
	}
	for _, workload := range workloads.Items {
		if workload.OwnerReferences[0].Name == jobName && workload.Namespace == jobNamespace {
			return &workload, nil
		}
	}
	return nil, fmt.Errorf("no Kueue Workload found with name=%s in namespace %s", jobName, jobNamespace)
}
func (r *CombinedStatusReconciler) updateWorkloadAndTestForEviction(ctx context.Context, activePods int32, readyPods int32, cluster string, wl *kueue.Workload) bool {
	log := log.FromContext(ctx)
	if activePods > 0 && readyPods < activePods {
		log.Info("+++ Job pods are in Pending state")
		evicted := apimeta.IsStatusConditionTrue(wl.Status.Conditions, kueue.WorkloadEvicted)

		if !evicted && !apimeta.IsStatusConditionTrue(wl.Status.Conditions, string(kueue.CheckStatePending)) {

			log.Info("............ Adding Pending condition", "Job", wl.Name, "Namespace", wl.Namespace)
			newCondition := metav1.Condition{
				Type:    string(kueue.CheckStatePending),
				Status:  metav1.ConditionTrue,
				Reason:  "JobStatus",
				Message: fmt.Sprintf("Job pods Pending on Work Cluster %q", cluster),
			}
			err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				namespacedName := types.NamespacedName{
					Name:      wl.Name,
					Namespace: wl.Namespace,
				}
				wl := &kueue.Workload{}
				if err := r.Client.Get(ctx, namespacedName, wl); err != nil {
					log.Error(err, "Error when fetching Workload object ")
					return err
				}
				apimeta.SetStatusCondition(&wl.Status.Conditions, newCondition)

				err := r.Client.Status().Update(ctx, wl)
				if err != nil {

					return err
				}
				return nil
			})
			if err != nil {
				log.Error(err, "Unable to update Workload object")
				return false
			}
		} else if apimeta.IsStatusConditionTrue(wl.Status.Conditions, ClusterAssigned) {
			condition := apimeta.FindStatusCondition(wl.Status.Conditions, ClusterAssigned)
			if condition != nil {
				elapsedTime := r.Clock.Since(condition.LastTransitionTime.Local())
				log.Info("Pods on remote cluster still in Pending state", "elapsedTime", elapsedTime)
				if elapsedTime.Minutes() > 1 {
					newCondition := metav1.Condition{
						Type:    TimedoutAwaitingPodsReady,
						Status:  metav1.ConditionTrue,
						Reason:  TimedoutAwaitingPodsReady,
						Message: fmt.Sprintf("Timedout Waiting For Job Pods to Reach Ready - Cluster %q", cluster),
					}
					log.Info("Exceeded time threshold while waiting for pods Ready", "remote cluster", cluster)
					err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
						namespacedName := types.NamespacedName{
							Name:      wl.Name,
							Namespace: wl.Namespace,
						}
						wl := &kueue.Workload{}
						if err := r.Client.Get(ctx, namespacedName, wl); err != nil {
							log.Error(err, "Error when fetching Workload object ")
							return err
						}
						apimeta.SetStatusCondition(&wl.Status.Conditions, newCondition)

						err := r.Client.Status().Update(ctx, wl)
						if err != nil {
							log.Error(err, "Unable to update Workload object")
							return err
						}
						return nil //ctrl.Result{RequeueAfter: time.Duration(1 * float64(time.Second))}, nil
					})
					if err != nil {
						log.Error(err, "Unable to update Workload object")
					}
					return true
				}
			}
		}

	} else if activePods > 0 && readyPods == activePods {
		log.Info("updateWorkloadAndTestForEviction............ Job pods are Running", "Job", wl.Name, "Namespace", wl.Namespace)

		if !apimeta.IsStatusConditionTrue(wl.Status.Conditions, string(kueue.CheckStatePending)) {
			newCondition := metav1.Condition{
				Type:    string(kueue.CheckStateReady),
				Status:  metav1.ConditionTrue,
				Reason:  "JobStatus",
				Message: "Job Pods are Ready on the Work Cluster",
			}
			apimeta.SetStatusCondition(&wl.Status.Conditions, newCondition)
			err := r.Client.Status().Update(ctx, wl)
			if err != nil {
				log.Error(err, "Unable to update Workload object")
			}
		}
	}
	return false
}
func (r *CombinedStatusReconciler) getActiveAndReady(ctx context.Context, statusObject map[string]interface{}) (int, int) {
	log := log.FromContext(ctx)
	readyPods := 0
	fmt.Printf("\n>>> %v\n", statusObject)

	activeAsIntf, found := statusObject["active"]
	if !found {
		log.Info("active not found in statusObject")
	} else {
		fmt.Printf(".....activeAsIntf= %v\n", activeAsIntf)
	}
	readyAsIntf, found := statusObject["ready"]
	if !found {
		log.Info("ready not found in statusObject")
	}

	if foo, ok := activeAsIntf.(int); ok {
		fmt.Printf(".....activeAsIntf is int %d\n", foo)
	} else if foo, ok := activeAsIntf.(int8); ok {
		fmt.Printf(".....activeAsIntf is int8 %d\n", foo)
	} else if foo, ok := activeAsIntf.(int16); ok {
		fmt.Printf(".....activeAsIntf is int16 %d\n", foo)
	} else if foo, ok := activeAsIntf.(int32); ok {
		fmt.Printf(".....activeAsIntf is int32 %d\n", foo)
	} else if foo, ok := activeAsIntf.(int64); ok {
		fmt.Printf(".....activeAsIntf is int64 %d\n", foo)
	} else {
		fmt.Printf(".....activeAsIntf is unknown type\n")
	}

	active, _ := activeAsIntf.(int32)

	if active > 0 {
		readyAsString, ok := readyAsIntf.(string)
		if ok {
			ready, err := strconv.Atoi(string(readyAsString))
			if err != nil {
				log.Error(err, "unable to convert ready - value: %v", ready)
			}
			readyPods = ready
		}
	} else {
		log.Info("active is zero", "activeAsString", active)
	}
	return int(active), readyPods

}

// SetupWithManager sets up the controller with the Manager.
func (r *CombinedStatusReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ksv1alpha1.CombinedStatus{}).
		Complete(r)
}
