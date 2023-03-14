package controller

import (
	"github.com/acorn-io/baaah/pkg/router"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	injectionLabel            = "istio-injection"
	proxySidecarContainerName = "istio-proxy"
)

type Handler struct {
	client     kubernetes.Interface
	debugImage string
}

// AddLabels adds the "istio-injection: enabled" label on every Acorn project namespace
func AddLabels(req router.Request, resp router.Response) error {
	projectNamespace := req.Object.(*corev1.Namespace)

	if projectNamespace.Labels == nil {
		projectNamespace.Labels = map[string]string{}
	}

	if projectNamespace.Labels[injectionLabel] == "enabled" {
		return nil
	}

	logrus.Infof("Updating project %v to add istio-injection label", projectNamespace.Name)
	projectNamespace.Labels[injectionLabel] = "enabled"
	if err := req.Client.Update(req.Ctx, projectNamespace); err != nil {
		return err
	}

	return nil
}

// KillIstioSidecar kills the Istio sidecar on every pod that corresponds to an Acorn job, once the job is complete
func (h Handler) KillIstioSidecar(req router.Request, resp router.Response) error {
	pod := req.Object.(*corev1.Pod)

	if _, ok := pod.Labels["acorn.io/job-name"]; !ok {
		return nil // pod doesn't belong to the job, so skip it
	}

	foundSidecar := false
	for _, containerStatus := range pod.Status.ContainerStatuses {
		if containerStatus.Name != proxySidecarContainerName && containerStatus.State.Terminated == nil {
			return nil
		}
		if containerStatus.Name == proxySidecarContainerName {
			foundSidecar = true
		}
	}

	if !foundSidecar {
		return nil
	}

	// If pod is already configured with ephemeral container, skip
	if len(pod.Spec.EphemeralContainers) > 0 {
		return nil
	}

	logrus.Infof("Launching ephemeral container to kill pod %v/%v sidecar", pod.Namespace, pod.Name)
	pod.Spec.EphemeralContainers = append(pod.Spec.EphemeralContainers, corev1.EphemeralContainer{
		TargetContainerName: proxySidecarContainerName,
		EphemeralContainerCommon: corev1.EphemeralContainerCommon{
			Name:            "shutdown-sidecar",
			Image:           h.debugImage,
			ImagePullPolicy: corev1.PullAlways,
			Command: []string{
				"curl", "-X", "POST", "http://localhost:15000/quitquitquit",
			},
		},
	})
	if _, err := h.client.CoreV1().Pods(pod.Namespace).UpdateEphemeralContainers(req.Ctx, pod.Name, pod, metav1.UpdateOptions{}); err != nil {
		return err
	}

	return nil
}
