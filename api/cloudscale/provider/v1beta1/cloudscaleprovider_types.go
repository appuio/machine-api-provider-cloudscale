package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type InterfaceType string

const (
	// InterfaceTypePublic is a public network interface.
	InterfaceTypePublic InterfaceType = "Public"
	// InterfaceTypePrivate is a private network interface.
	InterfaceTypePrivate InterfaceType = "Private"
)

// CloudscaleMachineProviderSpec is the type that will be embedded in a Machine.Spec.ProviderSpec field
// for a Cloudscale virtual machine. It is used by the GCP machine actuator to create a single Machine.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type CloudscaleMachineProviderSpec struct {
	metav1.TypeMeta `json:",inline"`

	// ObjectMeta is the standard object's metadata.
	// More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#metadata
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Zone is the zone in which the machine will be created.
	Zone string `json:"zone"`
	// ServerGroups is a list of UUIDs identifying the server groups to which the new server will be added.
	// Used for anti-affinity.
	// https://www.cloudscale.ch/en/api/v1#server-groups
	ServerGroups []string `json:"serverGroups,omitempty"`
	// Tags is a map of tags to apply to the machine.
	Tags map[string]string `json:"tags"`
	// Flavor is the flavor of the machine.
	Flavor string `json:"flavor"`
	// Image is the base image to use for the machine.
	// For images provided by cloudscale: the image’s slug.
	// For custom images: the image’s slug prefixed with custom: (e.g. custom:ubuntu-foo), or its UUID.
	// If multiple custom images with the same slug exist, the newest custom image will be used.
	// https://www.cloudscale.ch/en/api/v1#images
	Image string `json:"image"`
	// RootVolumeSizeGB is the size of the root volume in GB.
	RootVolumeSizeGB int `json:"rootVolumeSizeGB"`
	// SSHKeys is a list of SSH keys to add to the machine.
	SSHKeys []string `json:"sshKeys"`
	// UserData is the user data to pass to the machine.
	// Can be used for ignition or cloud-init.
	UserData string `json:"userData"`
	// UseIPV6 is a flag to enable IPv6 on the machine.
	// Defaults to true.
	UseIPV6 *bool `json:"useIPV6,omitempty"`
	// Interfaces is a list of network interfaces to add to the machine.
	Interfaces []Interface `json:"interfaces"`
}

// Interface is a network interface to add to a machine.
type Interface struct {
	// Type is the type of the interface. Required.
	Type InterfaceType `json:"type"`
	// NetworkUUID is the UUID of the network to attach the interface to.
	// Can only be set if type is private.
	// Must be compatible with Addresses.SubnetUUID if both are specified.
	NetworkUUID string `json:"networkUUID"`
	// Addresses is an optional list of addresses to assign to the interface.
	// Can only be set if type is private.
	Addresses []Address `json:"addresses"`
}

// Address is an address to assign to a network interface.
type Address struct {
	// Address is an optional IP address to assign to the interface.
	Address string `json:"address"`
	// SubnetUUID is the UUID of the subnet to assign the address to.
	// Must be compatible with Interface.NetworkUUID if both are specified.
	SubnetUUID string `json:"subnetUUID"`
}

// CloudscaleMachineProviderStatus is the type that will be embedded in a Machine.Status.ProviderStatus field.
// It contains cloudscale-specific status information.
type CloudscaleMachineProviderStatus struct {
	metav1.TypeMeta `json:",inline"`

	// InstanceID is the ID of the instance in Cloudscale.
	// +optional
	InstanceID string `json:"instanceId,omitempty"`
	// Status is the status of the instance in Cloudscale.
	// Can be "changing", "running" or "stopped".
	Status string `json:"status,omitempty"`
	// Conditions is a set of conditions associated with the Machine to indicate
	// errors or other status
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}