package controller

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/acorn-io/baaah/pkg/name"
	"github.com/acorn-io/baaah/pkg/router"
	"github.com/sirupsen/logrus"
	networkingapiv1beta1 "istio.io/api/networking/v1beta1"
	"istio.io/api/security/v1beta1"
	typev1beta1 "istio.io/api/type/v1beta1"
	networkingv1beta1 "istio.io/client-go/pkg/apis/networking/v1beta1"
	securityv1beta1 "istio.io/client-go/pkg/apis/security/v1beta1"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	apierror "k8s.io/apimachinery/pkg/api/errors"
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

// PoliciesForApp creates an Istio PeerAuthentication in each app's namespace.
// The PeerAuthentication sets mTLS to STRICT mode, meaning that all pods in the namespace will only
// accept incoming network traffic from other pods in the Istio mesh.
func (h Handler) PoliciesForApp(req router.Request, resp router.Response) error {
	appNamespace := req.Object.(*corev1.Namespace)

	// Create the PeerAuthentication to set entire app to mTLS STRICT mode by default
	peerAuth := securityv1beta1.PeerAuthentication{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name.SafeConcatName(appNamespace.Name, "strict"),
			Namespace: appNamespace.Name,
			Labels: map[string]string{
				acornManagedLabel: "true",
			},
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

// PoliciesForIngress creates Istio an PeerAuthentication for each Ingress resource
// created by Acorn. The PeerAuthentication sets mTLS to PERMISSIVE mode on the ports exposed by the
// Ingresses so that the containers will accept traffic coming from outside the Istio mesh.
func PoliciesForIngress(req router.Request, resp router.Response) error {
	ingress := req.Object.(*netv1.Ingress)

	// Don't process the Ingress resource created for Acorn DNS, since it doesn't refer to any pods
	if ingress.Name == systemIngress && ingress.Namespace == systemNamespace {
		return nil
	}

	appName := ingress.Labels[acornAppNameLabel]
	projectName := ingress.Labels[acornProjectNameLabel]

	// Create a mapping of k8s Service names to published port names/numbers
	svcNameToPorts := make(map[string][]netv1.ServiceBackendPort)
	for _, rule := range ingress.Spec.Rules {
		for _, path := range rule.HTTP.Paths {
			svcName := path.Backend.Service.Name
			port := path.Backend.Service.Port
			svcNameToPorts[svcName] = append(svcNameToPorts[svcName], port)
		}
	}

	for svcName, ports := range svcNameToPorts {
		// Get the Service from k8s
		svc := corev1.Service{}
		err := req.Get(&svc, ingress.Namespace, svcName)
		if err != nil {
			// service doesn't exist yet, so retry in 3 seconds
			resp.RetryAfter(3 * time.Second)
		}

		// This service is either a normal ClusterIP service or an ExternalName service which
		// points to a service in a different namespace (if there are Acorn links involved).
		// If it's an ExternalName, we need to get the service to which it points.
		if svc.Spec.Type == corev1.ServiceTypeExternalName {
			externalName := svc.Spec.ExternalName

			// The ExternalName is in the format <service name>.<namespace>.svc.<cluster domain>
			svcName, rest, ok := strings.Cut(externalName, ".")
			if !ok {
				return fmt.Errorf("failed to parse ExternalName '%s' of svc '%s'", externalName, svc.Name)
			}
			svcNamespace, _, ok := strings.Cut(rest, ".")
			if !ok {
				return fmt.Errorf("failed to parse ExternalName '%s' of svc '%s'", externalName, svc.Name)
			}

			svc = corev1.Service{}
			if err = req.Get(&svc, svcNamespace, svcName); err != nil {
				if apierror.IsNotFound(err) {
					return fmt.Errorf("failed to find service '%s', targeted by ExternalName '%s'", svcName, externalName)
				}
				return err
			}
		}

		policyName := name.SafeConcatName(projectName, appName, ingress.Name, svcName)

		// Find all published port numbers
		portsMTLS := make(map[uint32]*v1beta1.PeerAuthentication_MutualTLS, len(ports))
		var portNums []string
		for _, port := range ports {
			// Try to map this ingress port to a port on the service
			for _, svcPort := range svc.Spec.Ports {
				if (svcPort.Name != "" && svcPort.Name == port.Name) || svcPort.Port == port.Number {
					targetPort := svcPort.TargetPort
					portsMTLS[uint32(targetPort.IntVal)] = &v1beta1.PeerAuthentication_MutualTLS{
						Mode: v1beta1.PeerAuthentication_MutualTLS_PERMISSIVE,
					}
					portNums = append(portNums, strconv.Itoa(int(targetPort.IntVal)))
				}
			}
		}

		// Create a permissive PeerAuthentication for pods targeted by the service
		peerAuth := securityv1beta1.PeerAuthentication{
			ObjectMeta: metav1.ObjectMeta{
				Name:      policyName,
				Namespace: svc.Namespace,
				Labels: map[string]string{
					acornManagedLabel: "true",
				},
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

// PoliciesForService creates an Istio PeerAuthentication for each LoadBalancer Service
// created by Acorn. The PeerAuthentication sets mTLS to PERMISSIVE mode on the ports targeted by the Service
// so that the containers will accept traffic coming from outside the Istio mesh.
func PoliciesForService(req router.Request, resp router.Response) error {
	service := req.Object.(*corev1.Service)

	// We only care about LoadBalancer services that were created for published TCP/UDP ports
	if service.Spec.Type != corev1.ServiceTypeLoadBalancer {
		return nil
	}

	appName := service.Labels[acornAppNameLabel]
	projectName := service.Labels[acornProjectNameLabel]
	containerName := service.Labels[acornContainerNameLabel]

	portsMTLS := make(map[uint32]*v1beta1.PeerAuthentication_MutualTLS, len(service.Spec.Ports))
	var portNums []string
	for _, port := range service.Spec.Ports {
		portsMTLS[uint32(port.TargetPort.IntVal)] = &v1beta1.PeerAuthentication_MutualTLS{
			Mode: v1beta1.PeerAuthentication_MutualTLS_PERMISSIVE,
		}
		portNums = append(portNums, strconv.Itoa(int(port.TargetPort.IntVal)))
	}

	policyName := name.SafeConcatName(projectName, appName, service.Name, containerName)

	// Create a permissive PeerAuthentication for pods targeted by the service
	peerAuth := securityv1beta1.PeerAuthentication{
		ObjectMeta: metav1.ObjectMeta{
			Name:      policyName,
			Namespace: service.Namespace,
			Labels: map[string]string{
				acornManagedLabel: "true",
			},
		},
		Spec: v1beta1.PeerAuthentication{
			Selector: &typev1beta1.WorkloadSelector{
				MatchLabels: service.Spec.Selector,
			},
			PortLevelMtls: portsMTLS,
		},
	}

	resp.Objects(&peerAuth)
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
