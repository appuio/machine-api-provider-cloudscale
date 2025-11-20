package machine

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/appuio/machine-api-provider-cloudscale/pkg/machine/csmock"
	"github.com/cloudscale-ch/cloudscale-go-sdk/v6"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	machinecontroller "github.com/openshift/machine-api-operator/pkg/controller/machine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	csv1beta1 "github.com/appuio/machine-api-provider-cloudscale/api/cloudscale/provider/v1beta1"
)

func Test_Actuator_Create_ComplexMachineE2E(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	const clusterID = "cluster-id"

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	machine := &machinev1beta1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name: "app-test",
			Labels: map[string]string{
				machineClusterIDLabelName: clusterID,
			},
		},
	}
	providerSpec := csv1beta1.CloudscaleMachineProviderSpec{
		UserDataSecret:   &corev1.LocalObjectReference{Name: "app-user-data"},
		TokenSecret:      &corev1.LocalObjectReference{Name: "cloudscale-token"},
		BaseDomain:       "cluster.example.com",
		Zone:             "rma1",
		AntiAffinityKey:  "app",
		Flavor:           "flex-16-4",
		Image:            "custom:rhcos-4.15",
		RootVolumeSizeGB: 100,
		Interfaces: []csv1beta1.Interface{
			{
				Type:        csv1beta1.InterfaceTypePrivate,
				NetworkUUID: "6ad814b4-587f-44d2-96a1-38750c9a21d5",
				Addresses: []csv1beta1.Address{
					{
						SubnetUUID: "6ad814b4-587f-44d2-96a1-38750c9a21d5",
						Address:    "172.10.11.12",
					},
				},
			}, {
				Type: csv1beta1.InterfaceTypePublic,
			},
		},
	}
	setProviderSpecOnMachine(t, machine, &providerSpec)
	tokenSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: providerSpec.TokenSecret.Name,
		},
		Data: map[string][]byte{
			"token": []byte("my-cloudscale-token"),
		},
	}
	userDataSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: providerSpec.UserDataSecret.Name,
		},
		Data: map[string][]byte{
			"ignitionCA": []byte("CADATA"),
			"userData":   []byte("{ca: std.extVar('context').data.ignitionCA}"),
		},
	}

	c := newFakeClient(t, machine, tokenSecret, userDataSecret)
	ss := csmock.NewMockServerService(ctrl)
	sgs := csmock.NewMockServerGroupService(ctrl)
	actuator := newActuator(c, ss, sgs)

	sgs.EXPECT().List(
		gomock.Any(),
		csTagMatcher{t: t, tags: map[string]string{antiAffinityTag: providerSpec.AntiAffinityKey}},
	).Return([]cloudscale.ServerGroup{}, nil)

	sgs.EXPECT().Create(
		gomock.Any(),
		newDeepEqualMatcher(t, &cloudscale.ServerGroupRequest{
			Name: providerSpec.AntiAffinityKey,
			TaggedResourceRequest: cloudscale.TaggedResourceRequest{
				Tags: &cloudscale.TagMap{
					antiAffinityTag: providerSpec.AntiAffinityKey,
				},
			},
			Type: "anti-affinity",
			ZonalResourceRequest: cloudscale.ZonalResourceRequest{
				Zone: providerSpec.Zone,
			},
		}),
	).Return(&cloudscale.ServerGroup{
		UUID: "created-server-group-uuid",
	}, nil)

	ss.EXPECT().Create(
		gomock.Any(),
		newDeepEqualMatcher(t, &cloudscale.ServerRequest{
			Name: fmt.Sprintf("%s.%s", machine.Name, providerSpec.BaseDomain),

			TaggedResourceRequest: cloudscale.TaggedResourceRequest{
				Tags: ptr.To(cloudscale.TagMap{
					machineNameTag:      machine.Name,
					machineClusterIDTag: clusterID,
				}),
			},
			Zone: providerSpec.Zone,
			ZonalResourceRequest: cloudscale.ZonalResourceRequest{
				Zone: providerSpec.Zone,
			},
			Flavor:       providerSpec.Flavor,
			Image:        providerSpec.Image,
			VolumeSizeGB: providerSpec.RootVolumeSizeGB,
			Interfaces: ptr.To([]cloudscale.InterfaceRequest{
				{
					Network: providerSpec.Interfaces[0].NetworkUUID,
					Addresses: &[]cloudscale.AddressRequest{{
						Subnet:  providerSpec.Interfaces[0].Addresses[0].SubnetUUID,
						Address: providerSpec.Interfaces[0].Addresses[0].Address,
					}},
				}, {
					Network: "public",
				},
			}),
			SSHKeys:      []string{},
			UseIPV6:      providerSpec.UseIPV6,
			ServerGroups: []string{"created-server-group-uuid"},
			UserData:     "{\"ca\":\"CADATA\"}",
		}),
	).DoAndReturn(cloudscaleServerFromServerRequest(func(s *cloudscale.Server) {
		s.UUID = "created-server-uuid"
		s.TaggedResource = cloudscale.TaggedResource{
			Tags: cloudscale.TagMap{
				machineNameTag:      machine.Name,
				machineClusterIDTag: clusterID,
			},
		}
	}))

	require.NoError(t, actuator.Create(ctx, machine))

	updatedMachine := &machinev1beta1.Machine{}
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(machine), updatedMachine))
	if assert.NotNil(t, updatedMachine.Spec.ProviderID) {
		assert.Equal(t, "cloudscale://created-server-uuid", *updatedMachine.Spec.ProviderID)
	}

	// Labels are just for show with kubectl get
	if assert.NotNil(t, updatedMachine.Labels) {
		assert.Equal(t, "flex-16-4", updatedMachine.Labels[machinecontroller.MachineInstanceTypeLabelName])
		assert.Equal(t, "rma1", updatedMachine.Labels[machinecontroller.MachineAZLabelName])
		assert.Equal(t, "rma", updatedMachine.Labels[machinecontroller.MachineRegionLabelName])
	}

	// https://github.com/openshift/cluster-machine-approver?tab=readme-ov-file#requirements-for-cluster-api-providers
	// * A Machine must have a NodeInternalDNS set in Status.Addresses that matches the name of the Node.
	//   The NodeInternalDNS entry must be present, even before the Node resource is created.
	// * A Machine must also have matching NodeInternalDNS, NodeExternalDNS, NodeHostName, NodeInternalIP, and NodeExternalIP addresses
	//   as those listed on the Node resource. All of these addresses are placed in the CSR and are validated against the addresses
	//   on the Machine object.
	assert.ElementsMatch(t, []corev1.NodeAddress{
		{
			Type:    corev1.NodeHostName,
			Address: "app-test.cluster.example.com",
		},
		{
			Type:    corev1.NodeInternalDNS,
			Address: "app-test",
		},
		{
			Type:    corev1.NodeInternalDNS,
			Address: "app-test.cluster.example.com",
		},
		{
			Type:    corev1.NodeInternalIP,
			Address: "172.10.11.12",
		},
	}, updatedMachine.Status.Addresses)
}

