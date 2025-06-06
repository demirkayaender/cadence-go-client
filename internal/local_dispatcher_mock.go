// Code generated by mockery v2.53.3. DO NOT EDIT.

package internal

import mock "github.com/stretchr/testify/mock"

// mockLocalDispatcher is an autogenerated mock type for the localDispatcher type
type mockLocalDispatcher struct {
	mock.Mock
}

type mockLocalDispatcher_Expecter struct {
	mock *mock.Mock
}

func (_m *mockLocalDispatcher) EXPECT() *mockLocalDispatcher_Expecter {
	return &mockLocalDispatcher_Expecter{mock: &_m.Mock}
}

// SendTask provides a mock function with given fields: task
func (_m *mockLocalDispatcher) SendTask(task *locallyDispatchedActivityTask) bool {
	ret := _m.Called(task)

	if len(ret) == 0 {
		panic("no return value specified for SendTask")
	}

	var r0 bool
	if rf, ok := ret.Get(0).(func(*locallyDispatchedActivityTask) bool); ok {
		r0 = rf(task)
	} else {
		r0 = ret.Get(0).(bool)
	}

	return r0
}

// mockLocalDispatcher_SendTask_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'SendTask'
type mockLocalDispatcher_SendTask_Call struct {
	*mock.Call
}

// SendTask is a helper method to define mock.On call
//   - task *locallyDispatchedActivityTask
func (_e *mockLocalDispatcher_Expecter) SendTask(task interface{}) *mockLocalDispatcher_SendTask_Call {
	return &mockLocalDispatcher_SendTask_Call{Call: _e.mock.On("SendTask", task)}
}

func (_c *mockLocalDispatcher_SendTask_Call) Run(run func(task *locallyDispatchedActivityTask)) *mockLocalDispatcher_SendTask_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(*locallyDispatchedActivityTask))
	})
	return _c
}

func (_c *mockLocalDispatcher_SendTask_Call) Return(_a0 bool) *mockLocalDispatcher_SendTask_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *mockLocalDispatcher_SendTask_Call) RunAndReturn(run func(*locallyDispatchedActivityTask) bool) *mockLocalDispatcher_SendTask_Call {
	_c.Call.Return(run)
	return _c
}

// newMockLocalDispatcher creates a new instance of mockLocalDispatcher. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func newMockLocalDispatcher(t interface {
	mock.TestingT
	Cleanup(func())
}) *mockLocalDispatcher {
	mock := &mockLocalDispatcher{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
