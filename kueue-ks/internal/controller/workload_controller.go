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
	"k8s.io/apimachinery/pkg/labels"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"

	"k8s.io/utils/clock"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	kueue "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	kueueClient "sigs.k8s.io/kueue/client-go/clientset/versioned"
	"sigs.k8s.io/kueue/pkg/util/admissioncheck"
	"sigs.k8s.io/kueue/pkg/workload"
)

// WorkloadReconciler reconciles a Workload object
type WorkloadReconciler struct {
	Client                 client.Client
	RestClient             rest.Interface
	RestMapper             meta.RESTMapper
	KueueClient            *kueueClient.Clientset
	DynamicClient          *dynamic.DynamicClient
	Scheduler              scheduler.MultiClusterScheduler
	Recorder               record.EventRecorder
	Clock                  clock.Clock
	CleanupWecOnCompletion bool
}

const (
	KsLabelLocationGroupKey = "location-group"
	KsLabelLocationGroup    = "edge"
	KsLabelClusterNameKey   = "name"
	//AssignedClusterLabel    = "mcc.kubestellar.io/cluster"
	AssignedClusterLabel   = "kubestellar.io/cluster"
	NodeSelectorAddedBy    = "workloadcontroller"
	NodeSelectorController = "mcc.nodeselectororigin"
	ClusterAssigned        = "ClusterAssigned"
	Pending                = "Pending"
)

//+kubebuilder:rbac:groups=kueue.x-k8s.io.galaxy.kubestellar.io,resources=workloads,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=kueue.x-k8s.io.galaxy.kubestellar.io,resources=workloads/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=kueue.x-k8s.io.galaxy.kubestellar.io,resources=workloads/finalizers,verbs=update

func NewWorkloadReconciler(c client.Client, kueueClient *kueueClient.Clientset, cfg *rest.Config, rm meta.RESTMapper, record record.EventRecorder, cmImage string) *WorkloadReconciler {
	return &WorkloadReconciler{
		Client:        c,
		RestMapper:    rm,
		DynamicClient: dynamic.NewForConfigOrDie(cfg),
		KueueClient:   kueueClient,
		Scheduler:     scheduler.NewDefaultScheduler(),
		Recorder:      record,
		Clock:         clock.RealClock{},
	}
}

// Reconciles kueue Workload object and if quota exists it downsyncs a job to a worker cluster.

// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.12.1/pkg/reconcile
func (r *WorkloadReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	log.Info("Reconcile Workload -----------------")

	wl := &kueue.Workload{}
	if err := r.Client.Get(ctx, req.NamespacedName, wl); err != nil {
		log.Error(err, "Error when fetching Workload object ")
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	fmt.Printf("---------------- wl.Status %v\n", wl.Status)
	fmt.Printf("---------------- wl.isAadmitted %v\n", workload.IsAdmitted(wl))
	if match := r.RemoteFinishedCondition(*wl); match != nil {
		log.Info("workload finished", "CleanupWecOnCompletion", r.CleanupWecOnCompletion)
		if r.CleanupWecOnCompletion {
			log.Info("Evicting from a remote cluster due to workload completed")
			//return reconcile.Result{}, r.evictJobByBindingPolicyDelete(ctx, wl)
			return reconcile.Result{}, r.evictJobByLabel(ctx, wl)
		}
	} else if apimeta.IsStatusConditionTrue(wl.Status.Conditions, kueue.WorkloadEvicted) {
		log.Info("............ Workload Evicted", "Job", wl.Name, "Namespace", wl.Namespace)
		return reconcile.Result{}, nil
	}

	if workload.HasQuotaReservation(wl) {
		return r.handleJobWithQuota(ctx, wl)
	} else {
		// Check if workload was preempted. In such case, Kueue will take quota back and
		// set a condition of type Preempted to true. Preempted job will be evicted and
		// a new condition will be added to direct kueue to requeue this job and retry
		// later, possibly using a different flavor
		return r.handleJobWithNoQuota(ctx, wl)
	}
}

/*
only jobs with quota are allowed to run.
*/
func (r *WorkloadReconciler) handleJobWithQuota(ctx context.Context, wl *kueue.Workload) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	jobObject, mapping, err := r.getJobObject(ctx, wl.GetObjectMeta())
	if err != nil {
		log.Error(err, "Unable to fetch JobObject")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log.Info("Found  jobObject ----" + jobObject.GetName())
	if jobObject.GetLabels() == nil {
		jobObject.SetLabels(map[string]string{})
	}
	_, jobAssignedToCluster := jobObject.GetLabels()[AssignedClusterLabel]

	namespacedName := types.NamespacedName{
		Name:      wl.Name,
		Namespace: wl.Namespace,
	}
	log.Info("handleJobWithQuota", "jobAssignedToCluster", jobAssignedToCluster)
	if jobAssignedToCluster {

		if !apimeta.IsStatusConditionTrue(wl.Status.Conditions, ClusterAssigned) {
			err := retry.RetryOnConflict(retry.DefaultRetry, func() error {

				wl := &kueue.Workload{}
				if err := r.Client.Get(ctx, namespacedName, wl); err != nil {
					log.Error(err, "Error when fetching Workload object ")
					return err
				}

				log.Info("............ Got Quota Reservation", "Job", wl.Name, "Namespace", wl.Namespace)
				newCondition := metav1.Condition{
					Type:    ClusterAssigned,
					Status:  metav1.ConditionTrue,
					Reason:  ClusterAssigned,
					Message: fmt.Sprintf("Job Ready for Sync to the Work Cluster %q", jobObject.GetLabels()[AssignedClusterLabel]),
				}
				apimeta.SetStatusCondition(&wl.Status.Conditions, newCondition)
				return r.Client.Status().Update(ctx, wl)

			})
			if err != nil {
				return reconcile.Result{}, err
			}
			return ctrl.Result{RequeueAfter: time.Duration(1 * float64(time.Second))}, nil
		} /* else {
			if r.jobPendingPodReadyTimeout(ctx, jobObject.GetName(), jobObject.GetNamespace()) {
				log.Info("............ Job Pods stuck in Pending state beyond allowed threshold - evicting job from the WEC")
			}
		} */

	} else {
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
		fmt.Printf("---------------- workload.admitted: %v\n", workload.IsAdmitted(wl))

		fmt.Printf("---------------- wl.Status.Admission %v\n", wl.Status.Admission)

		if wl.Status.Admission == nil {
			log.Info("------------- Kueue has not added admission details - retrying ...")
			return reconcile.Result{RequeueAfter: requeAfter}, nil
		}
		var flavor string
		for _, flavorName := range wl.Status.Admission.PodSetAssignments[0].Flavors {
			flavor = string(flavorName)
			break
		}

		fmt.Printf("---------------- workload flavor %s\n", flavor)

		// podSpecs is a temporary structure which is needed by the scheduler.
		// Below add a nodeSelector if not specified by user already.

		for _, podSpec := range podSpecs {
			// Kueue only injects nodeSelector to admitted workloads. Since scheduling occurs
			// on the mgmt host where workloads are not running we need to inject nodeSelector
			// before scheduler is called.
			if podSpec.NodeSelector == nil {
				podSpec.NodeSelector = map[string]string{"instance": string(flavor)}
			}
		}

		cluster := r.Scheduler.SelectCluster(podSpecs, clusterMetricsList)
		if cluster == "" {
			log.Info("------------- Scheduler did not find suitable cluster for a Job to run")
			return reconcile.Result{RequeueAfter: requeAfter}, nil
		}
		fmt.Printf("Selected cluster: %s\n", cluster)
		// inject node selector into a job
		if err := r.injectNodeSelector(jobObject, flavor); err != nil {
			log.Info("-------------Unable to inject node selector into the job")
			return ctrl.Result{}, err
		}

		if r.Recorder != nil {
			r.Recorder.Event(jobObject, corev1.EventTypeNormal, "AssignedFlavor", "will run in node pool: "+flavor)
		}

		if r.Recorder != nil {
			r.Recorder.Event(jobObject, corev1.EventTypeNormal, "AssignedCluster", "target cluster:"+cluster)
		}
		labels := jobObject.GetLabels()
		labels[AssignedClusterLabel] = cluster
		jobObject.SetLabels(labels)

		_, err = r.DynamicClient.Resource(mapping.Resource).Namespace(wl.Namespace).Update(ctx, jobObject, metav1.UpdateOptions{})
		if err != nil {
			log.Error(err, "Error when Updating object ")
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

func (r *WorkloadReconciler) handleJobWithNoQuota(ctx context.Context, wl *kueue.Workload) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	log.Info("workload with no reservation")

	if err := r.evictJobByLabel(ctx, wl); err != nil {
		log.Error(err, "Error when while evicting job ")
		return reconcile.Result{}, err
	}
	if apimeta.IsStatusConditionTrue(wl.Status.Conditions, "Preempted") {
		jobObject, mapping, err := r.getJobObject(ctx, wl.GetObjectMeta())
		if err != nil {
			log.Error(err, "Unable to fetch JobObject")
			return ctrl.Result{}, err
		}
		// this workload was preempted but it still has a label assigned by
		// this controller earlier which states which cluster to use. If label
		// exists, delete it so that Kueue can retry it later on possibly different
		// cluster
		_, jobAssignedToCluster := jobObject.GetLabels()[AssignedClusterLabel]
		if jobAssignedToCluster {
			newLabels := make(map[string]string)
			for key, value := range jobObject.GetLabels() {
				if key != AssignedClusterLabel {
					newLabels[key] = value
				}
			}
			jobObject.SetLabels(newLabels)

			// remove node selector if it was added by this controller
			if err := removeNodeSelector(jobObject); err != nil {
				log.Error(err, "Unable to remove nodeSelector from JobObject")
				return ctrl.Result{}, err
			}

			_, err = r.DynamicClient.Resource(mapping.Resource).Namespace(wl.Namespace).Update(ctx, jobObject, metav1.UpdateOptions{})
			if err != nil {
				log.Error(err, "Error when Updating object ")
				return ctrl.Result{}, err
			}

			log.Info("Reset jobObject due to Eviction -- ", "Job", jobObject.GetName())
		}
		log.Info("workload Preempted - Requeing")
		return reconcile.Result{}, r.requeueDueToNoQuota(ctx, *wl)
	}
	return reconcile.Result{}, nil
}

func (r *WorkloadReconciler) injectNodeSelector(job *unstructured.Unstructured, flavor string) error {
	spec, ok := job.Object["spec"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("job spec is not a map")
	}
	template, ok := spec["template"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("job template is not a map")
	}
	template["spec"].(map[string]interface{})["nodeSelector"] = map[string]string{"instance": flavor}

	if job.GetAnnotations() == nil {
		job.SetAnnotations(map[string]string{})
	}
	newAnnotations := job.GetAnnotations()
	_, controllerAddedNodeSelector := job.GetAnnotations()[NodeSelectorController]
	if !controllerAddedNodeSelector {
		newAnnotations[NodeSelectorController] = NodeSelectorAddedBy
		job.SetAnnotations(newAnnotations)
		fmt.Printf("---------------- injectNodeSelector Annotation: NodeSelectorController\n")
	}
	return nil
}

/*
	    Removes nodeSelector only if it was injected by this controller earlier. The removal is warranted
		in case where a workload is preempted and subsequently retried. It initially is assigned to run
		on some cluster but Kueue may decide to run it elsewhere after requeue. Initial nodeSelector
		must thus be removed as part of a preemption.
*/
func removeNodeSelector(job *unstructured.Unstructured) error {

	newAnnotations := make(map[string]string)
	_, controllerAddedNodeSelector := job.GetAnnotations()[NodeSelectorController]
	if controllerAddedNodeSelector {
		for key, annotation := range job.GetAnnotations() {
			if key == NodeSelectorController {
				spec, ok := job.Object["spec"].(map[string]interface{})
				if !ok {
					return fmt.Errorf("job spec is not a map")
				}
				template, ok := spec["template"]
				if !ok {
					return fmt.Errorf("job template is not a map")
				}
				templateSpec, ok := template.(map[string]interface{})["spec"].(map[string]interface{})
				if ok {
					delete(templateSpec, "nodeSelector")
				}
				// skip the annotation. This job will be requeued in Kueue and subject to new cluster assignment
				continue
			}
			newAnnotations[key] = annotation
		}
		job.SetAnnotations(newAnnotations)
	}

	return nil
}
func (r *WorkloadReconciler) jobPendingPodReadyTimeout(ctx context.Context, jobName, jobNamespace string) bool {

	combinedStatus, err := r.getCombinedStatus(ctx, jobName, jobNamespace)
	if err != nil {
		return false
	}
	value, found := combinedStatus.Annotations["PodReadyTimeout"]
	if found && value == "true" {
		return true
	}
	return false
}

func (r *WorkloadReconciler) ReclaimQuota(ctx context.Context, wl *kueue.Workload, cl client.Client, reason, message string) error {
	log := log.FromContext(ctx)
	log.Info("ReclaimQuota ----")
	if apimeta.IsStatusConditionTrue(wl.Status.Conditions, kueue.WorkloadEvicted) {
		// the workload has already been evicted by the PodsReadyTimeout
		return nil
	}
	jobObject, mapping, err := r.getJobObject(ctx, wl.GetObjectMeta())
	if err != nil {
		log.Error(err, "Unable to fetch JobObject")
		return err
	}
	// this workload was preempted but it still has a label assigned by
	// this controller earlier which states which cluster to use. If label
	// exists, delete it so that Kueue can retry it later on possibly different
	// cluster
	_, jobAssignedToCluster := jobObject.GetLabels()[AssignedClusterLabel]
	if jobAssignedToCluster {
		newLabels := make(map[string]string)
		for key, value := range jobObject.GetLabels() {
			if key != AssignedClusterLabel {
				newLabels[key] = value
			}
		}
		jobObject.SetLabels(newLabels)

		_, err = r.DynamicClient.Resource(mapping.Resource).Namespace(wl.Namespace).Update(ctx, jobObject, metav1.UpdateOptions{})
		if err != nil {
			log.Error(err, "Error when Updating object ")
			return err
		}
		log.Info("ReclaimQuota ---- Removed BP label")
	}

	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		namespacedName := types.NamespacedName{
			Name:      wl.Name,
			Namespace: wl.Namespace,
		}
		newWl := &kueue.Workload{}
		if err := cl.Get(ctx, namespacedName, newWl); err != nil {
			log.Error(err, "Error when fetching Workload object ")
			return client.IgnoreNotFound(err)
		}

		log.Info("ReclaimQuota - --- removing Conditions")

		if existing := apimeta.FindStatusCondition(newWl.Status.Conditions, Pending); existing != nil {
			apimeta.RemoveStatusCondition(&newWl.Status.Conditions, Pending)
		}
		if existing := apimeta.FindStatusCondition(newWl.Status.Conditions, ClusterAssigned); existing != nil {
			apimeta.RemoveStatusCondition(&newWl.Status.Conditions, ClusterAssigned)
		}

		log.Info("ReclaimQuota - --- Conditions removed - setting Evicted condition")

		err = cl.Status().Update(ctx, newWl)
		if err != nil {
			return err
		}
		log.Info("ReclaimQuota - --- Conditions removed - Evicted condition set")
		return nil
	})

	if err != nil {
		return err
	}

	return nil
}
func (r *WorkloadReconciler) triggerDeactivationOrBackoffRequeue(ctx context.Context, wl *kueue.Workload) (bool, error) {

	if wl.Status.RequeueState == nil {
		wl.Status.RequeueState = &kueue.RequeueState{}
	}
	// If requeuingBackoffLimitCount equals to null, the workloads is repeatedly and endless re-queued.
	requeuingCount := ptr.Deref(wl.Status.RequeueState.Count, 0) + 1

	var waitDuration time.Duration
	waitDuration = time.Duration(30) * time.Second
	fmt.Printf("---------------- triggerDeactivationOrBackoffRequeue() ---- waitDuration %v\n", waitDuration)
	fmt.Printf("---------------- triggerDeactivationOrBackoffRequeue() - clock now  %v\n", r.Clock.Now())
	wl.Status.RequeueState.RequeueAt = ptr.To(metav1.NewTime(r.Clock.Now().Add(waitDuration)))
	wl.Status.RequeueState.Count = &requeuingCount
	requeueAfter := wl.Status.RequeueState.RequeueAt.Time.Sub(r.Clock.Now())
	fmt.Printf("---------------- triggerDeactivationOrBackoffRequeue() - requeueAfter %v\n", requeueAfter)
	return false, nil
}
func (r *WorkloadReconciler) requeueDueToNoQuota(ctx context.Context, wl kueue.Workload) error {
	relevantChecks, err := admissioncheck.FilterForController(ctx, r.Client, wl.Status.AdmissionChecks, ControllerName)
	if err != nil {
		return err
	}

	if len(relevantChecks) == 0 {
		return nil
	}

	if acs := workload.FindAdmissionCheck(wl.Status.AdmissionChecks, relevantChecks[0]); acs != nil {
		// add a condition to force Keueu to requeue a workload
		acs.State = kueue.CheckStatePending
		acs.Message = "Requeued"
		acs.LastTransitionTime = metav1.NewTime(time.Now())
		wlPatch := workload.BaseSSAWorkload(&wl)
		workload.SetAdmissionCheckState(&wlPatch.Status.AdmissionChecks, *acs)
		err = r.Client.Status().Patch(ctx, wlPatch, client.Apply, client.FieldOwner(ControllerName), client.ForceOwnership)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *WorkloadReconciler) testRunOnHost(ctx context.Context, wl kueue.Workload) error {
	relevantChecks, err := admissioncheck.FilterForController(ctx, r.Client, wl.Status.AdmissionChecks, ControllerName)
	if err != nil {
		return err
	}

	if len(relevantChecks) == 0 {
		return nil
	}

	if acs := workload.FindAdmissionCheck(wl.Status.AdmissionChecks, relevantChecks[0]); acs != nil {

		acs.State = kueue.CheckStateReady
		acs.Message = "Ready" // this setting allows workload to run on the host (test mode only)
		acs.LastTransitionTime = metav1.NewTime(time.Now())
		wlPatch := workload.BaseSSAWorkload(&wl)
		workload.SetAdmissionCheckState(&wlPatch.Status.AdmissionChecks, *acs)
		err = r.Client.Status().Patch(ctx, wlPatch, client.Apply, client.FieldOwner(ControllerName), client.ForceOwnership)
		if err != nil {
			return err
		}
	}
	return nil
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
Evicts job from a WEC by removing AssignedClusterLabel label from a job on the management cluster
*/
func (r *WorkloadReconciler) evictJobByLabel(ctx context.Context, wl *kueue.Workload) error {
	log := log.FromContext(ctx)
	log.V(4).Info("evictJob() ----")

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {

		jobObject, mapping, err := r.getJobObject(ctx, wl.GetObjectMeta())
		if err != nil {
			log.Error(err, "Unable to fetch JobObject")
			return err
		}
		cluster := ""
		log.V(4).Info("Found  jobObject ----" + jobObject.GetName())
		if jobObject.GetLabels() != nil {
			newLabels := (map[string]string{})
			for key, value := range jobObject.GetLabels() {
				if key == AssignedClusterLabel {
					continue
				}
				newLabels[key] = value
			}
			jobObject.SetLabels(newLabels)
		}

		if r.Recorder != nil {
			r.Recorder.Event(jobObject.DeepCopy(), corev1.EventTypeNormal, "DeleteJobFromCluster", "Evict from "+cluster)
		}
		_, err = r.DynamicClient.Resource(mapping.Resource).Namespace(wl.Namespace).Update(ctx, jobObject, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
		return nil
	})

	if err != nil {
		return err
	}

	return nil
}
func (r *WorkloadReconciler) evictJobByBindingPolicyDelete(ctx context.Context, wl *kueue.Workload) error {
	log := log.FromContext(ctx)
	log.Info("evictJobByBindingPolicyDelete() ----")
	wlOwners := wl.GetObjectMeta().GetOwnerReferences()

	meta := &metav1.ObjectMeta{
		Name:      wlOwners[0].Name,
		Namespace: wl.Namespace,
	}
	namespacedName := types.NamespacedName{
		Name:      bindingPolicyName(meta),
		Namespace: wl.Namespace,
	}
	bindingPolicy := ksv1alpha1.BindingPolicy{}
	if err := r.Client.Get(ctx, namespacedName, &bindingPolicy); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		log.Error(err, "evictJobByBindingPolicyDelete() ---- Error Getting BindingPolicy object ")
		return err
	}
	if err := r.Client.Delete(ctx, &bindingPolicy); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		log.Error(err, "evictJobByBindingPolicyDelete() ---- Error deleting BindingPolicy object ")
		return err
	}

	log.Info("Deleted BindingPolicy ----", "", bindingPolicyName(meta))
	return nil
}
func EvictJobByBindingPolicyDelete(ctx context.Context, client client.Client, wl *kueue.Workload) error {
	log := log.FromContext(ctx)
	log.V(4).Info("EvictJobByBindingPolicyDelete() ----")
	wlOwners := wl.GetObjectMeta().GetOwnerReferences()

	meta := &metav1.ObjectMeta{
		Name:      wlOwners[0].Name,
		Namespace: wl.Namespace,
	}
	namespacedName := types.NamespacedName{
		Name:      bindingPolicyName(meta),
		Namespace: wl.Namespace,
	}
	bindingPolicy := ksv1alpha1.BindingPolicy{}
	if err := client.Get(ctx, namespacedName, &bindingPolicy); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		log.Error(err, "EvictJobByBindingPolicyDelete() ---- Error Getting BindingPolicy object ")
		return err
	}
	if err := client.Delete(ctx, &bindingPolicy); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		log.Error(err, "EvictJobByBindingPolicyDelete() ---- Error deleting BindingPolicy object ")
		return err
	}

	log.Info("Deleted BindingPolicy ----", "", bindingPolicyName(meta))
	return nil
}
func (r *WorkloadReconciler) addEvictedCondition(ctx context.Context, wl *kueue.Workload) error {

	newCondition := metav1.Condition{
		Type:               kueue.WorkloadEvicted,
		Status:             metav1.ConditionTrue,
		LastTransitionTime: metav1.Now(),
		Reason:             kueue.WorkloadEvictedByPreemption,
		Message:            "Preempted to accommodate a higher priority Workload",
	}

	apimeta.SetStatusCondition(&wl.Status.Conditions, newCondition)
	return r.Client.Status().Update(ctx, wl)

}
func (r *WorkloadReconciler) getJobObject(ctx context.Context, meta metav1.Object) (*unstructured.Unstructured, *apimeta.RESTMapping, error) {
	log := log.FromContext(ctx)

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

func WorkloadKey(req ctrl.Request) string {
	return fmt.Sprintf("%s/%s", req.Namespace, req.Name)
}

func IsAdmitted(w *kueue.Workload) bool {
	return apimeta.IsStatusConditionTrue(w.Status.Conditions, kueue.WorkloadAdmitted)
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
		if apierrors.IsNotFound(err) {

			if meta.GetLabels() != nil && meta.GetLabels()[AssignedClusterLabel] != "" {

				clusterSelector := []metav1.LabelSelector{
					{
						MatchLabels: map[string]string{
							KsLabelLocationGroupKey: KsLabelLocationGroup,
							KsLabelClusterNameKey:   meta.GetLabels()[AssignedClusterLabel],
						},
					},
				}
				downSync := []ksv1alpha1.DownsyncPolicyClause{ //DownsyncObjectTestAndStatusCollection{
					{
						StatusCollection: &ksv1alpha1.StatusCollection{
							StatusCollectors: []string{
								"jobs-status-aggregator",
							},
						},
						DownsyncObjectTest: ksv1alpha1.DownsyncObjectTest{
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
						CreateOnly: true,
					},
				}

				bindingPolicy := ksv1alpha1.BindingPolicy{
					ObjectMeta: metav1.ObjectMeta{
						Name: bindingPolicyName(meta),
					},
					Spec: ksv1alpha1.BindingPolicySpec{
						ClusterSelectors: clusterSelector,
						Downsync:         downSync,
						// turn the flag on so that kubestellar updates status of the appwrapper
						WantSingletonReportedState: true,
					},
				}

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
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		For(&kueue.Workload{}).
		Complete(r)
}

func (r *WorkloadReconciler) getCombinedStatus(ctx context.Context, csName, csNamespace string) (*ksv1alpha1.CombinedStatus, error) {
	log := log.FromContext(ctx)
	list := &ksv1alpha1.CombinedStatusList{}
	labelSelector := labels.SelectorFromSet(labels.Set{
		"status.kubestellar.io/name": csName,
	})

	listOptions := client.ListOptions{
		Namespace:     csNamespace,
		LabelSelector: labelSelector,
	}

	err := r.Client.List(ctx, list, &listOptions)

	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("CombinedStatus resource not found, ignoring since it must have been deleted")
			return nil, nil
		}
		log.Error(err, "Failed to get CombinedStatus")
		return nil, err
	}

	if len(list.Items) > 0 {
		for _, cs := range list.Items {
			log.Info("getCombinedStatus found instance - returning")
			return &cs, nil
		}
	}

	return nil, nil

}