func Test_Actuator_Create_AntiAffinityPools(t *testing.T) {
	const zone = "rma1"

	const clusterID = "cluster-id"

	tcs := []struct {
		name    string
		apiMock func(*testing.T, *machinev1beta1.Machine, csv1beta1.CloudscaleMachineProviderSpec, *csmock.MockServerService, *csmock.MockServerGroupService)
		err     error
	}{
		{
			name: "no anti-affinity pool exists",
			apiMock: func(t *testing.T, machine *machinev1beta1.Machine, ps csv1beta1.CloudscaleMachineProviderSpec, ss *csmock.MockServerService, sgs *csmock.MockServerGroupService) {
				const newSGUUID = "new-server-group-uuid"
				sgs.EXPECT().List(
					gomock.Any(),
					csTagMatcher{t: t, tags: map[string]string{antiAffinityTag: ps.AntiAffinityKey}},
				).Return([]cloudscale.ServerGroup{}, nil)
				sgs.EXPECT().Create(
					gomock.Any(),
					newDeepEqualMatcher(t, &cloudscale.ServerGroupRequest{
						Name: ps.AntiAffinityKey,
						ZonalResourceRequest: cloudscale.ZonalResourceRequest{
							Zone: zone,
						},
						TaggedResourceRequest: cloudscale.TaggedResourceRequest{
							Tags: &cloudscale.TagMap{
								antiAffinityTag: ps.AntiAffinityKey,
							},
						},
						Type: "anti-affinity",
					}),
				).Return(&cloudscale.ServerGroup{
					UUID: newSGUUID,
				}, nil)
				ss.EXPECT().Create(
					gomock.Any(),
					newDeepEqualMatcher(t, &cloudscale.ServerRequest{
						Name: machine.Name,
						ZonalResourceRequest: cloudscale.ZonalResourceRequest{
							Zone: zone,
						},
						TaggedResourceRequest: cloudscale.TaggedResourceRequest{
							Tags: ptr.To(cloudscale.TagMap{
								machineNameTag:      machine.Name,
								machineClusterIDTag: clusterID,
							}),
						},
						ServerGroups: []string{newSGUUID},
						SSHKeys:      []string{},
						Zone:         zone,
					}),
				).Return(&cloudscale.Server{}, nil)
			},
		},
		{
			name: "pool with space left exists",
			apiMock: func(t *testing.T, machine *machinev1beta1.Machine, ps csv1beta1.CloudscaleMachineProviderSpec, ss *csmock.MockServerService, sgs *csmock.MockServerGroupService) {
				const existingUUID = "existing-server-group-uuid"
				sgs.EXPECT().List(
					gomock.Any(),
					csTagMatcher{t: t, tags: map[string]string{antiAffinityTag: ps.AntiAffinityKey}},
				).Return([]cloudscale.ServerGroup{
					{
						UUID:    existingUUID,
						Servers: make([]cloudscale.ServerStub, 3),
						ZonalResource: cloudscale.ZonalResource{
							Zone: cloudscale.Zone{
								Slug: zone,
							},
						},
					},
				}, nil)
				ss.EXPECT().Create(
					gomock.Any(),
					newDeepEqualMatcher(t, &cloudscale.ServerRequest{
						Name: machine.Name,
						ZonalResourceRequest: cloudscale.ZonalResourceRequest{
							Zone: zone,
						},
						TaggedResourceRequest: cloudscale.TaggedResourceRequest{
							Tags: ptr.To(cloudscale.TagMap{
								machineNameTag:      machine.Name,
								machineClusterIDTag: clusterID,
							}),
						},
						ServerGroups: []string{existingUUID},
						SSHKeys:      []string{},
						Zone:         zone,
					}),
				).Return(&cloudscale.Server{}, nil)
			},
		},
		{
			name: "pool with no space left exists, create new pool",
			apiMock: func(t *testing.T, machine *machinev1beta1.Machine, ps csv1beta1.CloudscaleMachineProviderSpec, ss *csmock.MockServerService, sgs *csmock.MockServerGroupService) {
				const newSGUUID = "new-server-group-uuid"
				sgs.EXPECT().List(
					gomock.Any(),
					csTagMatcher{t: t, tags: map[string]string{antiAffinityTag: ps.AntiAffinityKey}},
				).Return([]cloudscale.ServerGroup{
					{
						UUID:    "existing-server-group-uuid",
						Servers: make([]cloudscale.ServerStub, 4),
						ZonalResource: cloudscale.ZonalResource{
							Zone: cloudscale.Zone{
								Slug: zone,
							},
						},
					},
				}, nil)
				sgs.EXPECT().Create(
					gomock.Any(),
					newDeepEqualMatcher(t, &cloudscale.ServerGroupRequest{
						Name: ps.AntiAffinityKey,
						ZonalResourceRequest: cloudscale.ZonalResourceRequest{
							Zone: zone,
						},
						TaggedResourceRequest: cloudscale.TaggedResourceRequest{
							Tags: &cloudscale.TagMap{
								antiAffinityTag: ps.AntiAffinityKey,
							},
						},
						Type: "anti-affinity",
					}),
				).Return(&cloudscale.ServerGroup{
					UUID: newSGUUID,
				}, nil)
				ss.EXPECT().Create(
					gomock.Any(),
					newDeepEqualMatcher(t, &cloudscale.ServerRequest{
						Name: machine.Name,
						ZonalResourceRequest: cloudscale.ZonalResourceRequest{
							Zone: zone,
						},
						TaggedResourceRequest: cloudscale.TaggedResourceRequest{
							Tags: ptr.To(cloudscale.TagMap{
								machineNameTag:      machine.Name,
								machineClusterIDTag: clusterID,
							}),
						},
						ServerGroups: []string{newSGUUID},
						SSHKeys:      []string{},
						Zone:         zone,
					}),
				).Return(&cloudscale.Server{}, nil)
			},
		},
		{
			name: "pool with space exists in wrong zone, create new pool",
			apiMock: func(t *testing.T, machine *machinev1beta1.Machine, ps csv1beta1.CloudscaleMachineProviderSpec, ss *csmock.MockServerService, sgs *csmock.MockServerGroupService) {
				const newSGUUID = "new-server-group-uuid"
				sgs.EXPECT().List(
					gomock.Any(),
					csTagMatcher{t: t, tags: map[string]string{antiAffinityTag: ps.AntiAffinityKey}},
				).Return([]cloudscale.ServerGroup{
					{
						UUID:    "existing-server-group-uuid",
						Servers: make([]cloudscale.ServerStub, 0),
						ZonalResource: cloudscale.ZonalResource{
							Zone: cloudscale.Zone{
								Slug: "other-zone",
							},
						},
					},
				}, nil)
				sgs.EXPECT().Create(
					gomock.Any(),
					newDeepEqualMatcher(t, &cloudscale.ServerGroupRequest{
						Name: ps.AntiAffinityKey,
						ZonalResourceRequest: cloudscale.ZonalResourceRequest{
							Zone: zone,
						},
						TaggedResourceRequest: cloudscale.TaggedResourceRequest{
							Tags: &cloudscale.TagMap{
								antiAffinityTag: ps.AntiAffinityKey,
							},
						},
						Type: "anti-affinity",
					}),
				).Return(&cloudscale.ServerGroup{
					UUID: newSGUUID,
				}, nil)
				ss.EXPECT().Create(
					gomock.Any(),
					newDeepEqualMatcher(t, &cloudscale.ServerRequest{
						Name: machine.Name,
						ZonalResourceRequest: cloudscale.ZonalResourceRequest{
							Zone: zone,
						},
						TaggedResourceRequest: cloudscale.TaggedResourceRequest{
							Tags: ptr.To(cloudscale.TagMap{
								machineNameTag:      machine.Name,
								machineClusterIDTag: clusterID,
							}),
						},
						ServerGroups: []string{newSGUUID},
						SSHKeys:      []string{},
						Zone:         zone,
					}),
				).Return(&cloudscale.Server{}, nil)
			},
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()

			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			machine := &machinev1beta1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name: "app-test",
					Labels: map[string]string{
						machineClusterIDLabelName: "cluster-id",
					},
				},
			}
			providerSpec := csv1beta1.CloudscaleMachineProviderSpec{
				TokenSecret:     &corev1.LocalObjectReference{Name: "cloudscale-token"},
				Zone:            zone,
				AntiAffinityKey: "app",
			}
			setProviderSpecOnMachine(t, machine, &providerSpec)
			tokenSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: providerSpec.TokenSecret.Name,
				},
				Data: map[string][]byte{
					"token": []byte("my-cloudscale-token"),
				},
			}

			c := newFakeClient(t, machine, tokenSecret)
			ss := csmock.NewMockServerService(ctrl)
			sgs := csmock.NewMockServerGroupService(ctrl)
			actuator := newActuator(c, ss, sgs)

			tc.apiMock(t, machine, providerSpec, ss, sgs)

			require.NoError(t, actuator.Create(ctx, machine))
		})
	}
}

