package machine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudscale-ch/cloudscale-go-sdk/v5"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	machinecontroller "github.com/openshift/machine-api-operator/pkg/controller/machine"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	csv1beta1 "github.com/appuio/machine-api-provider-cloudscale/api/cloudscale/provider/v1beta1"
)

const (
	antiAffinityTag = "machine-api-provider-cloudscale_appuio_io_antiAffinityKey"
	machineNameTag  = "machine-api-provider-cloudscale_appuio_io_name"
)

// Actuator is responsible for performing machine reconciliation.
type Actuator struct {
	K8sClient client.Client

	DefaultCloudscaleAPIToken string

	ServerClientFactory      func(token string) cloudscale.ServerService
	ServerGroupClientFactory func(token string) cloudscale.ServerGroupService
}

// ActuatorParams holds parameter information for Actuator.
type ActuatorParams struct {
	K8sClient client.Client

	DefaultCloudscaleAPIToken string

	ServerClientFactory      func(token string) cloudscale.ServerService
	ServerGroupClientFactory func(token string) cloudscale.ServerGroupService
}

// NewActuator returns an actuator.
func NewActuator(params ActuatorParams) *Actuator {
	return &Actuator{
		K8sClient: params.K8sClient,

		DefaultCloudscaleAPIToken: params.DefaultCloudscaleAPIToken,

		ServerClientFactory:      params.ServerClientFactory,
		ServerGroupClientFactory: params.ServerGroupClientFactory,
	}
}

// Create creates a machine and is invoked by the machine controller.
func (a *Actuator) Create(ctx context.Context, machine *machinev1beta1.Machine) error {
	l := log.FromContext(ctx).WithName("Actuator.Create")

	mctx, err := a.getMachineContext(ctx, machine)
	if err != nil {
		return fmt.Errorf("failed to get machine context: %w", err)
	}
	spec := mctx.spec
	sc := a.ServerClientFactory(mctx.token)

	userData, err := a.loadUserDataSecret(ctx, mctx)
	if err != nil {
		return fmt.Errorf("failed to load user data secret: %w", err)
	}

	// Null is not allowed for tags in the cloudscale API
	if spec.Tags == nil {
		spec.Tags = make(map[string]string)
	}
	spec.Tags[machineNameTag] = machine.Name

	serverGroups := spec.ServerGroups
	if spec.AntiAffinityKey != "" {
		sgc := a.ServerGroupClientFactory(mctx.token)
		aasg, err := a.ensureAntiAffinityServerGroupForKey(ctx, sgc, spec.Zone, spec.AntiAffinityKey)
		if err != nil {
			return fmt.Errorf("failed to ensure anti-affinity server group for machine %q and key %q: %w", machine.Name, spec.AntiAffinityKey, err)
		}
		serverGroups = append(serverGroups, aasg)
	}

	name := machine.Name
	if spec.BaseDomain != "" {
		name = fmt.Sprintf("%s.%s", name, spec.BaseDomain)
	}

	req := &cloudscale.ServerRequest{
		Name: name,

		TaggedResourceRequest: cloudscale.TaggedResourceRequest{
			Tags: ptr.To(cloudscale.TagMap(spec.Tags)),
		},
		Zone: spec.Zone,
		ZonalResourceRequest: cloudscale.ZonalResourceRequest{
			Zone: spec.Zone,
		},

		Flavor:       spec.Flavor,
		Image:        spec.Image,
		VolumeSizeGB: spec.RootVolumeSizeGB,
		Interfaces:   cloudscaleServerInterfacesFromProviderSpecInterfaces(spec.Interfaces),
		SSHKeys:      spec.SSHKeys,
		UseIPV6:      spec.UseIPV6,
		ServerGroups: serverGroups,
		UserData:     userData,
	}
	s, err := sc.Create(ctx, req)
	if err != nil {
		reqRaw, _ := json.Marshal(req)
		return fmt.Errorf("failed to create machine %q: %w, req:%+v", machine.Name, err, string(reqRaw))
	}

	l.Info("Created machine", "machine", machine.Name, "uuid", s.UUID, "server", s)

	if err := updateMachineFromCloudscaleServer(machine, *s); err != nil {
		return fmt.Errorf("failed to update machine %q from cloudscale API response: %w", machine.Name, err)
	}

	if err := a.patchMachine(ctx, mctx.machine, machine); err != nil {
		return fmt.Errorf("failed to patch machine %q: %w", machine.Name, err)
	}

	return nil
}

func (a *Actuator) Exists(ctx context.Context, machine *machinev1beta1.Machine) (bool, error) {
	mctx, err := a.getMachineContext(ctx, machine)
	if err != nil {
		return false, fmt.Errorf("failed to get machine context: %w", err)
	}
	sc := a.ServerClientFactory(mctx.token)

	s, err := a.getServer(ctx, sc, machine)

	return s != nil, err
}

func (a *Actuator) Update(ctx context.Context, machine *machinev1beta1.Machine) error {
	mctx, err := a.getMachineContext(ctx, machine)
	if err != nil {
		return fmt.Errorf("failed to get machine context: %w", err)
	}
	sc := a.ServerClientFactory(mctx.token)

	s, err := a.getServer(ctx, sc, machine)
	if err != nil {
		return fmt.Errorf("failed to get server %q: %w", machine.Name, err)
	}

	if err := updateMachineFromCloudscaleServer(machine, *s); err != nil {
		return fmt.Errorf("failed to update machine %q from cloudscale API response: %w", machine.Name, err)
	}

	if err := a.patchMachine(ctx, mctx.machine, machine); err != nil {
		return fmt.Errorf("failed to patch machine %q: %w", machine.Name, err)
	}

	return nil
}

