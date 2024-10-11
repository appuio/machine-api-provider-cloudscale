package machine

import (
	"context"

	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
)

// Actuator is responsible for performing machine reconciliation.
type Actuator struct {
}

// ActuatorParams holds parameter information for Actuator.
type ActuatorParams struct {
}

// NewActuator returns an actuator.
func NewActuator(params ActuatorParams) *Actuator {
	return &Actuator{}
}

// Create creates a machine and is invoked by the machine controller.
func (a *Actuator) Create(ctx context.Context, machine *machinev1beta1.Machine) error {
	panic("not implemented")
}

func (a *Actuator) Exists(ctx context.Context, machine *machinev1beta1.Machine) (bool, error) {
	panic("not implemented")
}

func (a *Actuator) Update(ctx context.Context, machine *machinev1beta1.Machine) error {
	panic("not implemented")
}

func (a *Actuator) Delete(ctx context.Context, machine *machinev1beta1.Machine) error {
	panic("not implemented")
}