func Test_Actuator_Exists(t *testing.T) {
	t.Parallel()
	const clusterID = "cluster-id"

	tcs := []struct {
		name    string
		servers []cloudscale.Server
		exists  bool
	}{
		{
			name: "machine exists",
			servers: []cloudscale.Server{
				{
					Name: "app-test",
					TaggedResource: cloudscale.TaggedResource{
						Tags: cloudscale.TagMap{
							machineNameTag:      "app-test",
							machineClusterIDTag: clusterID,
						},
					},
				},
			},
			exists: true,
		},
		{
			name:    "machine does not exist",
			servers: []cloudscale.Server{},
			exists:  false,
		},
		{
			name: "machine has wrong cluster ID",
			servers: []cloudscale.Server{
				{
					Name: "app-test",
					TaggedResource: cloudscale.TaggedResource{
						Tags: cloudscale.TagMap{
							machineNameTag:      "app-test",
							machineClusterIDTag: "wrong-cluster-id",
						},
					},
				},
			},
			exists: false,
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()

			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			machine := &machinev1beta1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name: "app-test",
					Labels: map[string]string{
						machineClusterIDLabelName: clusterID,
					},
				},
			}
			providerSpec := csv1beta1.CloudscaleMachineProviderSpec{
				TokenSecret: &corev1.LocalObjectReference{Name: "cloudscale-token"},
			}
			setProviderSpecOnMachine(t, machine, &providerSpec)
			tokenSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: providerSpec.TokenSecret.Name,
				},
				Data: map[string][]byte{
					"token": []byte("my-cloudscale-token"),
				},
			}

			c := newFakeClient(t, machine, tokenSecret)
			ss := csmock.NewMockServerService(ctrl)
			sgs := csmock.NewMockServerGroupService(ctrl)
			actuator := newActuator(c, ss, sgs)

			ss.EXPECT().List(ctx, csTagMatcher{t: t, tags: map[string]string{
				machineNameTag: machine.Name,
			}}).Return(tc.servers, nil)

			exists, err := actuator.Exists(ctx, machine)
			require.NoError(t, err)
			assert.Equal(t, tc.exists, exists)
		})
	}
}

