package controller

import (
	"fmt"
	"time"

	"github.com/acorn-io/baaah/pkg/router"
	"github.com/sirupsen/logrus"
	"istio.io/api/security/v1beta1"
	typev1beta1 "istio.io/api/type/v1beta1"
	securityv1beta1 "istio.io/client-go/pkg/apis/security/v1beta1"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	injectionLabel            = "istio-injection"
	proxySidecarContainerName = "istio-proxy"

	acornAppNameLabel     = "acorn.io/app-name"
	acornProjectNameLabel = "acorn.io/app-namespace"
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

func PeerAuthenticationForApp(req router.Request, resp router.Response) error {
	appNamespace := req.Object.(*corev1.Namespace)

	// set entire app to mTLS strict mode by default
	// additional PeerAuthentications will be setup to set published ports to permissive mode
	peerAuth := securityv1beta1.PeerAuthentication{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-strict", appNamespace.Name),
			Namespace: appNamespace.Name,
		},
		Spec: v1beta1.PeerAuthentication{
			Mtls: &v1beta1.PeerAuthentication_MutualTLS{
				Mode: v1beta1.PeerAuthentication_MutualTLS_STRICT,
			},
		},
	}

	resp.Objects(&peerAuth)

	return nil
}

func PeerAuthenticationForIngress(req router.Request, resp router.Response) error {
	ingress := req.Object.(*netv1.Ingress)

	appName := ingress.Labels[acornAppNameLabel]
	projectName := ingress.Labels[acornProjectNameLabel]

	// create a mapping of k8s Service names to published port names/numbers
	svcNameToPorts := make(map[string][]netv1.ServiceBackendPort)
	for _, rule := range ingress.Spec.Rules {
		for _, path := range rule.HTTP.Paths {
			svcName := path.Backend.Service.Name
			port := path.Backend.Service.Port
			svcNameToPorts[svcName] = append(svcNameToPorts[svcName], port)
		}
	}

	for svcName, ports := range svcNameToPorts {
		// get the Service from k8s
		svc := corev1.Service{}
		err := req.Get(&svc, ingress.Namespace, svcName)
		if err != nil {
			// service doesn't exist yet, so retry in 3 seconds
			resp.RetryAfter(3 * time.Second)
		}

		peerAuthName := fmt.Sprintf("%s-%s-%s-%s", projectName, appName, ingress.Name, svcName)

		// find all published port numbers and set them to permissive mode
		var portsMTLS map[uint32]*v1beta1.PeerAuthentication_MutualTLS
		for _, port := range ports {
			// try to map this ingress port to a port on the service
			for _, svcPort := range svc.Spec.Ports {
				if (svcPort.Name != "" && svcPort.Name == port.Name) || svcPort.Port == port.Number {
					targetPort := svcPort.TargetPort
					portsMTLS[uint32(targetPort.IntVal)] = &v1beta1.PeerAuthentication_MutualTLS{
						Mode: v1beta1.PeerAuthentication_MutualTLS_PERMISSIVE,
					}
				}
			}
		}

		// create a permissive PeerAuthentication for pods targeted by the service
		peerAuth := securityv1beta1.PeerAuthentication{
			ObjectMeta: metav1.ObjectMeta{
				Name:      peerAuthName,
				Namespace: ingress.Namespace,
			},
			Spec: v1beta1.PeerAuthentication{
				Selector: &typev1beta1.WorkloadSelector{
					MatchLabels: svc.Spec.Selector,
				},
				PortLevelMtls: portsMTLS,
			},
		}

		resp.Objects(&peerAuth)
	}

	return nil
}
