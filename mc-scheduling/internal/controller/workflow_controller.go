/*
Copyright 2024.

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
	"strconv"
	"strings"

	wfv1 "github.com/argoproj/argo-workflows/v3/pkg/apis/workflow/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	sched "kubestellar/galaxy/mc-scheduling/pkg/scheduler"

	cmv1alpha1 "kubestellar/galaxy/clustermetrics/api/v1alpha1"
)

const (
	schedulingLabelKey = "kubestellar.io/cluster"
	hostingClusterName = "local"
)

// WorkflowReconciler reconciles a Workflow object
type WorkflowReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// PodSpecPatch is used to deserialize the data in the Workflow annotation for patching pod specs
type PodSpecPatch struct {
	Args      []string
	Command   []string
	Image     []string
	Resources map[string]interface{}
}

// ResourceReqs provides the resource requirements for a specified function
type NamedResourceRequirements struct {
	FunctionName string
	ResourceReqs corev1.ResourceRequirements
}

//+kubebuilder:rbac:groups=integrations.kubestellar.io,resources=clustermetrics,verbs=get;list;watch
//+kubebuilder:rbac:groups=integrations.kubestellar.io,resources=clustermetrics/status,verbs=get
//+kubebuilder:rbac:groups=argoproj.io,resources=workflows,verbs=get;list;watch;update;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Workflow object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.17.2/pkg/reconcile
func (r *WorkflowReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	var scheduler sched.MultiClusterScheduler

	logger.Info("reconciling workflow")
	workflow := &wfv1.Workflow{}
	err := r.Client.Get(ctx, req.NamespacedName, workflow, &client.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("workflow no longer exists", "workflow", req.NamespacedName.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// check if it has already been scheduled
	if cluster, ok := workflow.Labels[schedulingLabelKey]; ok {
		logger.Info("workflow already scheduled", "cluster", cluster)
		return ctrl.Result{}, nil
	}

	podSpecs, err := extractPodSpecList(workflow)
	if err != nil {
		return ctrl.Result{}, err
	}

	clusterMetricsList := &cmv1alpha1.ClusterMetricsList{}
	err = r.Client.List(ctx, clusterMetricsList, &client.ListOptions{})
	if err != nil {
		return ctrl.Result{}, err
	}

	// use the default scheduler
	scheduler = sched.NewDefaultScheduler()

	cluster := scheduler.SelectCluster(podSpecs, clusterMetricsList)

	fmt.Printf("Selected cluster: %s\n", cluster)

	if cluster == "" {
		msg := fmt.Sprintf("no cluster that fits workflow %s could be found", workflow.Name)
		return ctrl.Result{}, fmt.Errorf(msg)
	}

	logger.Info("setting target cluster in workflow object", "cluster", cluster, "workflow", workflow.Name)
	// retrieve the latest version to minimize potential conflicts
	err = r.Client.Get(ctx, req.NamespacedName, workflow, &client.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("workflow no longer exists", "workflow", req.NamespacedName.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	workflowToUpdate := workflow.DeepCopy()
	setTargetClusterInWorkFlow(workflowToUpdate, cluster)
	err = r.Client.Update(ctx, workflowToUpdate, &client.UpdateOptions{})
	return ctrl.Result{}, err
}

// SetupWithManager sets up the controller with the Manager.
func (r *WorkflowReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&wfv1.Workflow{}).
		Complete(r)
}

func extractPodSpecList(workflow *wfv1.Workflow) ([]*corev1.PodSpec, error) {
	podSpecList := []*corev1.PodSpec{}
	taskMap := map[string]string{}

	// find the entrypoint template and traverse from there
	entryTemplate := findTemplate(workflow.Spec.Entrypoint, workflow.Spec.Templates)

	if entryTemplate.DAG == nil {
		return nil, fmt.Errorf("could not find a DAG entrypoint template")
	}

	// process the DAG to find the container template for each task
	processDAG(entryTemplate.DAG, workflow.Spec.Templates, "", taskMap)

	// discover the Resources Patches (if any) by task
	resourcePatchesMap := discoverResourcePatches(workflow)

	// iterate all tasks and append container specs
	for task, templateName := range taskMap {
		template := findTemplate(templateName, workflow.Spec.Templates)
		if template.Container != nil {
			podSpec := &corev1.PodSpec{}
			podSpec.Containers = []corev1.Container{}
			container := template.Container.DeepCopy()
			// patch the resources if there is a patch for that task
			if resources, ok := resourcePatchesMap[task]; ok {
				container.Resources = *resources
			}
			podSpec.Containers = append(podSpec.Containers, *container)
			podSpecList = append(podSpecList, podSpec)
		}
	}
	return podSpecList, nil
}

func discoverResourcePatches(workflow *wfv1.Workflow) map[string]*corev1.ResourceRequirements {
	resourceRequirementsMap := map[string]*corev1.ResourceRequirements{}
	for k, v := range workflow.Annotations {
		if strings.Contains(k, "pipelines.kubeflow.org/implementations-comp-") {
			podSpecPatch := &PodSpecPatch{}
			json.Unmarshal([]byte(v), podSpecPatch)
			if podSpecPatch.Resources != nil {
				taskName := extractFunctionNameFromArgs(podSpecPatch.Args)
				resourceRequirements := corev1.ResourceRequirements{}
				resourceRequirements.Limits = make(corev1.ResourceList)
				resourceRequirements.Requests = make(corev1.ResourceList)
				if o, ok := podSpecPatch.Resources["cpuLimit"]; ok {
					if lim, ok := o.(float64); ok {
						cpu := int64(lim * 1000.0)
						resourceRequirements.Limits[corev1.ResourceCPU] = *resource.NewScaledQuantity(cpu, resource.Milli)
					}
				}
				if o, ok := podSpecPatch.Resources["cpuRequest"]; ok {
					if req, ok := o.(float64); ok {
						cpu := int64(req * 1000.0)
						resourceRequirements.Requests[corev1.ResourceCPU] = *resource.NewScaledQuantity(cpu, resource.Milli)
					}
				}
				if o, ok := podSpecPatch.Resources["memoryLimit"]; ok {
					if lim, ok := o.(float64); ok {
						mem := int64(lim * 1000000000.0)
						resourceRequirements.Limits[corev1.ResourceMemory] = *resource.NewScaledQuantity(mem, 0)
					}
				}
				if o, ok := podSpecPatch.Resources["memoryRequest"]; ok {
					if req, ok := o.(float64); ok {
						mem := int64(req * 1000000000.0)
						resourceRequirements.Requests[corev1.ResourceMemory] = *resource.NewScaledQuantity(mem, 0)
					}
				}
				if o, ok := podSpecPatch.Resources["accelerator"]; ok {
					if accelerator, ok := o.(map[string]interface{}); ok {
						if o, ok := accelerator["count"]; ok {
							if countStr, ok := o.(string); ok {
								count, err := strconv.ParseInt(countStr, 10, 64)
								if err == nil {
									if o, ok := accelerator["type"]; ok {
										if accType, ok := o.(string); ok {
											resourceRequirements.Requests[corev1.ResourceName(accType)] = *resource.NewScaledQuantity(count, 0)
										}
									}
								}
							}
						}
					}
				}
				resourceRequirementsMap[taskName] = &resourceRequirements
			}
		}
	}
	return resourceRequirementsMap
}

func extractFunctionNameFromArgs(args []string) string {
	for i, arg := range args {
		if arg == "--function_to_execute" && len(args) > i {
			return args[i+1]
		}
	}
	return ""
}

// processDAG returns a map of tasks and related container templates
func processDAG(dag *wfv1.DAGTemplate, templates []wfv1.Template, taskName string, taskMap map[string]string) {
	for _, task := range dag.Tasks {
		taskTemplate := findTemplate(task.Template, templates)
		if taskTemplate.Container != nil {
			if taskName != "" {
				taskMap[taskName] = taskTemplate.Name
			} else {
				taskMap[task.Name] = taskTemplate.Name
			}
		} else if taskTemplate.DAG != nil {
			taskName = task.Name
			processDAG(taskTemplate.DAG, templates, taskName, taskMap) // Recursively process nested DAG
		}
	}
}

func findTemplate(templateName string, templates []wfv1.Template) *wfv1.Template {
	for _, t := range templates {
		if t.Name == templateName {
			return &t
		}
	}
	return nil
}

// sets workflow to run on cluster selected by multicluster scheduler
func setTargetClusterInWorkFlow(workflow *wfv1.Workflow, cluster string) {
	// no cluster that could fit the workload was found, keep it unscheduled
	if cluster == "" {
		return
	}

	// set the label for cluster
	if workflow.Labels == nil {
		workflow.Labels = make(map[string]string)
	}
	workflow.Labels[schedulingLabelKey] = cluster

	// if local, un-suspend the workflow
	if cluster == hostingClusterName {
		workflow.Spec.Suspend = ptr.To(false)
	}
}