func Test_Actuator_Update(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	const clusterID = "cluster-id"

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	machine := &machinev1beta1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name: "app-test",
			Labels: map[string]string{
				machineClusterIDLabelName: clusterID,
			},
		},
	}
	providerSpec := csv1beta1.CloudscaleMachineProviderSpec{
		TokenSecret: &corev1.LocalObjectReference{Name: "cloudscale-token"},
	}
	setProviderSpecOnMachine(t, machine, &providerSpec)
	tokenSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: providerSpec.TokenSecret.Name,
		},
		Data: map[string][]byte{
			"token": []byte("my-cloudscale-token"),
		},
	}

	c := newFakeClient(t, machine, tokenSecret)
	ss := csmock.NewMockServerService(ctrl)
	sgs := csmock.NewMockServerGroupService(ctrl)
	actuator := newActuator(c, ss, sgs)

	ss.EXPECT().List(ctx, csTagMatcher{
		t: t,
		tags: map[string]string{
			machineNameTag: machine.Name,
		},
	}).Return([]cloudscale.Server{{
		UUID: "machine-uuid",
		TaggedResource: cloudscale.TaggedResource{
			Tags: cloudscale.TagMap{
				machineNameTag:      machine.Name,
				machineClusterIDTag: clusterID,
			},
		},
	}}, nil)

	require.NoError(t, actuator.Update(ctx, machine))

	var updatedMachine machinev1beta1.Machine
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(machine), &updatedMachine))
	if assert.NotNil(t, updatedMachine.Spec.ProviderID) {
		assert.Equal(t, "cloudscale://machine-uuid", *updatedMachine.Spec.ProviderID)
	}
}

