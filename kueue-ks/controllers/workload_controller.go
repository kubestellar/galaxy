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
	"time"

	metricsv1alpha1 "kubestellar/galaxy/clustermetrics/api/v1alpha1"
	scheduler "kubestellar/galaxy/mc-scheduling/pkg/scheduler"

	ksv1alpha1 "github.com/kubestellar/kubestellar/api/control/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	kueue "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	kueueClient "sigs.k8s.io/kueue/client-go/clientset/versioned"
	"sigs.k8s.io/kueue/pkg/util/admissioncheck"
	"sigs.k8s.io/kueue/pkg/workload"
)

// WorkloadReconciler reconciles a Workload object
type WorkloadReconciler struct {
	Client        client.Client
	RestMapper    meta.RESTMapper
	KueueClient   *kueueClient.Clientset
	DynamicClient *dynamic.DynamicClient
	Scheduler     scheduler.MultiClusterScheduler
}

const (
	KsLabelLocationGroupKey = "location-group"
	KsLabelLocationGroup    = "edge"
	KsLabelClusterNameKey   = "name"
	AssignedClusterLabel    = "mcc.kubestellar.io/cluster"
)

//+kubebuilder:rbac:groups=kueue.x-k8s.io.galaxy.kubestellar.io,resources=workloads,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=kueue.x-k8s.io.galaxy.kubestellar.io,resources=workloads/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=kueue.x-k8s.io.galaxy.kubestellar.io,resources=workloads/finalizers,verbs=update

func NewWorkloadReconciler(c client.Client, kueueClient *kueueClient.Clientset, cfg *rest.Config, rm meta.RESTMapper) *WorkloadReconciler {
	return &WorkloadReconciler{
		Client:        c,
		RestMapper:    rm,
		DynamicClient: dynamic.NewForConfigOrDie(cfg),
		KueueClient:   kueueClient,
		Scheduler:     scheduler.NewDefaultScheduler(),
	}
}

// Reconciles kueue Workload object and if quota exists it downsyncs a job to a worker cluster.

// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.12.1/pkg/reconcile
func (r *WorkloadReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	log.Info("Reconcile Workload ----")

	wl := &kueue.Workload{}
	if err := r.Client.Get(ctx, req.NamespacedName, wl); err != nil {
		log.Error(err, "Error when fetching Workload object ")
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	if !workload.HasQuotaReservation(wl) {
		log.Info("workload with no reservation, delete owned requests")
		return reconcile.Result{}, r.evictJob(ctx, wl)
	}

	if apimeta.IsStatusConditionTrue(wl.Status.Conditions, kueue.WorkloadFinished) {
		log.Info("remote workload has completed")
		return reconcile.Result{}, nil
	}

	if IsAdmitted(wl) {
		// check the state of the request, eventually toggle the checks to false
		// otherwise there is nothing to here
		log.Info("workload admitted, sync checks")
		return reconcile.Result{}, nil
	}

	jobObject, mapping, err := r.getJobObject(ctx, wl.GetObjectMeta())
	if err != nil {
		log.Error(err, "Unable to fetch JobObject")
		return ctrl.Result{}, err
	}

	log.Info("Found  jobObject ----" + jobObject.GetName())
	if jobObject.GetLabels() == nil {
		jobObject.SetLabels(map[string]string{})
	}
	// jobs with assigned cluster have already been scheduled to run
	if _, exists := jobObject.GetLabels()[AssignedClusterLabel]; exists {
		if workload.HasAllChecksReady(wl) {
			err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				wl := &kueue.Workload{}
				if err := r.Client.Get(ctx, req.NamespacedName, wl); err != nil {
					log.Error(err, "Error when fetching Workload object ")
					return err
				}
				log.Info("............ All Checks Ready")
				newCondition := metav1.Condition{
					Type:    string(kueue.CheckStateReady),
					Status:  metav1.ConditionTrue,
					Reason:  "ClusterAssigned",
					Message: fmt.Sprintf("Job Ready for Sync to the Work Cluster %q", jobObject.GetLabels()[AssignedClusterLabel]),
				}
				apimeta.SetStatusCondition(&wl.Status.Conditions, newCondition)
				return r.Client.Status().Update(ctx, wl)
			})
			if err != nil {
				return reconcile.Result{}, err
			}

			meta := &metav1.ObjectMeta{
				Name:      jobObject.GetName(),
				Namespace: jobObject.GetNamespace(),
				Labels:    jobObject.GetLabels(),
			}

			if err = r.createBindingPolicy(ctx, meta); err != nil {
				log.Error(err, "Error creating BindingPolicy object ")
				return reconcile.Result{}, err
			}
			log.Info("New BindingPolicy created for object", "Name", meta.Name)

		} else {
			relevantChecks, err := admissioncheck.FilterForController(ctx, r.Client, wl.Status.AdmissionChecks, ControllerName)
			if err != nil {
				return reconcile.Result{}, err
			}

			if len(relevantChecks) == 0 {
				return reconcile.Result{}, nil
			}
			for check := range relevantChecks {
				log.Info(">>>>>>>>>>>>>> relevant check", "", check)
			}
			acs := workload.FindAdmissionCheck(wl.Status.AdmissionChecks, relevantChecks[0])

			if acs == nil {
				log.Info(">>>>>>>>>>>>>> admissioncheck is null")
			} else {
				log.Info(">>>>>>>>>>>>>> ", "ACS", acs)
				acs.State = kueue.CheckStateReady
				acs.Message = fmt.Sprintf("The workload got reservation on %q", jobObject.GetLabels()[AssignedClusterLabel])
				// update the transition time since is used to detect the lost worker state.
				acs.LastTransitionTime = metav1.NewTime(time.Now())
				wlPatch := workload.BaseSSAWorkload(wl)
				workload.SetAdmissionCheckState(&wlPatch.Status.AdmissionChecks, *acs)
				err := r.Client.Status().Patch(ctx, wlPatch, client.Apply, client.FieldOwner(ControllerName), client.ForceOwnership)
				if err != nil {
					return reconcile.Result{}, err
				}
			}
		}
		return ctrl.Result{}, nil
	}
	podSpecs, err := extractPodSpecList(wl)
	if err != nil {
		return ctrl.Result{}, err
	}

	clusterMetricsList := &metricsv1alpha1.ClusterMetricsList{}
	err = r.Client.List(ctx, clusterMetricsList, &client.ListOptions{})
	if err != nil {
		return ctrl.Result{}, err
	}
	requeAfter := time.Duration(10 * float64(time.Second))

	cluster := r.Scheduler.SelectCluster(podSpecs, clusterMetricsList)

	if cluster == "" {
		log.Info("------------- Scheduler did not find suitable cluster for a Job to run")
		return reconcile.Result{RequeueAfter: requeAfter}, nil
	}
	fmt.Printf("Selected cluster: %s\n", cluster)
	labels := jobObject.GetLabels()
	labels[AssignedClusterLabel] = cluster

	jobObject.SetLabels(labels)

	_, err = r.DynamicClient.Resource(mapping.Resource).Namespace(wl.Namespace).Update(ctx, jobObject, metav1.UpdateOptions{})
	if err != nil {
		log.Error(err, "Error when Updating object ")
		return ctrl.Result{}, err
	}
	log.Info("Updated  jobObject with target cluster label ----" + jobObject.GetName())

	return ctrl.Result{RequeueAfter: time.Duration(1 * float64(time.Second))}, nil

}

func (r *WorkloadReconciler) RemoteFinishedCondition(wl kueue.Workload) *metav1.Condition {
	var bestMatch *metav1.Condition
	if c := apimeta.FindStatusCondition(wl.Status.Conditions, kueue.WorkloadFinished); c != nil && c.Status == metav1.ConditionTrue && (bestMatch == nil || c.LastTransitionTime.Before(&bestMatch.LastTransitionTime)) {
		bestMatch = c
	}

	return bestMatch
}
func extractPodSpecList(workload *kueue.Workload) ([]*corev1.PodSpec, error) {
	podSpecList := []*corev1.PodSpec{}
	for _, podSet := range workload.Spec.PodSets {
		podSpecList = append(podSpecList, podSet.Template.Spec.DeepCopy())
	}
	return podSpecList, nil
}

/*
Evicts job from a WEC by deleting its BindingPolicy on the controlling host (HUB)
*/
func (r *WorkloadReconciler) evictJob(ctx context.Context, workload *kueue.Workload) error {
	log := log.FromContext(ctx)
	log.Info("evictJob() ----")
	wlOwners := workload.GetObjectMeta().GetOwnerReferences()

	meta := &metav1.ObjectMeta{
		Name:      wlOwners[0].Name,
		Namespace: workload.Namespace,
	}
	namespacedName := types.NamespacedName{
		Name: bindingPolicyName(meta),
	}
	bindingPolicy := ksv1alpha1.BindingPolicy{}
	if err := r.Client.Get(ctx, namespacedName, &bindingPolicy); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if err := r.Client.Delete(ctx, &bindingPolicy); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		log.Error(err, "evictJob() ---- Error deleting BindingPolicy object ")
		return err
	}
	log.Info("Deleted BindingPolicy ----", "", bindingPolicyName(meta))
	return nil
}
func WorkloadKey(req ctrl.Request) string {
	return fmt.Sprintf("%s/%s", req.Namespace, req.Name)
}

