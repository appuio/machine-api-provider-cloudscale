// Code generated by MockGen. DO NOT EDIT.
// Source: github.com/cloudscale-ch/cloudscale-go-sdk/v5 (interfaces: ServerGroupService)
//
// Generated by this command:
//
//	mockgen -destination=./csmock/server_group_service.go -package csmock github.com/cloudscale-ch/cloudscale-go-sdk/v5 ServerGroupService
//

// Package csmock is a generated GoMock package.
package csmock

import (
	context "context"
	reflect "reflect"

	cloudscale "github.com/cloudscale-ch/cloudscale-go-sdk/v5"
	gomock "go.uber.org/mock/gomock"
)

// MockServerGroupService is a mock of ServerGroupService interface.
type MockServerGroupService struct {
	ctrl     *gomock.Controller
	recorder *MockServerGroupServiceMockRecorder
	isgomock struct{}
}

// MockServerGroupServiceMockRecorder is the mock recorder for MockServerGroupService.
type MockServerGroupServiceMockRecorder struct {
	mock *MockServerGroupService
}

// NewMockServerGroupService creates a new mock instance.
func NewMockServerGroupService(ctrl *gomock.Controller) *MockServerGroupService {
	mock := &MockServerGroupService{ctrl: ctrl}
	mock.recorder = &MockServerGroupServiceMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockServerGroupService) EXPECT() *MockServerGroupServiceMockRecorder {
	return m.recorder
}

// Create mocks base method.
func (m *MockServerGroupService) Create(ctx context.Context, createRequest *cloudscale.ServerGroupRequest) (*cloudscale.ServerGroup, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Create", ctx, createRequest)
	ret0, _ := ret[0].(*cloudscale.ServerGroup)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// Create indicates an expected call of Create.
func (mr *MockServerGroupServiceMockRecorder) Create(ctx, createRequest any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Create", reflect.TypeOf((*MockServerGroupService)(nil).Create), ctx, createRequest)
}

// Delete mocks base method.
func (m *MockServerGroupService) Delete(ctx context.Context, serverGroupID string) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Delete", ctx, serverGroupID)
	ret0, _ := ret[0].(error)
	return ret0
}

// Delete indicates an expected call of Delete.
func (mr *MockServerGroupServiceMockRecorder) Delete(ctx, serverGroupID any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Delete", reflect.TypeOf((*MockServerGroupService)(nil).Delete), ctx, serverGroupID)
}

// Get mocks base method.
func (m *MockServerGroupService) Get(ctx context.Context, serverGroupID string) (*cloudscale.ServerGroup, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Get", ctx, serverGroupID)
	ret0, _ := ret[0].(*cloudscale.ServerGroup)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// Get indicates an expected call of Get.
func (mr *MockServerGroupServiceMockRecorder) Get(ctx, serverGroupID any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Get", reflect.TypeOf((*MockServerGroupService)(nil).Get), ctx, serverGroupID)
}

// List mocks base method.
func (m *MockServerGroupService) List(ctx context.Context, modifiers ...cloudscale.ListRequestModifier) ([]cloudscale.ServerGroup, error) {
	m.ctrl.T.Helper()
	varargs := []any{ctx}
	for _, a := range modifiers {
		varargs = append(varargs, a)
	}
	ret := m.ctrl.Call(m, "List", varargs...)
	ret0, _ := ret[0].([]cloudscale.ServerGroup)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// List indicates an expected call of List.
func (mr *MockServerGroupServiceMockRecorder) List(ctx any, modifiers ...any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	varargs := append([]any{ctx}, modifiers...)
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "List", reflect.TypeOf((*MockServerGroupService)(nil).List), varargs...)
}

// Update mocks base method.
func (m *MockServerGroupService) Update(ctx context.Context, networkID string, updateRequest *cloudscale.ServerGroupRequest) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Update", ctx, networkID, updateRequest)
	ret0, _ := ret[0].(error)
	return ret0
}

// Update indicates an expected call of Update.
func (mr *MockServerGroupServiceMockRecorder) Update(ctx, networkID, updateRequest any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Update", reflect.TypeOf((*MockServerGroupService)(nil).Update), ctx, networkID, updateRequest)
}