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
	//	"encoding/json"
	"errors"
	"fmt"
	"time"

	metricsv1alpha1 "kubestellar/galaxy/clustermetrics/api/v1alpha1"
	scheduler "kubestellar/galaxy/mc-scheduling/pkg/scheduler"

	ksv1alpha1 "github.com/kubestellar/kubestellar/api/control/v1alpha1"
	//	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"

	//clientgo "k8s.io/client-go"

	//	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"

	//	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/clock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	//	"sigs.k8s.io/controller-runtime/pkg/event"
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
	clock                  clock.Clock
	CleanupWecOnCompletion bool
}

const (
	KsLabelLocationGroupKey = "location-group"
	KsLabelLocationGroup    = "edge"
	KsLabelClusterNameKey   = "name"
	AssignedClusterLabel    = "mcc.kubestellar.io/cluster"
	NodeSelectorAddedBy     = "workloadcontroller"
	NodeSelectorController  = "mcc.nodeselectororigin"
	ClusterAssigned         = "ClusterAssigned"
	Pending                 = "Pending"
)

//+kubebuilder:rbac:groups=kueue.x-k8s.io.galaxy.kubestellar.io,resources=workloads,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=kueue.x-k8s.io.galaxy.kubestellar.io,resources=workloads/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=kueue.x-k8s.io.galaxy.kubestellar.io,resources=workloads/finalizers,verbs=update

func NewWorkloadReconciler(c client.Client, kueueClient *kueueClient.Clientset, cfg *rest.Config, rm meta.RESTMapper, record record.EventRecorder) *WorkloadReconciler {
	return &WorkloadReconciler{
		Client:        c,
		RestMapper:    rm,
		DynamicClient: dynamic.NewForConfigOrDie(cfg),
		KueueClient:   kueueClient,
		Scheduler:     scheduler.NewDefaultScheduler(),
		Recorder:      record,
		clock:         clock.RealClock{},
	}
}

// Reconciles kueue Workload object and if quota exists it downsyncs a job to a worker cluster.

// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.12.1/pkg/reconcile
func (r *WorkloadReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	log.V(4).Info("Reconcile Workload -----------------")

	wl := &kueue.Workload{}
	if err := r.Client.Get(ctx, req.NamespacedName, wl); err != nil {
		log.Error(err, "Error when fetching Workload object ")
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	if match := r.RemoteFinishedCondition(*wl); match != nil {
		log.Info("workload finished - evicting from a remote cluster", "CleanupWecOnCompletion", r.CleanupWecOnCompletion)
		if r.CleanupWecOnCompletion {
			return reconcile.Result{}, r.evictJobByBindingPolicyDelete(ctx, wl)
		}
	}

	if workload.HasQuotaReservation(wl) {
		if apimeta.IsStatusConditionTrue(wl.Status.Conditions, TimedoutAwaitingPodsReady) {
			log.Info("............ Timed out awaiting pods to reach Ready", "Job", wl.Name, "Namespace", wl.Namespace)
			err := r.ReclaimQuota(ctx, wl, r.Client, kueue.WorkloadEvictedByPodsReadyTimeout, "Exceeded the PodsReady timeout ns")
			if err != nil {
				return ctrl.Result{}, err
			}
		}
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
		return ctrl.Result{}, err
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

		if !apimeta.IsStatusConditionTrue(wl.Status.Conditions, ClusterAssigned) { //string(kueue.CheckStateReady)) {
			err := retry.RetryOnConflict(retry.DefaultRetry, func() error {

				wl := &kueue.Workload{}
				if err := r.Client.Get(ctx, namespacedName, wl); err != nil {
					log.Error(err, "Error when fetching Workload object ")
					return err
				}

				log.Info("............ Got Quota Reservation", "Job", wl.Name, "Namespace", wl.Namespace)
				newCondition := metav1.Condition{
					Type:    ClusterAssigned, //string(kueue.CheckStateReady),
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
		} else {
			//log.Info("............ Got into the dead zone - nothing is done here")
			if r.jobPendingPodReadyTimeout(ctx, jobObject.GetName(), jobObject.GetNamespace()) {
				log.Info("............ Job Pods stuck in Pending state beyond allowed threshold - evicting job from the WEC")
			}
		}

	} else { // if !jobAssignedToCluster {
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

		meta := &metav1.ObjectMeta{
			Name:      jobObject.GetName(),
			Namespace: jobObject.GetNamespace(),
			Labels:    jobObject.GetLabels(),
		}

		if err = r.createBindingPolicy(ctx, meta); err != nil {
			log.Error(err, "Error creating BindingPolicy object ")
			return ctrl.Result{}, err
		}
		log.Info("New BindingPolicy created for object", "Name", meta.Name)
	}
	return ctrl.Result{}, nil
}

func (r *WorkloadReconciler) handleJobWithNoQuota(ctx context.Context, wl *kueue.Workload) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	log.Info("workload with no reservation")

	if err := r.evictJobByBindingPolicyDelete(ctx, wl); err != nil {
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
	//template["spec"].(map[string]interface{})["nodeSelector"] = map[string]string{"instance": string(flavor)}
	template["spec"].(map[string]interface{})["nodeSelector"] = map[string]string{"instance": "spot"}

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

/*
	setRequeued := evCond.Reason == kueue.WorkloadEvictedByPreemption || evCond.Reason == kueue.WorkloadEvictedByAdmissionCheck
	workload.SetRequeuedCondition(wl, evCond.Reason, evCond.Message, setRequeued)
	_ = workload.UnsetQuotaReservationWithCondition(wl, "Pending", evCond.Message)
	err := workload.ApplyAdmissionStatus(ctx, r.client, wl, true)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("clearing admission: %w", err)
	}

*/

func (r *WorkloadReconciler) ReclaimQuota(ctx context.Context, wl *kueue.Workload, cl client.Client, reason, message string) error {
	log := log.FromContext(ctx)
	log.Info("ReclaimQuota ----")

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
		/*
			// remove node selector if it was added by this controller
			if err := removeNodeSelector(jobObject); err != nil {
				log.Error(err, "Unable to remove nodeSelector from JobObject")
				return err
			}
		*/
		_, err = r.DynamicClient.Resource(mapping.Resource).Namespace(wl.Namespace).Update(ctx, jobObject, metav1.UpdateOptions{})
		if err != nil {
			log.Error(err, "Error when Updating object ")
			return err
		}
		log.Info("ReclaimQuota ---- Removed BP label")
	}

	//setRequeued := evCond.Reason == kueue.WorkloadEvictedByPreemption || evCond.Reason == kueue.WorkloadEvictedByAdmissionCheck
	//workload.UnsetQuotaReservationWithCondition(wl, reason, message)
	_ = workload.UnsetQuotaReservationWithCondition(wl, kueue.WorkloadEvictedByPodsReadyTimeout, message)
	err = workload.ApplyAdmissionStatus(ctx, cl, wl, true)
	if err != nil {
		return fmt.Errorf("clearing admission: %w", err)
	}
	relevantChecks, err := admissioncheck.FilterForController(ctx, cl, wl.Status.AdmissionChecks, ControllerName)
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
		wlPatch := workload.BaseSSAWorkload(wl)
		workload.SetAdmissionCheckState(&wlPatch.Status.AdmissionChecks, *acs)
		err = cl.Status().Patch(ctx, wlPatch, client.Apply, client.FieldOwner(ControllerName), client.ForceOwnership)
		if err != nil {
			return err
		}
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
		//	newWl.Status.Conditions = []metav1.Condition{}

		if existing := apimeta.FindStatusCondition(newWl.Status.Conditions, Pending); existing != nil {
			apimeta.RemoveStatusCondition(&newWl.Status.Conditions, Pending)
		}
		if existing := apimeta.FindStatusCondition(newWl.Status.Conditions, ClusterAssigned); existing != nil {
			apimeta.RemoveStatusCondition(&newWl.Status.Conditions, ClusterAssigned)
		}
		if existing := apimeta.FindStatusCondition(newWl.Status.Conditions, kueue.WorkloadQuotaReserved); existing != nil {
			apimeta.RemoveStatusCondition(&newWl.Status.Conditions, kueue.WorkloadQuotaReserved)
		}
		/*
			newCondition := metav1.Condition{
				Type:    kueue.WorkloadEvicted,
				Status:  metav1.ConditionFalse,
				Reason:  kueue.WorkloadEvictedByPodsReadyTimeout,
				Message: "Exceeded the PodsReady timeout ns",
			}
			apimeta.SetStatusCondition(&newWl.Status.Conditions, newCondition)
		*/
		err = cl.Status().Update(ctx, newWl)
		if err != nil {
			//	log.Error(err, "Error when Updating status of the workload object ")
			return err
		}
		return nil
	})

	if err != nil {
		return err
	}

	return nil
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

	//	ruh := &resourceUpdatesHandler{r: r}

	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		For(&kueue.Workload{}).
		//		Watches(&ksv1alpha1.CombinedStatus{}, ruh).
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

	// Perform reconciliation for CombinedStatus

	if len(list.Items) > 0 {
		for _, cs := range list.Items {
			log.Info("getCombinedStatus found instance - returning")
			return &cs, nil
		}
	}

	return nil, nil

}

/*
func (r *WorkloadReconciler) CombinedStatusCreatefunc(ctx context.Context, e event.TypedCreateEvent[client.Object], rli workqueue.RateLimitingInterface) {
	log := log.FromContext(ctx)
	log.Info("CombinedStatusCreatefunc() ----")

	cs := e.Object.(*ksv1alpha1.CombinedStatus)
	r.status(ctx, cs)
}
func (r *WorkloadReconciler) CombinedStatusUpdatefunc(ctx context.Context, e event.TypedUpdateEvent[client.Object], rli workqueue.RateLimitingInterface) {
	log := log.FromContext(ctx)
	log.Info("CombinedStatusUpdatefunc() ----")
	cs := e.ObjectNew.(*ksv1alpha1.CombinedStatus)
	r.status(ctx, cs)
}

func (r *WorkloadReconciler) status(ctx context.Context, cs *ksv1alpha1.CombinedStatus) {
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

					jobName, found := cs.Labels["status.kubestellar.io/name"]
					if !found {
						log.Info("CombinedStatus status.kubestellar.io/name label is missing", "combinedstatus name", cs.Name)
						return
					}
					jobNamespace, found := cs.Labels["status.kubestellar.io/namespace"]
					if !found {
						log.Info("CombinedStatus status.kubestellar.io/name label is missing", "combinedstatus name", cs.Name)
						return
					}
					wl, err := r.fetchKueueWorkloadForJob(ctx, jobNamespace, jobName)
					if err != nil {
						log.Error(err, "Unable to fetch Workload object")
						return
					}
					log.Info("Got Workload Object", "Name", wl.GetName())
					if statusObject.Active > 0 && *statusObject.Ready < statusObject.Active {
						log.Info("+++ Job pods are in Pending state")

						if !apimeta.IsStatusConditionTrue(wl.Status.Conditions, string(kueue.CheckStatePending)) {

							log.Info("............ Adding Pending condition", "Job", wl.Name, "Namespace", wl.Namespace)
							newCondition := metav1.Condition{
								Type:    string(kueue.CheckStatePending),
								Status:  metav1.ConditionTrue,
								Reason:  "JobStatus",
								Message: fmt.Sprintf("Job pods Pending on Work Cluster %q", *cs.Results[0].Rows[0].Columns[0].String),
							}
							apimeta.SetStatusCondition(&wl.Status.Conditions, newCondition)

							err := r.Client.Status().Update(ctx, wl)
							if err != nil {
								log.Error(err, "Unable to update Workload object")
								return
							}
						} else if apimeta.IsStatusConditionTrue(wl.Status.Conditions, "ClusterAssigned") {
							condition := apimeta.FindStatusCondition(wl.Status.Conditions, "ClusterAssigned")
							elapsedTime := r.clock.Since(condition.LastTransitionTime.Time)
							if elapsedTime.Minutes() > 1 {
								log.Info("Exceeded time threshold while waiting for pods Ready on remote cluster")
							}
						}

					} else if statusObject.Active > 0 && *statusObject.Ready == statusObject.Active {
						log.Info("............ Got Quota Reservation", "Job", wl.Name, "Namespace", wl.Namespace)
						if !apimeta.IsStatusConditionTrue(wl.Status.Conditions, string(kueue.CheckStatePending)) {
							newCondition := metav1.Condition{
								Type:    string(kueue.CheckStateReady),
								Status:  metav1.ConditionTrue,
								Reason:  "JobStatus",
								Message: fmt.Sprintf("Job Pods are Ready on the Work Cluster"),
							}
							apimeta.SetStatusCondition(&wl.Status.Conditions, newCondition)
							err := r.Client.Status().Update(ctx, wl)
							if err != nil {
								log.Error(err, "Unable to update Workload object")
								return
							}
						}

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

type resourceUpdatesHandler struct {
	r *WorkloadReconciler
}

func (h *resourceUpdatesHandler) Generic(_ context.Context, _ event.GenericEvent, _ workqueue.RateLimitingInterface) {
}

func (h *resourceUpdatesHandler) Create(ctx context.Context, e event.CreateEvent, q workqueue.RateLimitingInterface) {
	log := log.FromContext(ctx)
	log.Info("Create event")
	h.handle(ctx, e.Object, q)

}

func (h *resourceUpdatesHandler) Update(ctx context.Context, e event.UpdateEvent, q workqueue.RateLimitingInterface) {
	log := log.FromContext(ctx)
	log.Info("Update event")
	h.handle(ctx, e.ObjectNew, q)
}

func (h *resourceUpdatesHandler) Delete(ctx context.Context, e event.DeleteEvent, q workqueue.RateLimitingInterface) {
	log := log.FromContext(ctx)
	log.Info("Delete event")
	h.handle(ctx, e.Object, q)
}
func (h *resourceUpdatesHandler) handle(ctx context.Context, obj client.Object, q workqueue.RateLimitingInterface) {
	cs := obj.(*ksv1alpha1.CombinedStatus)
	log := log.FromContext(ctx)
	namespacedName := types.NamespacedName{
		Name:      cs.Name,
		Namespace: cs.Namespace,
	}

	if err := h.r.Client.Get(ctx, namespacedName, cs); err != nil {
		log.Error(err, "Error when fetching CombinedStatus object ")
		return
	}
	log.Info("handle() ", "CombinedStatus Fetch", "Success")
	h.r.status(ctx, cs)
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      cs.Name,
			Namespace: cs.Namespace,
		},
	}
	q.Add(req)
}

// Inside your controller's Reconcile function or a relevant handler
func (r *WorkloadReconciler) fetchKueueWorkloadForJob(ctx context.Context, jobNamespace, jobName string) (*kueue.Workload, error) {
	log := log.FromContext(ctx)

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
*/
