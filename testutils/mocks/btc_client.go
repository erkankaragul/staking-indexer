// Code generated by MockGen. DO NOT EDIT.
// Source: btcscanner/expected_btc_client.go

// Package mocks is a generated GoMock package.
package mocks

import (
	reflect "reflect"

	types "github.com/babylonchain/staking-indexer/types"
	gomock "github.com/golang/mock/gomock"
	"github.com/btcsuite/btcd/wire"
)

// MockClient is a mock of Client interface.
type MockClient struct {
	ctrl     *gomock.Controller
	recorder *MockClientMockRecorder
}

// MockClientMockRecorder is the mock recorder for MockClient.
type MockClientMockRecorder struct {
	mock *MockClient
}

// NewMockClient creates a new mock instance.
func NewMockClient(ctrl *gomock.Controller) *MockClient {
	mock := &MockClient{ctrl: ctrl}
	mock.recorder = &MockClientMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockClient) EXPECT() *MockClientMockRecorder {
	return m.recorder
}

// GetBlockByHeight mocks base method.
func (m *MockClient) GetBlockByHeight(height uint64) (*types.IndexedBlock, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetBlockByHeight", height)
	ret0, _ := ret[0].(*types.IndexedBlock)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetBlockByHeight indicates an expected call of GetBlockByHeight.
func (mr *MockClientMockRecorder) GetBlockByHeight(height interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetBlockByHeight", reflect.TypeOf((*MockClient)(nil).GetBlockByHeight), height)
}

// GetBlockHeaderByHeight mocks base method.
func (m *MockClient) GetBlockHeaderByHeight(height uint64) (*wire.BlockHeader, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetBlockHeaderByHeight", height)
	ret0, _ := ret[0].(*wire.BlockHeader)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetBlockHeaderByHeight indicates an expected call of GetBlockHeaderByHeight.
func (mr *MockClientMockRecorder) GetBlockHeaderByHeight(height interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetBlockHeaderByHeight", reflect.TypeOf((*MockClient)(nil).GetBlockHeaderByHeight), height)
}

// GetTipHeight mocks base method.
func (m *MockClient) GetTipHeight() (uint64, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetTipHeight")
	ret0, _ := ret[0].(uint64)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetTipHeight indicates an expected call of GetTipHeight.
func (mr *MockClientMockRecorder) GetTipHeight() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetTipHeight", reflect.TypeOf((*MockClient)(nil).GetTipHeight))
}