func Test_Actuator_Delete(t *testing.T) {
	t.Parallel()

	const clusterID = "cluster-id"

	tcs := []struct {
		name    string
		apiMock func(*testing.T, *machinev1beta1.Machine, *csmock.MockServerService, *csmock.MockServerGroupService)
		err     error
	}{
		{
			name: "machine exists",
			apiMock: func(t *testing.T, machine *machinev1beta1.Machine, ss *csmock.MockServerService, sgs *csmock.MockServerGroupService) {
				ss.EXPECT().List(
					gomock.Any(),
					csTagMatcher{t: t, tags: map[string]string{
						machineNameTag: machine.Name,
					}},
				).Return([]cloudscale.Server{
					{
						UUID: "machine-uuid",
						TaggedResource: cloudscale.TaggedResource{
							Tags: cloudscale.TagMap{
								machineNameTag:      machine.Name,
								machineClusterIDTag: clusterID,
							},
						},
					},
				}, nil)
				ss.EXPECT().Delete(
					gomock.Any(),
					"machine-uuid",
				).Return(nil)
			},
		}, {
			name: "machine does not exist",
			apiMock: func(t *testing.T, machine *machinev1beta1.Machine, ss *csmock.MockServerService, sgs *csmock.MockServerGroupService) {
				ss.EXPECT().List(
					gomock.Any(),
					csTagMatcher{t: t, tags: map[string]string{
						machineNameTag: machine.Name,
					}},
				).Return([]cloudscale.Server{}, nil)
			},
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()

			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			machine := &machinev1beta1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name: "app-test",
					Labels: map[string]string{
						machineClusterIDLabelName: clusterID,
					},
				},
			}
			providerSpec := csv1beta1.CloudscaleMachineProviderSpec{
				TokenSecret: &corev1.LocalObjectReference{Name: "cloudscale-token"},
			}
			setProviderSpecOnMachine(t, machine, &providerSpec)
			tokenSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: providerSpec.TokenSecret.Name,
				},
				Data: map[string][]byte{
					"token": []byte("my-cloudscale-token"),
				},
			}

			c := newFakeClient(t, machine, tokenSecret)
			ss := csmock.NewMockServerService(ctrl)
			sgs := csmock.NewMockServerGroupService(ctrl)
			actuator := newActuator(c, ss, sgs)

			tc.apiMock(t, machine, ss, sgs)

			require.Equal(t, tc.err, actuator.Delete(ctx, machine))
		})
	}
}

