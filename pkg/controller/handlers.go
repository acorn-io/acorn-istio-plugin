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
	"k8s.io/utils/strings/slices"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
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

// PoliciesForApp creates an Istio PeerAuthentication and AuthorizationPolicy in each app's namespace.
// The PeerAuthentication sets mTLS to STRICT mode, meaning that all pods in the namespace will only
// accept incoming network traffic from other pods in the Istio mesh. The AuthorizationPolicy specifies that
// the only traffic allowed into the pods must come from other Acorn apps in the same project, or one of the
// namespaces specified by --allow-traffic-from-namespaces.
func (h Handler) PoliciesForApp(req router.Request, resp router.Response) error {
	appNamespace := req.Object.(*corev1.Namespace)
	projectName := appNamespace.Labels[acornProjectNameLabel]

	// create the PeerAuthentication to set entire app to mTLS STRICT mode by default
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

	// next, create the AuthorizationPolicy
	// allow traffic from the other namespaces that belong to the same project
	otherNamespaces := corev1.NamespaceList{}
	if err := req.Client.List(req.Ctx, &otherNamespaces, client.MatchingLabels{
		acornProjectNameLabel: projectName,
	}); err != nil {
		return fmt.Errorf("failed to list namespaces: %w", err)
	}

	var allowedNamespaces []string
	// also allow traffic from any namespaces that were specified in --allow-traffic-from-namespaces
	if h.allowTrafficFromNamespaces != "" {
		allowedNamespaces = strings.Split(h.allowTrafficFromNamespaces, ",")
	}
	for _, namespace := range otherNamespaces.Items {
		allowedNamespaces = append(allowedNamespaces, namespace.Name)
	}

	authPolicy := securityv1beta1.AuthorizationPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name.SafeConcatName(appNamespace.Name, "authorization"),
			Namespace: appNamespace.Name,
			Labels: map[string]string{
				acornManagedLabel: "true",
			},
		},
		Spec: v1beta1.AuthorizationPolicy{
			Rules: []*v1beta1.Rule{{
				From: []*v1beta1.Rule_From{{
					Source: &v1beta1.Source{
						Namespaces: allowedNamespaces,
					}},
				}},
			},
			Action: v1beta1.AuthorizationPolicy_ALLOW,
		},
	}

	resp.Objects(&authPolicy)

	return nil
}

// PoliciesForIngress creates Istio PeerAuthentication and AuthorizationPolicy resources for each Ingress resource
// created by Acorn. The PeerAuthentications set mTLS to PERMISSIVE mode on the ports exposed by the
// Ingresses so that the containers will accept traffic coming from outside the Istio mesh.
// The AuthorizationPolicies allow traffic from any pod in the cluster (that way, the ingress controller can
// proxy traffic to it). This doesn't block incoming network traffic from a different Acorn project in the same
// cluster, but Acorn's built-in NetworkPolicies already take care of that.
func PoliciesForIngress(req router.Request, resp router.Response) error {
	// check if this is being called as a finalizer for a deleted Ingress
	// we need this because we sometimes create policies in different namespaces from where their owning Ingresses live
	if !req.Object.GetDeletionTimestamp().IsZero() {
		return nil
	}

	ingress := req.Object.(*netv1.Ingress)

	appName := ingress.Labels[acornAppNameLabel]
	projectName := ingress.Labels[acornProjectNameLabel]

	// first, determine the pod IP address ranges from the nodes
	// this information will be used later in the creation of the AuthorizationPolicy
	var podCIDRs []string
	nodes := corev1.NodeList{}
	if err := req.List(&nodes, &client.ListOptions{}); err != nil {
		return fmt.Errorf("failed to list nodes: %w", err)
	}
	for _, node := range nodes.Items {
		for _, cidr := range node.Spec.PodCIDRs {
			if !slices.Contains(podCIDRs, cidr) {
				podCIDRs = append(podCIDRs, cidr)
			}
		}
	}

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

		// This service is either a normal ClusterIP service or an ExternalName service which
		// points to a service in a different namespace (if there are Acorn links involved).
		// If it's an ExternalName, we need to get the service to which it points.
		if svc.Spec.Type == corev1.ServiceTypeExternalName {
			externalName := svc.Spec.ExternalName

			// the ExternalName is in the format <service name>.<namespace>.svc.<cluster domain>
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

		// find all published port numbers
		portsMTLS := make(map[uint32]*v1beta1.PeerAuthentication_MutualTLS, len(ports))
		var portNums []string
		for _, port := range ports {
			// try to map this ingress port to a port on the service
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

		// create a permissive PeerAuthentication for pods targeted by the service
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

		// create an AuthorizationPolicy to allow traffic from all pods
		authPolicy := securityv1beta1.AuthorizationPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      policyName,
				Namespace: svc.Namespace,
				Labels: map[string]string{
					acornManagedLabel: "true",
				},
			},
			Spec: v1beta1.AuthorizationPolicy{
				Selector: &typev1beta1.WorkloadSelector{
					MatchLabels: svc.Spec.Selector,
				},
				Rules: []*v1beta1.Rule{{
					From: []*v1beta1.Rule_From{{
						Source: &v1beta1.Source{
							IpBlocks: podCIDRs,
						}},
					},
					To: []*v1beta1.Rule_To{{
						Operation: &v1beta1.Operation{
							Ports: portNums,
						}},
					}},
				},
				Action: v1beta1.AuthorizationPolicy_ALLOW,
			},
		}

		resp.Objects(&authPolicy)
	}

	return nil
}

// PoliciesForService creates an Istio PeerAuthentication and AuthorizationPolicy for each LoadBalancer Service
// created by Acorn. The PeerAuthentication sets mTLS to PERMISSIVE mode on the ports targeted by the Service
// so that the containers will accept traffic coming from outside the Istio mesh. The AuthorizationPolicy will
// allow connections from any IP address, inside or outside the cluster. This does not block incoming network
// traffic from another Acorn project in the cluster, but Acorn's built-in NetworkPolicies already take care of that.
func PoliciesForService(req router.Request, resp router.Response) error {
	service := req.Object.(*corev1.Service)

	// we only care about LoadBalancer services that were created for published TCP/UDP ports
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

	// create a permissive PeerAuthentication for pods targeted by the service
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

	// create an AuthorizationPolicy to allow traffic from anywhere to the published ports
	authPolicy := securityv1beta1.AuthorizationPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      policyName,
			Namespace: service.Namespace,
			Labels: map[string]string{
				acornManagedLabel: "true",
			},
		},
		Spec: v1beta1.AuthorizationPolicy{
			Selector: &typev1beta1.WorkloadSelector{
				MatchLabels: service.Spec.Selector,
			},
			Rules: []*v1beta1.Rule{{
				From: []*v1beta1.Rule_From{{
					Source: &v1beta1.Source{
						// allow traffic from anywhere
						IpBlocks: []string{"0.0.0.0/0"},
					}},
				},
				To: []*v1beta1.Rule_To{{
					Operation: &v1beta1.Operation{
						Ports: portNums,
					}},
				}},
			},
			Action: v1beta1.AuthorizationPolicy_ALLOW,
		},
	}

	resp.Objects(&authPolicy)

	return nil
}

// VirtualServiceForLink creates an Istio VirtualService for each link between Acorn apps.
// This is in order to make mTLS work between workloads across namespaces.
func VirtualServiceForLink(req router.Request, resp router.Response) error {
	service := req.Object.(*corev1.Service)

	// the link label shouldn't be present on any non-ExternalName type Services, but check anyway
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
