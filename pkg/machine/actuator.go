package machine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cloudscale-ch/cloudscale-go-sdk/v6"
	"github.com/google/go-jsonnet"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	machinecontroller "github.com/openshift/machine-api-operator/pkg/controller/machine"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	csv1beta1 "github.com/appuio/machine-api-provider-cloudscale/api/cloudscale/provider/v1beta1"
)

const (
	antiAffinityTag     = "machine-api-provider-cloudscale_appuio_io_antiAffinityKey"
	machineNameTag      = "machine-api-provider-cloudscale_appuio_io_name"
	machineClusterIDTag = "machine-api-provider-cloudscale_appuio_io_cluster_id"

	machineClusterIDLabelName = "machine.openshift.io/cluster-api-cluster"
)

// Actuator is responsible for performing machine reconciliation.
// It creates, updates, and deletes machines.
// Currently changing machine spec is not supported.
// The user data is rendered using Jsonnet with the machine and secret data as context.
// Machines are automatically spread across server groups on create based on the AntiAffinityKey.
type Actuator struct {
	k8sClient client.Client

	defaultCloudscaleAPIToken string

	serverClientFactory      func(token string) cloudscale.ServerService
	serverGroupClientFactory func(token string) cloudscale.ServerGroupService
	volumeClientFactory      func(token string) cloudscale.VolumeService
}

// ActuatorParams holds parameter information for Actuator.
type ActuatorParams struct {
	K8sClient client.Client

	DefaultCloudscaleAPIToken string

	ServerClientFactory      func(token string) cloudscale.ServerService
	ServerGroupClientFactory func(token string) cloudscale.ServerGroupService
	VolumeClientFactory      func(token string) cloudscale.VolumeService
}