func (a *Actuator) Delete(ctx context.Context, machine *machinev1beta1.Machine) error {
	l := log.FromContext(ctx).WithName("Actuator.Delete")

	mctx, err := a.getMachineContext(ctx, machine)
	if err != nil {
		return fmt.Errorf("failed to get machine context: %w", err)
	}
	sc := a.ServerClientFactory(mctx.token)

	s, err := a.getServer(ctx, sc, machine)
	if err != nil {
		return fmt.Errorf("failed to get server %q: %w", machine.Name, err)
	}

	if s == nil {
		l.Info("Machine to delete not found, skipping", "machine", machine.Name)
		return nil
	}

	if err := sc.Delete(ctx, s.UUID); err != nil {
		return fmt.Errorf("failed to delete server %q: %w", machine.Name, err)
	}

	return nil
}

func (a *Actuator) getServer(ctx context.Context, sc cloudscale.ServerService, machine *machinev1beta1.Machine) (*cloudscale.Server, error) {
	lookupKey := cloudscale.TagMap{machineNameTag: machine.Name}

	ss, err := sc.List(ctx, cloudscale.WithTagFilter(lookupKey))
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

// ensureAntiAffinityServerGroupForKey ensures that a server group with less than 4 servers exists for the given key.
// If such a server group exists, its UUID is returned.
// If no such server group exists, a new server group is created and its UUID is returned.
func (a *Actuator) ensureAntiAffinityServerGroupForKey(ctx context.Context, sgc cloudscale.ServerGroupService, zone, key string) (string, error) {
	l := log.FromContext(ctx).WithName("Actuator.ensureAntiAffinityServerGroupForKey").WithValues("key", key, "zone", zone)
	lookupKey := cloudscale.TagMap{antiAffinityTag: key}

	sgs, err := sgc.List(ctx, cloudscale.WithTagFilter(lookupKey))
	if err != nil {
		return "", fmt.Errorf("failed to list server groups: %w", err)
	}

	for _, sg := range sgs {
		if sg.Zone.Slug == zone {
			if len(sg.Servers) < 4 {
				l.Info("Found existing server group with less than 4 servers", "serverGroup", sg.UUID)
				return sg.UUID, nil
			}
		}
	}

	l.Info("No server group with less than 4 servers left, creating new server group")
	sg, err := sgc.Create(ctx, &cloudscale.ServerGroupRequest{
		ZonalResourceRequest: cloudscale.ZonalResourceRequest{
			Zone: zone,
		},
		TaggedResourceRequest: cloudscale.TaggedResourceRequest{
			Tags: ptr.To(lookupKey),
		},
		Name: key,
		Type: "anti-affinity",
	})
	if err != nil {
		l.Error(err, "Failed to create server group")
		return "", fmt.Errorf("failed to create server group: %w", err)
	}

	return sg.UUID, nil
}

func updateMachineFromCloudscaleServer(machine *machinev1beta1.Machine, s cloudscale.Server) error {
	if machine.Labels == nil {
		machine.Labels = make(map[string]string)
	}
	machine.Labels[machinecontroller.MachineInstanceTypeLabelName] = s.Flavor.Slug
	machine.Labels[machinecontroller.MachineRegionLabelName] = strings.TrimRightFunc(s.Zone.Slug, func(r rune) bool {
		return r >= '0' && r <= '9'
	})
	machine.Labels[machinecontroller.MachineAZLabelName] = s.Zone.Slug

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
	addresses := []corev1.NodeAddress{
		{
			Type:    corev1.NodeHostName,
			Address: s.Name,
		},
	}

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

type machineContext struct {
	machine *machinev1beta1.Machine
	spec    csv1beta1.CloudscaleMachineProviderSpec
	token   string
}

func (a *Actuator) getMachineContext(ctx context.Context, machine *machinev1beta1.Machine) (*machineContext, error) {
	const tokenKey = "token"

	origMachine := machine.DeepCopy()

	spec, err := csv1beta1.ProviderSpecFromRawExtension(machine.Spec.ProviderSpec.Value)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider spec from machine %q: %w", machine.Name, err)
	}

	token := a.DefaultCloudscaleAPIToken
	if spec.TokenSecret != nil {
		secret := &corev1.Secret{}
		if err := a.K8sClient.Get(ctx, client.ObjectKey{Name: spec.TokenSecret.Name, Namespace: machine.Namespace}, secret); err != nil {
			return nil, fmt.Errorf("failed to get secret %q: %w", spec.TokenSecret.Name, err)
		}

		tb, ok := secret.Data[tokenKey]
		if !ok {
			return nil, fmt.Errorf("token key %q not found in secret %q", tokenKey, spec.TokenSecret.Name)
		}

		token = string(tb)
	}

	return &machineContext{
		machine: origMachine,
		spec:    *spec,
		token:   token,
	}, nil
}

func (a *Actuator) loadUserDataSecret(ctx context.Context, mctx *machineContext) (string, error) {
	const userDataKey = "userData"

	if mctx.spec.UserDataSecret == nil {
		return "", nil
	}

	secret := &corev1.Secret{}
	if err := a.K8sClient.Get(ctx, client.ObjectKey{Name: mctx.spec.UserDataSecret.Name, Namespace: mctx.machine.Namespace}, secret); err != nil {
		return "", fmt.Errorf("failed to get secret %q: %w", mctx.spec.UserDataSecret.Name, err)
	}

	userData, ok := secret.Data[userDataKey]
	if !ok {
		return "", fmt.Errorf("%q key not found in secret %q", userDataKey, mctx.spec.UserDataSecret.Name)
	}

	return string(userData), nil
}