// cloudscaleServerFromServerRequest returns a function that creates a cloudscale.Server from a cloudscale.ServerRequest
// The returned server can be modified by the callback function before being returned.
func cloudscaleServerFromServerRequest(cb func(*cloudscale.Server)) func(_ context.Context, req *cloudscale.ServerRequest) (*cloudscale.Server, error) {
	return func(_ context.Context, req *cloudscale.ServerRequest) (*cloudscale.Server, error) {
		reqIntfs := req.Interfaces
		if reqIntfs == nil {
			reqIntfs = &[]cloudscale.InterfaceRequest{}
		}
		intfs := []cloudscale.Interface{}
		for _, i := range *reqIntfs {
			sif := cloudscale.Interface{
				Network: cloudscale.NetworkStub{
					UUID: i.Network,
				},
			}
			if i.Addresses != nil {
				for _, a := range *i.Addresses {
					sif.Addresses = append(sif.Addresses, cloudscale.Address{
						Address: a.Address,
						Subnet: cloudscale.SubnetStub{
							UUID: a.Subnet,
						},
					})
				}
			}
			intfs = append(intfs, sif)
		}

		s := &cloudscale.Server{
			ZonalResource: cloudscale.ZonalResource{
				Zone: cloudscale.Zone{
					Slug: req.ZonalResourceRequest.Zone,
				},
			},
			TaggedResource: cloudscale.TaggedResource{
				Tags: ptr.Deref(req.TaggedResourceRequest.Tags, cloudscale.TagMap{}),
			},
			HREF:   "https://some-ref",
			UUID:   "UUID",
			Name:   req.Name,
			Status: "running",
			Flavor: cloudscale.Flavor{
				Slug: req.Flavor,
			},
			Image: cloudscale.Image{
				Slug: req.Image,
			},
			Interfaces: intfs,
		}
		cb(s)
		return s, nil
	}
}