// NewActuator returns an actuator.
func NewActuator(params ActuatorParams) *Actuator {
	return &Actuator{
		k8sClient: params.K8sClient,

		defaultCloudscaleAPIToken: params.DefaultCloudscaleAPIToken,

		serverClientFactory:      params.ServerClientFactory,
		serverGroupClientFactory: params.ServerGroupClientFactory,
		volumeClientFactory:      params.VolumeClientFactory,
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
	sc := a.serverClientFactory(mctx.token)

	userData, err := a.loadAndRenderUserDataSecret(ctx, mctx)
	if err != nil {
		return fmt.Errorf("failed to load user data secret: %w", err)
	}

	// Null is not allowed for tags in the cloudscale API
	if spec.Tags == nil {
		spec.Tags = make(map[string]string)
	}
	spec.Tags[machineNameTag] = machine.Name
	spec.Tags[machineClusterIDTag] = mctx.clusterId

	// Null is not allowed for SSH keys in the cloudscale API
	if spec.SSHKeys == nil {
		spec.SSHKeys = []string{}
	}

	serverGroups := spec.ServerGroups
	if spec.AntiAffinityKey != "" {
		sgc := a.serverGroupClientFactory(mctx.token)
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

	// Tag the RootVolume if tags are set
	// It can take some time for CloudScale to populate the root volume UUID
	if spec.RootVolumeTags != nil {
		backoff := wait.Backoff{
			Duration: 1 * time.Second,
			Factor:   2.0,
			Jitter:   0.1,
			Steps:    10,
			Cap:      5 * time.Minute,
		}
		vc := a.volumeClientFactory(mctx.token)

		var lastErr error
		err := wait.ExponentialBackoff(backoff, func() (bool, error) {
			// query server to check if root volume UUID has been populated
			s, err = sc.Get(ctx, s.UUID)
			if err != nil {
				lastErr = err
				return false, nil
			}
			if len(s.Volumes) == 0 {
				lastErr = fmt.Errorf("no volumes found for server %q", s.UUID)
				return false, nil
			}
			rootVolumeUUID := s.Volumes[0].UUID
			if rootVolumeUUID == "" {
				lastErr = fmt.Errorf("root volume UUID is empty for server %q", s.UUID)
				return false, nil
			}
			if err := tagRootVolume(ctx, vc, rootVolumeUUID, spec.RootVolumeTags); err != nil {
				lastErr = err
				return false, nil
			}
			return true, nil
		})
		if err != nil {
			reqRaw, _ := json.Marshal(req)
			if lastErr == nil {
				lastErr = err
			}
			return fmt.Errorf("failed to tag root volume of machine %q: %w (last error: %v), req:%+v", machine.Name, err, lastErr, string(reqRaw))
		}
	}

	return nil
}

func tagRootVolume(ctx context.Context, vc cloudscale.VolumeService, uuid string, tags map[string]string) error {
	req := &cloudscale.VolumeRequest{
		TaggedResourceRequest: cloudscale.TaggedResourceRequest{
			Tags: ptr.To(cloudscale.TagMap(tags)),
		},
	}
	return vc.Update(ctx, uuid, req)
}

func (a *Actuator) Exists(ctx context.Context, machine *machinev1beta1.Machine) (bool, error) {
	mctx, err := a.getMachineContext(ctx, machine)
	if err != nil {
		return false, fmt.Errorf("failed to get machine context: %w", err)
	}
	sc := a.serverClientFactory(mctx.token)

	s, err := a.getServer(ctx, sc, *mctx)

	return s != nil, err
}

func (a *Actuator) Update(ctx context.Context, machine *machinev1beta1.Machine) error {
	mctx, err := a.getMachineContext(ctx, machine)
	if err != nil {
		return fmt.Errorf("failed to get machine context: %w", err)
	}
	sc := a.serverClientFactory(mctx.token)

	s, err := a.getServer(ctx, sc, *mctx)
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
	sc := a.serverClientFactory(mctx.token)

	s, err := a.getServer(ctx, sc, *mctx)
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

func (a *Actuator) getServer(ctx context.Context, sc cloudscale.ServerService, machineCtx machineContext) (*cloudscale.Server, error) {
	lookupKey := cloudscale.TagMap{
		machineNameTag: machineCtx.machine.Name,
	}

	ssa, err := sc.List(ctx, cloudscale.WithTagFilter(lookupKey))
	if err != nil {
		return nil, fmt.Errorf("failed to list servers: %w", err)
	}
	// The cloudscale API does not support filtering by multiple tags, so we have to filter manually
	ss := make([]cloudscale.Server, 0, len(ssa))
	for _, s := range ssa {
		if tk := s.TaggedResource.Tags[machineClusterIDTag]; tk != "" && tk == machineCtx.clusterId {
			ss = append(ss, s)
		}
	}

	if len(ss) == 0 {
		return nil, nil
	}
	if len(ss) > 1 {
		return nil, fmt.Errorf("found multiple servers with name %q", machineCtx.machine.Name)
	}

	return &ss[0], nil
}

func (a *Actuator) patchMachine(ctx context.Context, orig, updated *machinev1beta1.Machine) error {
	if equality.Semantic.DeepEqual(orig, updated) {
		return nil
	}

	st := *updated.Status.DeepCopy()

	if err := a.k8sClient.Patch(ctx, updated, client.MergeFrom(orig)); err != nil {
		return fmt.Errorf("failed to patch machine %q: %w", updated.Name, err)
	}

	updated.Status = st
	if err := a.k8sClient.Status().Patch(ctx, updated, client.MergeFrom(orig)); err != nil {
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
		{
			Type:    corev1.NodeInternalDNS,
			Address: s.Name,
		},
	}

	// https://github.com/openshift/cluster-machine-approver?tab=readme-ov-file#requirements-for-cluster-api-providers
	// * A Machine must have a NodeInternalDNS set in Status.Addresses that matches the name of the Node.
	//   The NodeInternalDNS entry must be present, even before the Node resource is created.
	// * A Machine must also have matching NodeInternalDNS, NodeExternalDNS, NodeHostName, NodeInternalIP, and NodeExternalIP addresses
	//   as those listed on the Node resource. All of these addresses are placed in the CSR and are validated against the addresses
	//   on the Machine object.
	hostname := strings.Split(s.Name, ".")[0]
	if s.Name != hostname {
		addresses = append(addresses, corev1.NodeAddress{
			Type:    corev1.NodeInternalDNS,
			Address: hostname,
		})
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
	return fmt.Sprintf("cloudscale://%s", uuid)
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
			// From the cloudscale terraform provider. Public interfaces have no other configuration options.
			// https://github.com/cloudscale-ch/terraform-provider-cloudscale/blob/56f5cb40396e489657ee965a5f066b8a9f5c1bd5/cloudscale/resource_cloudscale_server.go#L424-L427
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
	machine   *machinev1beta1.Machine
	clusterId string
	spec      csv1beta1.CloudscaleMachineProviderSpec
	token     string
}

func (a *Actuator) getMachineContext(ctx context.Context, machine *machinev1beta1.Machine) (*machineContext, error) {
	const tokenKey = "token"

	origMachine := machine.DeepCopy()

	clusterId, ok := machine.Labels[machineClusterIDLabelName]
	if !ok {
		return nil, fmt.Errorf("cluster ID label %q not found on machine %q", machineClusterIDLabelName, machine.Name)
	}

	spec, err := csv1beta1.ProviderSpecFromRawExtension(machine.Spec.ProviderSpec.Value)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider spec from machine %q: %w", machine.Name, err)
	}

	token := a.defaultCloudscaleAPIToken
	if spec.TokenSecret != nil {
		secret := &corev1.Secret{}
		if err := a.k8sClient.Get(ctx, client.ObjectKey{Name: spec.TokenSecret.Name, Namespace: machine.Namespace}, secret); err != nil {
			return nil, fmt.Errorf("failed to get secret %q: %w", spec.TokenSecret.Name, err)
		}

		tb, ok := secret.Data[tokenKey]
		if !ok {
			return nil, fmt.Errorf("token key %q not found in secret %q", tokenKey, spec.TokenSecret.Name)
		}

		token = string(tb)
	}

	return &machineContext{
		machine:   origMachine,
		clusterId: clusterId,
		spec:      *spec,
		token:     token,
	}, nil
}

func (a *Actuator) loadAndRenderUserDataSecret(ctx context.Context, mctx *machineContext) (string, error) {
	const userDataKey = "userData"

	if mctx.spec.UserDataSecret == nil {
		return "", nil
	}

	secret := &corev1.Secret{}
	if err := a.k8sClient.Get(ctx, client.ObjectKey{Name: mctx.spec.UserDataSecret.Name, Namespace: mctx.machine.Namespace}, secret); err != nil {
		return "", fmt.Errorf("failed to get secret %q: %w", mctx.spec.UserDataSecret.Name, err)
	}

	userDataRaw, ok := secret.Data[userDataKey]
	if !ok {
		return "", fmt.Errorf("%q key not found in secret %q", userDataKey, mctx.spec.UserDataSecret.Name)
	}
	userData := string(userDataRaw)

	if userData == "" {
		return "", nil
	}

	data := make(map[string]string, len(secret.Data))
	for k, v := range secret.Data {
		data[k] = string(v)
	}

	var userDataSecrets corev1.SecretList
	if mctx.spec.UserDataSecretSelector != nil {
		sel, err := metav1.LabelSelectorAsSelector(mctx.spec.UserDataSecretSelector)
		if err != nil {
			return "", fmt.Errorf("failed to parse UserDataSecretSelector: %w", err)
		}
		if err := a.k8sClient.List(
			ctx,
			&userDataSecrets,
			client.InNamespace(mctx.machine.Namespace),
			client.MatchingLabelsSelector{Selector: sel},
		); err != nil {
			return "", fmt.Errorf("failed to list secrets in namespace %q: %w", mctx.machine.Namespace, err)
		}
	}

	jvm, err := jsonnetVMWithContext(mctx.machine, data, userDataSecrets)
	if err != nil {
		return "", fmt.Errorf("userData: failed to create jsonnet VM: %w", err)
	}
	ud, err := jvm.EvaluateAnonymousSnippet("context", userData)
	if err != nil {
		return "", fmt.Errorf("userData: failed to evaluate jsonnet: %w", err)
	}

	var compacted bytes.Buffer
	if err := json.Compact(&compacted, []byte(ud)); err != nil {
		return "", fmt.Errorf("userData: failed to compact json: %w", err)
	}

	return compacted.String(), nil
}

func jsonnetVMWithContext(machine *machinev1beta1.Machine, data map[string]string, userDataSecrets corev1.SecretList) (*jsonnet.VM, error) {
	jcr, err := json.Marshal(map[string]any{
		"machine": machine,
		"data":    data,
		"secrets": userDataSecrets.Items,
	})
	if err != nil {
		return nil, fmt.Errorf("unable to marshal jsonnet context: %w", err)
	}
	jvm := jsonnet.MakeVM()
	jvm.ExtCode("context", string(jcr))
	// Don't allow imports
	jvm.Importer(&jsonnet.MemoryImporter{})
	return jvm, nil
}
