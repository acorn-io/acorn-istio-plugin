package cilium

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// Here we register our own types for Cilium, since we only care about reading one spec field from CiliumNodes.
// It is easier than trying to use their API library.

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type CiliumNode struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`
	Spec              NodeSpec `json:"spec"`
}

// +k8s:deepcopy-gen=true

type NodeSpec struct {
	IPAM IPAMSpec `json:"ipam,omitempty"`
}

// +k8s:deepcopy-gen=true

type IPAMSpec struct {
	PodCIDRs []string `json:"podCIDRs,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type CiliumNodeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []CiliumNode `json:"items"`
}
