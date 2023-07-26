package controller

import (
	"github.com/acorn-io/baaah/pkg/router"
	"github.com/sirupsen/logrus"
	networkingapiv1beta1 "istio.io/api/networking/v1beta1"
	networkingv1beta1 "istio.io/client-go/pkg/apis/networking/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	systemIngress   = "acorn-dns-ingress"
	systemNamespace = "acorn-system"

	injectionLabel            = "istio-injection"
	proxySidecarContainerName = "istio-proxy"

	acornAppNameLabel       = "acorn.io/app-name"
	acornProjectNameLabel   = "acorn.io/app-namespace"
	acornContainerNameLabel = "acorn.io/container-name"
	acornManagedLabel       = "acorn.io/managed"
)

type Handler struct {
	client                     kubernetes.Interface
	debugImage                 string
	allowTrafficFromNamespaces string
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

func (h Handler) PoliciesForApp(req router.Request, resp router.Response) error {
	return nil
}

func PoliciesForIngress(req router.Request, resp router.Response) error {
	return nil
}

func PoliciesForService(req router.Request, resp router.Response) error {
	return nil
}

// VirtualServiceForLink creates an Istio VirtualService for each link between Acorn apps.
// This is in order to make mTLS work between workloads across namespaces.
func VirtualServiceForLink(req router.Request, resp router.Response) error {
	service := req.Object.(*corev1.Service)

	// The link label shouldn't be present on any non-ExternalName type Services, but check anyway
	if service.Spec.Type != corev1.ServiceTypeExternalName || len(service.Spec.Ports) == 0 {
		return nil
	}

	virtualService := networkingv1beta1.VirtualService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      service.Name,
			Namespace: service.Namespace,
			Labels: map[string]string{
				acornManagedLabel: "true",
			},
		},
		Spec: networkingapiv1beta1.VirtualService{
			Hosts: []string{service.Name},
			Http: []*networkingapiv1beta1.HTTPRoute{{
				Route: []*networkingapiv1beta1.HTTPRouteDestination{{
					Destination: &networkingapiv1beta1.Destination{
						Host: service.Spec.ExternalName,
						Port: &networkingapiv1beta1.PortSelector{
							Number: uint32(service.Spec.Ports[0].TargetPort.IntVal),
						},
					},
				}},
			}},
		},
	}

	resp.Objects(&virtualService)

	return nil
}

func DoNothing(req router.Request, resp router.Response) error {
	return nil
}
