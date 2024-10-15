package machine

import (
	"context"
	"fmt"

	"github.com/cloudscale-ch/cloudscale-go-sdk/v5"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	csv1beta1 "github.com/appuio/machine-api-provider-cloudscale/api/cloudscale/provider/v1beta1"
)

// Actuator is responsible for performing machine reconciliation.
type Actuator struct {
	K8sClient    client.Client
	ServerClient cloudscale.ServerService
}

// ActuatorParams holds parameter information for Actuator.
type ActuatorParams struct {
	K8sClient    client.Client
	ServerClient cloudscale.ServerService
}

// NewActuator returns an actuator.
func NewActuator(params ActuatorParams) *Actuator {
	return &Actuator{
		K8sClient:    params.K8sClient,
		ServerClient: params.ServerClient,
	}
}

// Create creates a machine and is invoked by the machine controller.
func (a *Actuator) Create(ctx context.Context, machine *machinev1beta1.Machine) error {
	l := log.FromContext(ctx).WithName("Actuator.Create")
	origMachine := machine.DeepCopy()

	spec, err := csv1beta1.ProviderSpecFromRawExtension(machine.Spec.ProviderSpec.Value)
	if err != nil {
		return fmt.Errorf("failed to get provider spec from machine %q: %w", machine.Name, err)
	}

	s, err := a.ServerClient.Create(ctx, &cloudscale.ServerRequest{
		Name: machine.Name,

		TaggedResourceRequest: cloudscale.TaggedResourceRequest{
			Tags: ptr.To(cloudscale.TagMap(spec.Tags)),
		},
		Zone: spec.Zone,
		ZonalResourceRequest: cloudscale.ZonalResourceRequest{
			Zone: spec.Zone,
		},

		Flavor:            spec.Flavor,
		Image:             spec.Image,
		VolumeSizeGB:      spec.RootVolumeSizeGB,
		Interfaces:        cloudscaleServerInterfacesFromProviderSpecInterfaces(spec.Interfaces),
		SSHKeys:           spec.SSHKeys,
		UsePublicNetwork:  ptr.To(false),
		UsePrivateNetwork: ptr.To(false),
		UseIPV6:           spec.UseIPV6,
		ServerGroups:      spec.ServerGroups,
		UserData:          spec.UserData,
	})
	if err != nil {
		return fmt.Errorf("failed to create machine %q: %w", machine.Name, err)
	}

	l.Info("Created machine", "machine", machine.Name, "uuid", s.UUID, "server", s)

	if err := updateMachineFromCloudscaleServer(machine, *s); err != nil {
		return fmt.Errorf("failed to update machine %q from cloudscale API response: %w", machine.Name, err)
	}

	if err := a.patchMachine(ctx, origMachine, machine); err != nil {
		return fmt.Errorf("failed to patch machine %q: %w", machine.Name, err)
	}

	return nil
}

func (a *Actuator) Exists(ctx context.Context, machine *machinev1beta1.Machine) (bool, error) {
	s, err := a.getServer(ctx, machine)

	return s != nil, err
}

func (a *Actuator) Update(ctx context.Context, machine *machinev1beta1.Machine) error {
	origMachine := machine.DeepCopy()

	s, err := a.getServer(ctx, machine)
	if err != nil {
		return fmt.Errorf("failed to get server %q: %w", machine.Name, err)
	}

	if err := updateMachineFromCloudscaleServer(machine, *s); err != nil {
		return fmt.Errorf("failed to update machine %q from cloudscale API response: %w", machine.Name, err)
	}

	if err := a.patchMachine(ctx, origMachine, machine); err != nil {
		return fmt.Errorf("failed to patch machine %q: %w", machine.Name, err)
	}

	return nil
}

func (a *Actuator) Delete(ctx context.Context, machine *machinev1beta1.Machine) error {
	return fmt.Errorf("not implemented")
}

func (a *Actuator) getServer(ctx context.Context, machine *machinev1beta1.Machine) (*cloudscale.Server, error) {
	ss, err := a.ServerClient.List(ctx, cloudscale.WithNameFilter(machine.Name))
	if err != nil {
		return nil, fmt.Errorf("failed to list servers: %w", err)
	}
	if len(ss) == 0 {
		return nil, nil
	}
	if len(ss) > 1 {
		return nil, fmt.Errorf("found multiple servers with name %q", machine.Name)
	}

	return &ss[0], nil
}

func (a *Actuator) patchMachine(ctx context.Context, orig, updated *machinev1beta1.Machine) error {
	if equality.Semantic.DeepEqual(orig, updated) {
		return nil
	}

	st := *updated.Status.DeepCopy()

	if err := a.K8sClient.Patch(ctx, updated, client.MergeFrom(orig)); err != nil {
		return fmt.Errorf("failed to patch machine %q: %w", updated.Name, err)
	}

	updated.Status = st
	if err := a.K8sClient.Status().Patch(ctx, updated, client.MergeFrom(orig)); err != nil {
		return fmt.Errorf("failed to patch machine status %q: %w", updated.Name, err)
	}

	return nil
}

func updateMachineFromCloudscaleServer(machine *machinev1beta1.Machine, s cloudscale.Server) error {
	machine.Spec.ProviderID = ptr.To(formatProviderID(s.UUID))
	machine.Status.Addresses = machineAddressesFromCloudscaleServer(s)
	status := providerStatusFromCloudscaleServer(s)
	rawStatus, err := csv1beta1.RawExtensionFromProviderStatus(&status)
	if err != nil {
		return fmt.Errorf("failed to create raw extension from provider status: %w", err)
	}
	machine.Status.ProviderStatus = rawStatus

	return nil
}

func machineAddressesFromCloudscaleServer(s cloudscale.Server) []corev1.NodeAddress {
	var addresses []corev1.NodeAddress

	for _, n := range s.Interfaces {
		typ := corev1.NodeInternalIP
		if n.Type == "public" {
			typ = corev1.NodeExternalIP
		}

		for _, a := range n.Addresses {
			addresses = append(addresses, corev1.NodeAddress{
				Type:    typ,
				Address: a.Address,
			})
		}
	}

	return addresses
}

func formatProviderID(uuid string) string {
	return fmt.Sprintf("cloudscale:///%s", uuid)
}

func providerStatusFromCloudscaleServer(s cloudscale.Server) csv1beta1.CloudscaleMachineProviderStatus {
	return csv1beta1.CloudscaleMachineProviderStatus{
		InstanceID: s.UUID,
		Status:     s.Status,
	}
}

func cloudscaleServerInterfacesFromProviderSpecInterfaces(interfaces []csv1beta1.Interface) *[]cloudscale.InterfaceRequest {
	if interfaces == nil {
		return nil
	}

	var cloudscaleInterfaces []cloudscale.InterfaceRequest
	for _, i := range interfaces {
		if i.Type == csv1beta1.InterfaceTypePublic {
			cloudscaleInterfaces = append(cloudscaleInterfaces, cloudscale.InterfaceRequest{
				Network: "public",
			})
			continue
		}

		ifr := cloudscale.InterfaceRequest{
			Network: i.NetworkUUID,
		}

		if i.Addresses != nil {
			addrs := make([]cloudscale.AddressRequest, 0, len(i.Addresses))
			for _, a := range i.Addresses {
				addrs = append(addrs, cloudscale.AddressRequest{
					Subnet:  a.SubnetUUID,
					Address: a.Address,
				})
			}
			ifr.Addresses = &addrs
		}

		cloudscaleInterfaces = append(cloudscaleInterfaces, ifr)
	}
	return &cloudscaleInterfaces
}