// IsAdmitted returns true if the workload is admitted.
func IsAdmitted(w *kueue.Workload) bool {
	return apimeta.IsStatusConditionTrue(w.Status.Conditions, kueue.WorkloadAdmitted)
}
func (r *WorkloadReconciler) getJobObject(ctx context.Context, meta metav1.Object) (*unstructured.Unstructured, *apimeta.RESTMapping, error) {
	log := log.FromContext(ctx)
	log.Info("getJobObject() ----")

	wlOwners := meta.GetOwnerReferences()
	log.Info("workload ", "", meta.GetName())
	gvk := schema.FromAPIVersionAndKind(wlOwners[0].APIVersion, wlOwners[0].Kind)
	gk := schema.GroupKind{Group: gvk.Group, Kind: gvk.Kind}
	mapping, err := r.RestMapper.RESTMapping(gk, gvk.Version)
	if err != nil {
		log.Error(err, "Error when RESTMapping object ")
		return nil, nil, err
	}
	jobObject, err := r.DynamicClient.Resource(mapping.Resource).Namespace(meta.GetNamespace()).Get(ctx, wlOwners[0].Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Error(err, "Job object not found")
			return nil, nil, err
		}
		log.Error(err, "Error when RESTMapping object ")
		return nil, nil, err
	}

	return jobObject.DeepCopy(), mapping, nil
}
func (*WorkloadReconciler) GetWorkloadKey(o runtime.Object) (types.NamespacedName, error) {
	wl, isWl := o.(*kueue.Workload)
	if !isWl {
		return types.NamespacedName{}, errors.New("not a workload")
	}
	return client.ObjectKeyFromObject(wl), nil
}
func (r *WorkloadReconciler) createBindingPolicy(ctx context.Context, meta metav1.Object) error {
	bindingPolicy := ksv1alpha1.BindingPolicy{}
	namespacedName := types.NamespacedName{
		Name: bindingPolicyName(meta),
	}
	if err := r.Client.Get(ctx, namespacedName, &bindingPolicy); err != nil {
		// create BindingPolicy object per appwrapper
		if apierrors.IsNotFound(err) {

			if meta.GetLabels() != nil && meta.GetLabels()[AssignedClusterLabel] != "" {

				bindingPolicy = ksv1alpha1.BindingPolicy{
					Spec: ksv1alpha1.BindingPolicySpec{
						ClusterSelectors: []metav1.LabelSelector{
							{
								MatchLabels: map[string]string{
									KsLabelLocationGroupKey: KsLabelLocationGroup,
									KsLabelClusterNameKey:   meta.GetLabels()[AssignedClusterLabel],
								},
							},
						},
						Downsync: []ksv1alpha1.DownsyncObjectTest{
							{
								Namespaces: []string{
									meta.GetNamespace(),
								},
								ObjectNames: []string{
									meta.GetName(),
								},

								ObjectSelectors: []metav1.LabelSelector{
									{
										MatchLabels: map[string]string{
											AssignedClusterLabel: meta.GetLabels()[AssignedClusterLabel],
										},
									},
								},
							},
						},
						// turn the flag on so that kubestellar updates status of the appwrapper
						WantSingletonReportedState: true,
					},
				}
				bindingPolicy.Name = bindingPolicyName(meta)
				if err := r.Client.Create(ctx, &bindingPolicy); err != nil {
					if apierrors.IsAlreadyExists(err) {
						return nil
					}
					return err
				}
				// BindingPolicy created
				return nil
			} else {
				return errors.New("cluster assignment label is missing from the Object - Add label " + AssignedClusterLabel)
			}
		} else {
			return err
		}
	}
	return nil
}
func bindingPolicyName(meta metav1.Object) string {
	return meta.GetName() + "-" + meta.GetNamespace()
}

// SetupWithManager sets up the controller with the Manager.
func (r *WorkloadReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kueue.Workload{}).
		Complete(r)
}
