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
	"fmt"
	"path/filepath"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/argoproj/argo-workflows/v3/pkg/apis/workflow/v1alpha1"
)

const (
	schedulingLabelKey       = "kubestellar.io/cluster"
	hostingClusterName       = "local"
	LokiInstallTypeOpenShift = "openshift"
	LokiInstallTypeDev       = "dev"
	certsVolume              = "certs"
	certsMountPath           = "/certs"
)

// WorkflowReconciler reconciles a Workflow object
type WorkflowReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	PodImage        string
	LokiInstallType string
	CertsSecretName string
}

// PodInfo is used to hold the relevant info for each pod in the workflow status
type PodInfo struct {
	Name      string
	Namespace string
	HostName  string
	Phase     v1alpha1.NodePhase
}

//+kubebuilder:rbac:groups=argoproj.io,resources=workflows,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;update;patch

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

	logger.Info("reconciling workflow")
	workflow := &v1alpha1.Workflow{}
	err := r.Client.Get(ctx, req.NamespacedName, workflow, &client.GetOptions{})
	if err != nil {
		logger.Info("workflow has been deleted")
		return ctrl.Result{}, nil
	}

	if !IsScheduledOnRemoteCluster(workflow) {
		logger.Info("local workflow, nothing to be done.")
		return ctrl.Result{}, nil
	}

	// extract the pod info from the workflow
	podInfoList := getPodsList(workflow)

	// iterate over all pods and create shadow pods
	for _, podInfo := range podInfoList {
		podExists, err := checkPodExists(r.Client, ctx, podInfo)
		if err != nil {
			logger.Error(err, "failed to check if Pod exists", "podInfo", podInfo)
			return ctrl.Result{}, err
		}

		if podExists {
			logger.Info("pod exists", "pod", podInfo.Name, "phase", podInfo.Phase)

			if isWorkflowCompletedOrError(workflow.Status.Phase) {
				// For pods that have completed the run, logs are archived in S3,
				// so there is no need to keep the pods around.
				logger.Info("deleting shadow pod", "pod", podInfo.Name, "phase", podInfo.Phase)
				if err := r.Client.Delete(ctx, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: podInfo.Name, Namespace: podInfo.Namespace}}, &client.DeleteOptions{}); err != nil {
					logger.Error(err, "failed to delete pod", "pod", podInfo.Name)
					return ctrl.Result{}, err
				}
			}

			continue
		}

		if !isWorkflowCompletedOrError(workflow.Status.Phase) {
			pod := generatePodTemplate(podInfo, r.PodImage, r.LokiInstallType, r.CertsSecretName)
			if err := controllerutil.SetControllerReference(workflow, pod, r.Scheme); err != nil {
				return ctrl.Result{}, err
			}

			addPodLabels(pod, workflow)

			logger.Info("creating pod", "pod", podInfo.Name, "namespace", podInfo.Namespace)
			if err := r.Client.Create(ctx, pod, &client.CreateOptions{}); err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *WorkflowReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Workflow{}).
		Complete(r)
}

func getPodsList(workflow *v1alpha1.Workflow) []PodInfo {
	podInfoList := []PodInfo{}

	for _, node := range workflow.Status.Nodes {
		// create shadow pods only once they have been assigned to a node
		if node.Type == v1alpha1.NodeTypePod && node.HostNodeName != "" {
			podName := getPodName(node)
			if podName == "" {
				continue
			}
			pInfo := PodInfo{
				Name:      podName,
				Namespace: workflow.Namespace,
				HostName:  node.HostNodeName,
				Phase:     node.Phase,
			}
			podInfoList = append(podInfoList, pInfo)
		}
	}
	return podInfoList
}

// In KFP 2.2 the correct pod name needs to be composed from node.id and node.templateName
func getPodName(node v1alpha1.NodeStatus) string {
	// example:
	// given node.id: tutorial-data-passing-d7bcd-2065044012
	// given node.templateName: system-dag-driver
	// name of pod is tutorial-data-passing-d7bcd-system-dag-driver-2065044012
	prefix, suffix := splitString(node.ID)
	return fmt.Sprintf("%s-%s-%s", prefix, node.TemplateName, suffix)
}

// splitString splits a given string into two parts: all segments except the last, and the last segment.
func splitString(input string) (firstPart string, lastPart string) {
	segments := strings.Split(input, "-")
	if len(segments) > 0 {
		lastPart = segments[len(segments)-1]
		firstPart = strings.Join(segments[:len(segments)-1], "-")
	}
	return firstPart, lastPart
}

func generatePodTemplate(podInfo PodInfo, image, lokiInstallType, CertsSecretName string) *corev1.Pod {
	podTemplate := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podInfo.Name,
			Namespace: podInfo.Namespace,
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:  "main",
					Image: image,
					Env: []corev1.EnvVar{
						{
							Name:  "POD_NAMESPACE",
							Value: podInfo.Namespace,
						},
						{
							Name:  "POD_NAME",
							Value: podInfo.Name,
						},
						{
							Name:  "HOST_NAME",
							Value: podInfo.HostName,
						},
					},
				},
			},
		},
	}
	if lokiInstallType == LokiInstallTypeOpenShift {
		podTemplate.Spec.Volumes = []corev1.Volume{
			{
				Name: certsVolume,
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: CertsSecretName,
					},
				},
			},
		}
		podTemplate.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{
			{
				Name:      certsVolume,
				ReadOnly:  true,
				MountPath: certsMountPath,
			},
		}
		extraEnv := []corev1.EnvVar{
			{
				Name:  "LOKI_INSTALL_TYPE",
				Value: LokiInstallTypeOpenShift,
			},
			{
				Name:  "TLS_CERT_FILE",
				Value: filepath.Join(certsMountPath, "tls.crt"),
			},
			{
				Name:  "TLS_KEY_FILE",
				Value: filepath.Join(certsMountPath, "tls.key"),
			},
		}
		podTemplate.Spec.Containers[0].Env = append(podTemplate.Spec.Containers[0].Env, extraEnv...)

		podTemplate.Spec.Containers[0].SecurityContext = &corev1.SecurityContext{
			AllowPrivilegeEscalation: ptr.To(false),
			RunAsNonRoot:             ptr.To(true),
			SeccompProfile: &corev1.SeccompProfile{
				Type: corev1.SeccompProfileTypeRuntimeDefault,
			},
		}
	}
	return podTemplate
}

func checkPodExists(kClient client.Client, ctx context.Context, podInfo PodInfo) (bool, error) {
	err := kClient.Get(ctx, types.NamespacedName{Namespace: podInfo.Namespace, Name: podInfo.Name}, &corev1.Pod{}, &client.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func IsScheduledOnRemoteCluster(workflow *v1alpha1.Workflow) bool {
	if v, ok := workflow.Labels[schedulingLabelKey]; ok {
		if v != hostingClusterName {
			return true
		}
	}
	return false
}

func addPodLabels(pod *corev1.Pod, workflow *v1alpha1.Workflow) {
	if pod.Labels == nil {
		pod.Labels = make(map[string]string)
	}
	runid, ok := workflow.Labels["pipeline/runid"]
	if ok {
		pod.Labels["pipeline/runid"] = runid
	}
}

func isWorkflowCompletedOrError(phase v1alpha1.WorkflowPhase) bool {
	return phase == v1alpha1.WorkflowSucceeded || phase == v1alpha1.WorkflowFailed
}
