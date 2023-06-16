package cidr

import (
	"fmt"

	"github.com/acorn-io/acorn-istio-plugin/pkg/cilium"
	"github.com/acorn-io/baaah/pkg/router"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/strings/slices"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func GetPodCIDRsFromCilium(req router.Request) ([]string, error) {
	ciliumNodes := cilium.CiliumNodeList{}
	if err := req.List(&ciliumNodes, &client.ListOptions{}); err != nil {
		return nil, fmt.Errorf("failed to list CiliumNodes: %w", err)
	}

	var cidrs []string
	for _, cn := range ciliumNodes.Items {
		for _, cidr := range cn.Spec.IPAM.PodCIDRs {
			if !slices.Contains(cidrs, cidr) {
				cidrs = append(cidrs, cidr)
			}
		}
	}

	return cidrs, nil
}

func GetPodCIDRsFromNodes(req router.Request) ([]string, error) {
	nodes := corev1.NodeList{}
	if err := req.List(&nodes, &client.ListOptions{}); err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}

	var cidrs []string
	for _, node := range nodes.Items {
		for _, cidr := range node.Spec.PodCIDRs {
			if !slices.Contains(cidrs, cidr) {
				cidrs = append(cidrs, cidr)
			}
		}
	}

	return cidrs, nil
}