// assertLogStub is a stub that implements the assert.TestingT interface but doe not fail the test. It only logs the error message to the test log.
type assertLogStub struct {
	t *testing.T
}

func (s assertLogStub) Errorf(format string, args ...interface{}) {
	s.t.Logf(format, args...)
}

func (c assertLogStub) FailNow() {
	panic("assertLogStub.FailNow called")
}

// DeepEqualMatcher uses testify/assert.Equal to compare the expected and actual values and print a meaningful error message if something fails
// It does not fail the test itself, but logs the error message to the test log.
type deepEqualMatcher struct {
	t    assert.TestingT
	comp any
}

// newDeepEqualMatcher creates a new deepEqualMatcher
func newDeepEqualMatcher(t *testing.T, comp any) *deepEqualMatcher {
	return &deepEqualMatcher{t: assertLogStub{t}, comp: comp}
}

func (m deepEqualMatcher) Matches(x any) bool {
	return assert.Equal(m.t, m.comp, x)
}

func (m deepEqualMatcher) String() string {
	return fmt.Sprint("is equal to", m.comp)
}

type csTagMatcher struct {
	t    *testing.T
	tags map[string]string
}

func (m csTagMatcher) Matches(x any) bool {
	tmf, ok := x.(cloudscale.ListRequestModifier)
	if !ok {
		m.t.Logf("expected cloudscale.ListRequestModifier, got %T", x)
		return false
	}

	req := httptest.NewRequest("GET", "http://example.com", nil)
	tmf(req)

	matches := true
	for key, value := range m.tags {
		matches = matches && assert.Contains(assertLogStub{m.t}, req.URL.RawQuery, fmt.Sprintf("tag%%3A%s=%s", key, value))
	}

	return matches
}

func (m csTagMatcher) String() string {
	return fmt.Sprint("matches function(*http.Request) adding to query:", m.tags)
}

func setProviderSpecOnMachine(t *testing.T, machine *machinev1beta1.Machine, providerSpec *csv1beta1.CloudscaleMachineProviderSpec) {
	t.Helper()

	ext, err := csv1beta1.RawExtensionFromProviderSpec(providerSpec)
	require.NoError(t, err)
	machine.Spec.ProviderSpec.Value = ext
}

func newActuator(c client.Client, ss cloudscale.ServerService, sgs cloudscale.ServerGroupService) *Actuator {
	return NewActuator(ActuatorParams{
		K8sClient:                 c,
		DefaultCloudscaleAPIToken: "",
		ServerClientFactory: func(token string) cloudscale.ServerService {
			return ss
		},
		ServerGroupClientFactory: func(token string) cloudscale.ServerGroupService {
			return sgs
		},
	})
}

var testScheme = func() *runtime.Scheme {
	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}
	scheme := runtime.NewScheme()
	must(clientgoscheme.AddToScheme(scheme))
	must(machinev1beta1.AddToScheme(scheme))
	return scheme
}()

func newFakeClient(t *testing.T, initObjs ...runtime.Object) client.Client {
	t.Helper()

	return fake.NewClientBuilder().
		WithScheme(testScheme).
		WithRuntimeObjects(initObjs...).
		WithStatusSubresource(
			&machinev1beta1.Machine{},
		).
		Build()
}
