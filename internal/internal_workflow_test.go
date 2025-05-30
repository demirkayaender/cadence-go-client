// Copyright (c) 2017 Uber Technologies, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package internal

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"go.uber.org/cadence/internal/common/testlogger"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"go.uber.org/cadence/internal/common/metrics"
)

type WorkflowUnitTest struct {
	suite.Suite
	WorkflowTestSuite
	activityOptions ActivityOptions
}

func (s *WorkflowUnitTest) SetupSuite() {
	s.activityOptions = ActivityOptions{
		ScheduleToStartTimeout: time.Minute,
		StartToCloseTimeout:    time.Minute,
		HeartbeatTimeout:       20 * time.Second,
	}
}
func (s *WorkflowUnitTest) SetupTest() {
	s.SetLogger(testlogger.NewZap(s.T()))
}

func TestWorkflowUnitTest(t *testing.T) {
	suite.Run(t, new(WorkflowUnitTest))
}

func worldWorkflow(ctx Context, input string) (result string, err error) {
	return input + " World!", nil
}

func (s *WorkflowUnitTest) Test_WorldWorkflow() {
	env := newTestWorkflowEnv(s.T())
	env.ExecuteWorkflow(worldWorkflow, "Hello")
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
}

func helloWorldAct(ctx context.Context) (string, error) {
	s := ctx.Value(unitTestKey).(*WorkflowUnitTest)
	info := GetActivityInfo(ctx)
	s.Equal(tasklist, info.TaskList)
	s.Equal(2*time.Second, info.HeartbeatTimeout)
	return "test", nil
}

type key int

const unitTestKey key = 1

func singleActivityWorkflowWithOptions(s *WorkflowUnitTest, ao ActivityOptions) error {
	helloWorldActivityWorkflow := func(ctx Context, input string) (result string, err error) {
		ctx1 := WithActivityOptions(ctx, ao)
		f := ExecuteActivity(ctx1, helloWorldAct)
		var r1 string
		err = f.Get(ctx, &r1)
		if err != nil {
			return "", err
		}
		return r1, nil
	}

	env := newTestWorkflowEnv(s.T())
	ctx := context.WithValue(context.Background(), unitTestKey, s)
	env.SetWorkerOptions(WorkerOptions{BackgroundActivityContext: ctx})
	env.RegisterActivity(helloWorldAct)
	env.ExecuteWorkflow(helloWorldActivityWorkflow, "Hello")
	s.True(env.IsWorkflowCompleted())
	return env.GetWorkflowError()
}

func (s *WorkflowUnitTest) Test_SingleActivityWorkflow() {
	ao := ActivityOptions{
		ScheduleToStartTimeout: 10 * time.Second,
		StartToCloseTimeout:    5 * time.Second,
		HeartbeatTimeout:       2 * time.Second,
		ActivityID:             "id1",
		TaskList:               tasklist,
	}
	err := singleActivityWorkflowWithOptions(s, ao)
	s.NoError(err)
}

func (s *WorkflowUnitTest) Test_SingleActivityWorkflowIsErrorMessagesMatched() {
	testCases := []struct {
		name                   string
		ScheduleToStartTimeout time.Duration
		StartToCloseTimeout    time.Duration
		ScheduleToCloseTimeout time.Duration
		expectedErrorMessage   string
	}{
		{
			name:                   "ZeroScheduleToStartTimeout",
			ScheduleToStartTimeout: 0 * time.Second,
			StartToCloseTimeout:    5 * time.Second,
			ScheduleToCloseTimeout: 0 * time.Second,
			expectedErrorMessage:   "missing or negative ScheduleToStartTimeoutSeconds",
		},
		{
			name:                   "ZeroStartToCloseTimeout",
			ScheduleToStartTimeout: 10 * time.Second,
			StartToCloseTimeout:    0 * time.Second,
			ScheduleToCloseTimeout: 0 * time.Second,
			expectedErrorMessage:   "missing or negative StartToCloseTimeoutSeconds",
		},
		{
			name:                   "NegativeScheduleToCloseTimeout",
			ScheduleToStartTimeout: 10 * time.Second,
			StartToCloseTimeout:    5 * time.Second,
			ScheduleToCloseTimeout: -1 * time.Second,
			expectedErrorMessage:   "invalid negative ScheduleToCloseTimeoutSeconds",
		},
	}

	for _, testCase := range testCases {
		ao := ActivityOptions{
			ScheduleToStartTimeout: testCase.ScheduleToStartTimeout,
			StartToCloseTimeout:    testCase.StartToCloseTimeout,
			ScheduleToCloseTimeout: testCase.ScheduleToCloseTimeout,
			HeartbeatTimeout:       2 * time.Second,
			ActivityID:             "id1",
			TaskList:               tasklist,
		}
		s.Run(testCase.name, func() {
			err := singleActivityWorkflowWithOptions(s, ao)
			s.ErrorContains(err, testCase.expectedErrorMessage)
		})
	}
}

func splitJoinActivityWorkflow(ctx Context, testPanic bool) (result string, err error) {
	var result1, result2 string
	var err1, err2 error

	ao := ActivityOptions{
		ScheduleToStartTimeout: 10 * time.Second,
		StartToCloseTimeout:    5 * time.Second,
	}
	ctx = WithActivityOptions(ctx, ao)

	c1 := NewChannel(ctx)
	c2 := NewChannel(ctx)
	Go(ctx, func(ctx Context) {
		ao.ActivityID = "id1"
		ctx1 := WithActivityOptions(ctx, ao)
		f := ExecuteActivity(ctx1, testAct)
		err1 = f.Get(ctx, &result1)
		if err1 == nil {
			c1.Send(ctx, true)
		}
	})
	Go(ctx, func(ctx Context) {
		ao.ActivityID = "id2"
		ctx2 := WithActivityOptions(ctx, ao)
		f := ExecuteActivity(ctx2, testAct)
		err1 := f.Get(ctx, &result2)
		if testPanic {
			panic("simulated")
		}
		if err1 == nil {
			c2.Send(ctx, true)
		}
	})

	c1.Receive(ctx, nil)
	// Use selector to test it
	selected := false
	NewSelector(ctx).AddReceive(c2, func(c Channel, more bool) {
		if !more {
			panic("more should be true")
		}
		selected = true
	}).Select(ctx)
	if !selected {
		return "", errors.New("selector does not work")
	}
	if err1 != nil {
		return "", err1
	}
	if err2 != nil {
		return "", err2
	}

	return result1 + result2, nil
}

func returnPanicWorkflow(ctx Context) (err error) {
	return newPanicError("panicError", "stackTrace")
}

func (s *WorkflowUnitTest) Test_SplitJoinActivityWorkflow() {
	env := newTestWorkflowEnv(s.T())
	env.RegisterWorkflowWithOptions(splitJoinActivityWorkflow, RegisterWorkflowOptions{Name: "splitJoinActivityWorkflow"})
	env.RegisterActivityWithOptions(testAct, RegisterActivityOptions{Name: "testActivityWithOptions"})
	env.OnActivity(testAct, mock.Anything).Return(func(ctx context.Context) (string, error) {
		activityID := GetActivityInfo(ctx).ActivityID
		switch activityID {
		case "id1":
			return "Hello", nil
		case "id2":
			return " Flow!", nil
		default:
			panic(fmt.Sprintf("Unexpected activityID: %v", activityID))
		}
	}).Twice()
	tracer := tracingInterceptorFactory{}
	env.SetWorkerOptions(WorkerOptions{WorkflowInterceptorChainFactories: []WorkflowInterceptorFactory{&tracer}})
	env.ExecuteWorkflow(splitJoinActivityWorkflow, false)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	env.AssertExpectations(s.T())
	var result string
	env.GetWorkflowResult(&result)
	s.Equal("Hello Flow!", result)
	s.Equal(1, len(tracer.instances))
	trace := tracer.instances[len(tracer.instances)-1].trace
	s.Equal([]string{
		"ExecuteWorkflow splitJoinActivityWorkflow begin",
		"ExecuteActivity testActivityWithOptions",
		"ExecuteActivity testActivityWithOptions",
		"ExecuteWorkflow splitJoinActivityWorkflow end",
	}, trace)
}

func TestWorkflowPanic(t *testing.T) {
	env := newTestWorkflowEnv(t)
	env.RegisterActivity(testAct)
	env.ExecuteWorkflow(splitJoinActivityWorkflow, true)
	require.True(t, env.IsWorkflowCompleted())
	require.NotNil(t, env.GetWorkflowError())
	resultErr := env.GetWorkflowError().(*PanicError)
	require.EqualValues(t, "simulated", resultErr.Error())
	require.Contains(t, resultErr.StackTrace(), "cadence/internal.splitJoinActivityWorkflow")
}

func TestWorkflowReturnsPanic(t *testing.T) {
	env := newTestWorkflowEnv(t)
	env.ExecuteWorkflow(returnPanicWorkflow)
	require.True(t, env.IsWorkflowCompleted())
	require.NotNil(t, env.GetWorkflowError())
	resultErr := env.GetWorkflowError().(*PanicError)
	require.EqualValues(t, "panicError", resultErr.Error())
	require.EqualValues(t, "stackTrace", resultErr.StackTrace())
}

func testClockWorkflow(ctx Context) (time.Time, error) {
	c := Now(ctx)
	return c, nil
}

func (s *WorkflowUnitTest) Test_ClockWorkflow() {
	env := newTestWorkflowEnv(s.T())
	env.ExecuteWorkflow(testClockWorkflow)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	var nowTime time.Time
	env.GetWorkflowResult(&nowTime)
	s.False(nowTime.IsZero())
}

type testTimerWorkflow struct {
	t *testing.T
}

func (w *testTimerWorkflow) Execute(ctx Context, input []byte) (result []byte, err error) {
	// Start a timer.
	t := NewTimer(ctx, 1)

	isWokeByTimer := false

	NewSelector(ctx).AddFuture(t, func(f Future) {
		err := f.Get(ctx, nil)
		require.NoError(w.t, err)
		isWokeByTimer = true
	}).Select(ctx)

	require.True(w.t, isWokeByTimer)

	// Start a timer and cancel it.
	ctx2, c2 := WithCancel(ctx)
	t2 := NewTimer(ctx2, 1)
	c2()
	err2 := t2.Get(ctx2, nil)

	require.Error(w.t, err2)
	_, isCancelErr := err2.(*CanceledError)
	require.True(w.t, isCancelErr)

	// Sleep 1 sec
	ctx3, _ := WithCancel(ctx)
	err3 := Sleep(ctx3, 1)
	require.NoError(w.t, err3)

	// Sleep and cancel.
	ctx4, c4 := WithCancel(ctx)
	c4()
	err4 := Sleep(ctx4, 1)

	require.Error(w.t, err4)
	_, isCancelErr = err4.(*CanceledError)
	require.True(w.t, isCancelErr)

	return []byte("workflow-completed"), nil
}

func TestTimerWorkflow(t *testing.T) {
	env := newTestWorkflowEnv(t)
	w := &testTimerWorkflow{t: t}
	env.RegisterWorkflow(w.Execute)
	env.ExecuteWorkflow(w.Execute, []byte{1, 2})
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

type testActivityCancelWorkflow struct {
	t *testing.T
}

func testAct(ctx context.Context) (string, error) {
	return "test", nil
}

func (w *testActivityCancelWorkflow) Execute(ctx Context, input []byte) (result []byte, err error) {
	ao := ActivityOptions{
		ScheduleToStartTimeout: 10 * time.Second,
		StartToCloseTimeout:    5 * time.Second,
	}
	ctx = WithActivityOptions(ctx, ao)

	// Sync cancellation
	ctx1, c1 := WithCancel(ctx)
	defer c1()

	ao.ActivityID = "id1"
	ctx1 = WithActivityOptions(ctx1, ao)
	f := ExecuteActivity(ctx1, testAct)
	var res1 string
	err1 := f.Get(ctx, &res1)
	require.NoError(w.t, err1, err1)
	require.Equal(w.t, res1, "test")

	// Async Cancellation (Callback completes before cancel)
	ctx2, c2 := WithCancel(ctx)
	ao.ActivityID = "id2"
	ctx2 = WithActivityOptions(ctx2, ao)
	f = ExecuteActivity(ctx2, testAct)
	c2()
	var res2 string
	err2 := f.Get(ctx, &res2)
	require.NotNil(w.t, err2)
	_, ok := err2.(*CanceledError)
	require.True(w.t, ok)
	return []byte("workflow-completed"), nil
}

func TestActivityCancellation(t *testing.T) {
	env := newTestWorkflowEnv(t)
	env.RegisterActivity(testAct)
	w := &testActivityCancelWorkflow{t: t}
	env.RegisterWorkflow(w.Execute)
	env.ExecuteWorkflow(w.Execute, []byte{1, 2})
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

type sayGreetingActivityRequest struct {
	Name     string
	Greeting string
}

func getGreetingActivity() (string, error) {
	return "Hello", nil
}
func getNameActivity() (string, error) {
	return "cadence", nil
}
func sayGreetingActivity(input *sayGreetingActivityRequest) (string, error) {
	return fmt.Sprintf("%v %v!", input.Greeting, input.Name), nil
}

// Greetings Workflow Decider.
func greetingsWorkflow(ctx Context) (result string, err error) {
	// Get Greeting.
	ao := ActivityOptions{
		ScheduleToStartTimeout: 10 * time.Second,
		StartToCloseTimeout:    5 * time.Second,
	}
	ctx1 := WithActivityOptions(ctx, ao)

	f := ExecuteActivity(ctx1, getGreetingActivity)
	var greetResult string
	err = f.Get(ctx, &greetResult)
	if err != nil {
		return "", err
	}

	// Get Name.
	f = ExecuteActivity(ctx1, getNameActivity)
	var nameResult string
	err = f.Get(ctx, &nameResult)
	if err != nil {
		return "", err
	}

	// Say Greeting.
	request := &sayGreetingActivityRequest{Name: nameResult, Greeting: greetResult}
	err = ExecuteActivity(ctx1, sayGreetingActivity, request).Get(ctx, &result)
	if err != nil {
		return "", err
	}
	return result, nil
}

func (s *WorkflowUnitTest) Test_ExternalExampleWorkflow() {
	env := newTestWorkflowEnv(s.T())
	env.RegisterActivity(getGreetingActivity)
	env.RegisterActivity(getNameActivity)
	env.RegisterActivity(sayGreetingActivity)

	env.ExecuteWorkflow(greetingsWorkflow)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	var result string
	env.GetWorkflowResult(&result)
	s.Equal("Hello cadence!", result)
}

func continueAsNewWorkflowTest(ctx Context) error {
	return NewContinueAsNewError(ctx, "continueAsNewWorkflowTest", []byte("start"))
}

func (s *WorkflowUnitTest) Test_ContinueAsNewWorkflow() {
	env := newTestWorkflowEnv(s.T())
	env.ExecuteWorkflow(continueAsNewWorkflowTest)
	s.True(env.IsWorkflowCompleted())
	s.NotNil(env.GetWorkflowError())
	resultErr := env.GetWorkflowError().(*ContinueAsNewError)
	s.EqualValues("continueAsNewWorkflowTest", resultErr.params.workflowType.Name)
	s.EqualValues(1, *resultErr.params.executionStartToCloseTimeoutSeconds)
	s.EqualValues(1, *resultErr.params.taskStartToCloseTimeoutSeconds)
	s.EqualValues("default-test-tasklist", *resultErr.params.taskListName)
}

func cancelWorkflowTest(ctx Context) (string, error) {
	if ctx.Done().Receive(ctx, nil); ctx.Err() == ErrCanceled {
		return "Cancelled.", ctx.Err()
	}
	return "Completed.", nil
}

func (s *WorkflowUnitTest) Test_CancelWorkflow() {
	env := newTestWorkflowEnv(s.T())
	env.RegisterDelayedCallback(func() {
		env.CancelWorkflow()
	}, time.Hour)
	env.ExecuteWorkflow(cancelWorkflowTest)
	s.True(env.IsWorkflowCompleted(), "Workflow failed to complete")
}

func cancelWorkflowAfterActivityTest(ctx Context) ([]byte, error) {
	// The workflow cancellation should handle activity and timer cancellation
	// not to propagate those decisions.

	// schedule an activity.
	ao := ActivityOptions{
		ScheduleToStartTimeout: 10 * time.Second,
		StartToCloseTimeout:    5 * time.Second,
	}
	ctx = WithActivityOptions(ctx, ao)

	err := ExecuteActivity(ctx, testAct).Get(ctx, nil)
	if err != nil {
		return nil, err
	}

	// schedule a timer
	err2 := Sleep(ctx, 1)
	if err2 != nil {
		return nil, err2
	}

	if ctx.Done().Receive(ctx, nil); ctx.Err() == ErrCanceled {
		return []byte("Cancelled."), ctx.Err()
	}
	return []byte("Completed."), nil
}

func (s *WorkflowUnitTest) Test_CancelWorkflowAfterActivity() {
	env := newTestWorkflowEnv(s.T())
	env.RegisterDelayedCallback(func() {
		env.CancelWorkflow()
	}, time.Hour)
	env.ExecuteWorkflow(cancelWorkflowAfterActivityTest)
	s.True(env.IsWorkflowCompleted())
}

func signalWorkflowTest(ctx Context) ([]byte, error) {
	// read multiple times.
	var result string
	ch := GetSignalChannel(ctx, "testSig1")
	var v string
	ok := ch.ReceiveAsync(&v)
	if !ok {
		return nil, errors.New("testSig1 not received")
	}
	result += v
	ch.Receive(ctx, &v)
	result += v

	// Read on a selector.
	ch2 := GetSignalChannel(ctx, "testSig2")
	s := NewSelector(ctx)
	s.AddReceive(ch2, func(c Channel, more bool) {
		c.Receive(ctx, &v)
		result += v
	})
	s.Select(ctx)
	s.Select(ctx)
	s.Select(ctx)

	// Read on a selector inside the callback, multiple times.
	ch2 = GetSignalChannel(ctx, "testSig2")
	s = NewSelector(ctx)
	s.AddReceive(ch2, func(c Channel, more bool) {
		for i := 0; i < 4; i++ {
			c.Receive(ctx, &v)
			result += v
		}
	})
	s.Select(ctx)

	// Check un handled signals.
	list := getWorkflowEnvOptions(ctx).getUnhandledSignalNames()
	if len(list) != 1 || list[0] != "testSig3" {
		panic("expecting one unhandled signal")
	}
	ch3 := GetSignalChannel(ctx, "testSig3")
	ch3.Receive(ctx, &v)
	result += v
	list = getWorkflowEnvOptions(ctx).getUnhandledSignalNames()
	if len(list) != 0 {
		panic("expecting no unhandled signals")
	}
	return []byte(result), nil
}

func (s *WorkflowUnitTest) Test_SignalWorkflow() {
	expected := []string{
		"Sig1Value1;",
		"Sig1Value2;",
		"Sig2Value1;",
		"Sig2Value2;",
		"Sig2Value3;",
		"Sig2Value4;",
		"Sig2Value5;",
		"Sig2Value6;",
		"Sig2Value7;",
		"Sig3Value1;",
	}
	env := newTestWorkflowEnv(s.T())

	// Setup signals.
	for i := 0; i < 2; i++ {
		msg := expected[i]
		var delay time.Duration
		if i > 0 {
			delay = time.Second
		}
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow("testSig1", msg)
		}, delay)
	}
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("testSig3", expected[9])
	}, time.Hour)
	for i := 2; i < 9; i++ {
		msg := expected[i]
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow("testSig2", msg)
		}, time.Hour)
	}

	env.ExecuteWorkflow(signalWorkflowTest)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	var result []byte
	env.GetWorkflowResult(&result)
	s.EqualValues(strings.Join(expected, ""), string(result))
}

type message struct {
	Value string
}

func receiveCorruptSignalWorkflowTest(ctx Context) ([]message, error) {
	ch := GetSignalChannel(ctx, "channelExpectingTypeMessage")
	var result []message
	var m message
	ch.Receive(ctx, &m)
	result = append(result, m)
	return result, nil
}

func receiveCorruptSignalOnClosedChannelWorkflowTest(ctx Context) ([]message, error) {
	ch := GetSignalChannel(ctx, "channelExpectingTypeMessage")
	var result []message
	var m message
	ch.Close()
	more := ch.Receive(ctx, &m)

	result = append(result, message{Value: fmt.Sprintf("%v", more)})
	return result, nil
}

func receiveWithSelectorCorruptSignalWorkflowTest(ctx Context) ([]message, error) {
	var result []message

	// Read on a selector
	ch := GetSignalChannel(ctx, "channelExpectingTypeMessage")
	s := NewSelector(ctx)
	s.AddReceive(ch, func(c Channel, more bool) {
		var m message
		ch.Receive(ctx, &m)
		result = append(result, m)
	})
	s.Select(ctx)
	return result, nil
}

func receiveAsyncCorruptSignalOnClosedChannelWorkflowTest(ctx Context) ([]int, error) {
	ch := GetSignalChannel(ctx, "channelExpectingInt")
	var result []int
	var m int

	ch.SendAsync("wrong")
	ch.Close()
	ok := ch.ReceiveAsync(&m)
	if ok == true {
		result = append(result, m)
	}

	return result, nil
}

func receiveAsyncCorruptSignalWorkflowTest(ctx Context) ([]message, error) {
	ch := GetSignalChannel(ctx, "channelExpectingTypeMessage")
	var result []message
	var m message

	ch.SendAsync("wrong")
	ok := ch.ReceiveAsync(&m)
	if ok == true {
		result = append(result, m)
	}

	ch.SendAsync("wrong again")
	ch.SendAsync(message{
		Value: "the right interface",
	})
	ok = ch.ReceiveAsync(&m)
	if ok == true {
		result = append(result, m)
	}
	return result, nil
}

func (s *WorkflowUnitTest) Test_CorruptedSignalWorkflow_ShouldLogMetricsAndNotPanic() {
	scope, closer, reporter := metrics.NewTaggedMetricsScope()
	s.SetMetricsScope(scope)
	env := s.NewTestWorkflowEnvironment()
	env.Test(s.T())

	// Setup signals.
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("channelExpectingTypeMessage", "wrong")
	}, time.Millisecond)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("channelExpectingTypeMessage", message{
			Value: "the right interface",
		})
	}, time.Second)

	env.ExecuteWorkflow(receiveCorruptSignalWorkflowTest)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	var result []message
	env.GetWorkflowResult(&result)

	s.EqualValues(1, len(result))
	s.EqualValues("the right interface", result[0].Value)

	closer.Close()
	counts := reporter.Counts()
	s.EqualValues(1, len(counts))
	s.EqualValues(metrics.CorruptedSignalsCounter, counts[0].Name())
	s.EqualValues(1, counts[0].Value())
}

func (s *WorkflowUnitTest) Test_CorruptedSignalWorkflow_OnSelectorRead_ShouldLogMetricsAndNotPanic() {
	scope, closer, reporter := metrics.NewTaggedMetricsScope()
	s.SetMetricsScope(scope)
	env := s.NewTestWorkflowEnvironment()
	env.Test(s.T())

	// Setup signals.
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("channelExpectingTypeMessage", "wrong")
	}, time.Second)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("channelExpectingTypeMessage", message{
			Value: "the right interface",
		})
	}, 3*time.Second)

	env.ExecuteWorkflow(receiveWithSelectorCorruptSignalWorkflowTest)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	var result []message
	env.GetWorkflowResult(&result)

	s.EqualValues(1, len(result))
	s.EqualValues("the right interface", result[0].Value)

	closer.Close()
	counts := reporter.Counts()
	s.EqualValues(1, len(counts))
	s.EqualValues(metrics.CorruptedSignalsCounter, counts[0].Name())
	s.EqualValues(1, counts[0].Value())
}

func (s *WorkflowUnitTest) Test_CorruptedSignalWorkflow_ReceiveAsync_ShouldLogMetricsAndNotPanic() {
	scope, closer, reporter := metrics.NewTaggedMetricsScope()
	s.SetMetricsScope(scope)
	env := s.NewTestWorkflowEnvironment()
	env.Test(s.T())

	env.ExecuteWorkflow(receiveAsyncCorruptSignalWorkflowTest)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	var result []message
	env.GetWorkflowResult(&result)
	s.EqualValues(1, len(result))
	s.EqualValues("the right interface", result[0].Value)

	closer.Close()
	counts := reporter.Counts()
	s.EqualValues(1, len(counts))
	s.EqualValues(metrics.CorruptedSignalsCounter, counts[0].Name())
	s.EqualValues(2, counts[0].Value())
}

func (s *WorkflowUnitTest) Test_CorruptedSignalOnClosedChannelWorkflow_ReceiveAsync_ShouldComplete() {
	env := newTestWorkflowEnv(s.T())

	env.ExecuteWorkflow(receiveAsyncCorruptSignalOnClosedChannelWorkflowTest)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	var result []message
	env.GetWorkflowResult(&result)
	s.EqualValues(0, len(result))
}

func (s *WorkflowUnitTest) Test_CorruptedSignalOnClosedChannelWorkflow_Receive_ShouldComplete() {
	env := newTestWorkflowEnv(s.T())

	// Setup signals.
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("channelExpectingTypeMessage", "wrong")
	}, time.Second)

	env.ExecuteWorkflow(receiveCorruptSignalOnClosedChannelWorkflowTest)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	var result []message
	env.GetWorkflowResult(&result)
	s.EqualValues(1, len(result))
	s.Equal("false", result[0].Value)
}

func closeChannelTest(ctx Context) error {
	ch := NewChannel(ctx)
	Go(ctx, func(ctx Context) {
		var dummy struct{}
		ch.Receive(ctx, &dummy)
		ch.Close()
	})

	ch.Send(ctx, struct{}{})
	return nil
}

func (s *WorkflowUnitTest) Test_CloseChannelWorkflow() {
	env := newTestWorkflowEnv(s.T())
	env.ExecuteWorkflow(closeChannelTest)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
}

func closeChannelInSelectTest(ctx Context) error {
	s := NewSelector(ctx)
	sendCh := NewChannel(ctx)
	receiveCh := NewChannel(ctx)
	expectedValue := "expected value"

	Go(ctx, func(ctx Context) {
		sendCh.Close()
		receiveCh.Send(ctx, expectedValue)
	})

	var v string
	s.AddSend(sendCh, struct{}{}, func() {
		panic("callback for sendCh should not be executed")
	})
	s.AddReceive(receiveCh, func(c Channel, m bool) {
		c.Receive(ctx, &v)
	})
	s.Select(ctx)
	if v != expectedValue {
		panic("callback for receiveCh is not executed")
	}
	return nil
}

func (s *WorkflowUnitTest) Test_CloseChannelInSelectWorkflow() {
	env := newTestWorkflowEnv(s.T())
	env.ExecuteWorkflow(closeChannelInSelectTest)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
}

func bufferedChanWorkflowTest(ctx Context, bufferSize int) error {
	bufferedCh := NewBufferedChannel(ctx, bufferSize)

	Go(ctx, func(ctx Context) {
		var dummy int
		for i := 0; i < bufferSize; i++ {
			bufferedCh.Receive(ctx, &dummy)
		}
	})

	for i := 0; i < bufferSize+1; i++ {
		bufferedCh.Send(ctx, i)
	}
	return nil
}

func (s *WorkflowUnitTest) Test_BufferedChanWorkflow() {
	bufferSizeList := []int{1, 5}
	for _, bufferSize := range bufferSizeList {
		env := newTestWorkflowEnv(s.T())
		env.ExecuteWorkflow(bufferedChanWorkflowTest, bufferSize)
		s.True(env.IsWorkflowCompleted())
		s.NoError(env.GetWorkflowError())
	}
}

func bufferedChanWithSelectorWorkflowTest(ctx Context, bufferSize int) error {
	bufferedCh := NewBufferedChannel(ctx, bufferSize)
	selectedCh := NewChannel(ctx)
	done := NewChannel(ctx)
	var dummy struct{}

	// 1. First we need to fill the buffer
	for i := 0; i < bufferSize; i++ {
		bufferedCh.Send(ctx, dummy)
	}

	// DO NOT change the order of these coroutines.
	Go(ctx, func(ctx Context) {
		// 3. Add another send callback to bufferedCh's blockedSends.
		bufferedCh.Send(ctx, dummy)
		done.Send(ctx, dummy)
	})

	Go(ctx, func(ctx Context) {
		// 4.  Make sure selectedCh is selected
		selectedCh.Receive(ctx, nil)

		// 5. Get a value from channel buffer. Receive call will also check if there's any blocked sends.
		// The first blockedSends is added by Select(). Since bufferedCh is not selected, it's fn() will
		// return false. The Receive call should continue to check other blockedSends, until fn() returns
		// true or the list is empty. In this case, it will move the value sent in step 3 into buffer
		// and thus unblocks it.
		bufferedCh.Receive(ctx, nil)
	})

	selector := NewSelector(ctx)
	selector.AddSend(selectedCh, dummy, func() {})
	selector.AddSend(bufferedCh, dummy, func() {})
	// 2. When select is called, callback for the second send will be added to bufferedCh's blockedSends
	selector.Select(ctx)

	// Make sure no coroutine blocks
	done.Receive(ctx, nil)
	return nil
}

func (s *WorkflowUnitTest) Test_BufferedChanWithSelectorWorkflow() {
	bufferSizeList := []int{1, 5}
	for _, bufferSize := range bufferSizeList {
		bufferSize := bufferSize
		env := newTestWorkflowEnv(s.T())
		env.ExecuteWorkflow(bufferedChanWithSelectorWorkflowTest, bufferSize)
		s.True(env.IsWorkflowCompleted())
		s.NoError(env.GetWorkflowError())
	}
}

func activityOptionsWorkflow(ctx Context) (result string, err error) {
	ao1 := ActivityOptions{
		ActivityID: "id1",
	}
	ao2 := ActivityOptions{
		ActivityID: "id2",
	}
	ctx1 := WithActivityOptions(ctx, ao1)
	ctx2 := WithActivityOptions(ctx, ao2)

	ctx1Ao := getActivityOptions(ctx1)
	ctx2Ao := getActivityOptions(ctx2)
	return *ctx1Ao.ActivityID + " " + *ctx2Ao.ActivityID, nil
}

// Test that activity options are correctly spawned with WithActivityOptions is called.
// See https://github.com/uber-go/cadence-client/issues/372
func (s *WorkflowUnitTest) Test_ActivityOptionsWorkflow() {
	env := newTestWorkflowEnv(s.T())
	env.ExecuteWorkflow(activityOptionsWorkflow)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	var result string
	env.GetWorkflowResult(&result)
	s.Equal("id1 id2", result)
}

const (
	memoTestKey = "testKey"
	memoTestVal = "testVal"
)

func getMemoTest(ctx Context) (result string, err error) {
	info := GetWorkflowInfo(ctx)
	val, ok := info.Memo.Fields[memoTestKey]
	if !ok {
		return "", errors.New("no memo found")
	}
	err = NewValue(val).Get(&result)
	return result, err
}

func (s *WorkflowUnitTest) Test_MemoWorkflow() {
	env := newTestWorkflowEnv(s.T())
	memo := map[string]interface{}{
		memoTestKey: memoTestVal,
	}
	err := env.SetMemoOnStart(memo)
	s.NoError(err)

	env.ExecuteWorkflow(getMemoTest)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	var result string
	env.GetWorkflowResult(&result)
	s.Equal(memoTestVal, result)
}

func sleepWorkflow(ctx Context, input time.Duration) (int, error) {
	if err := Sleep(ctx, input); err != nil {
		return 0, err
	}

	return 1, nil
}

func waitGroupWorkflowTest(ctx Context, n int) (int, error) {
	ctx = WithChildWorkflowOptions(ctx, ChildWorkflowOptions{
		ExecutionStartToCloseTimeout: time.Second * 30,
	})

	var err error
	results := make([]int, 0, n)
	waitGroup := NewWaitGroup(ctx)
	for i := 0; i < n; i++ {
		waitGroup.Add(1)
		t := time.Second * time.Duration(i+1)
		Go(ctx, func(ctx Context) {
			var result int
			err = ExecuteChildWorkflow(ctx, sleepWorkflow, t).Get(ctx, &result)
			results = append(results, result)
			waitGroup.Done()
		})
	}

	waitGroup.Wait(ctx)
	if err != nil {
		return 0, err
	}

	sum := 0
	for _, v := range results {
		sum = sum + v
	}

	return sum, nil
}

func waitGroupWaitForMWorkflowTest(ctx Context, n int, m int) (int, error) {
	ctx = WithChildWorkflowOptions(ctx, ChildWorkflowOptions{
		ExecutionStartToCloseTimeout: time.Second * 30,
	})

	var err error
	results := make([]int, 0, n)
	waitGroup := NewWaitGroup(ctx)
	waitGroup.Add(m)
	for i := 0; i < n; i++ {
		t := time.Second * time.Duration(i+1)
		Go(ctx, func(ctx Context) {
			var result int
			err = ExecuteChildWorkflow(ctx, sleepWorkflow, t).Get(ctx, &result)
			results = append(results, result)
			waitGroup.Done()
		})
	}

	waitGroup.Wait(ctx)
	if err != nil {
		return 0, err
	}

	sum := 0
	for _, v := range results {
		sum = sum + v
	}

	return sum, nil
}

func waitGroupMultipleWaitsWorkflowTest(ctx Context) (int, error) {
	ctx = WithChildWorkflowOptions(ctx, ChildWorkflowOptions{
		ExecutionStartToCloseTimeout: time.Second * 30,
	})

	n := 10
	var err error
	results := make([]int, 0, n)
	waitGroup := NewWaitGroup(ctx)
	waitGroup.Add(4)
	for i := 0; i < n; i++ {
		t := time.Second * time.Duration(i+1)
		Go(ctx, func(ctx Context) {
			var result int
			err = ExecuteChildWorkflow(ctx, sleepWorkflow, t).Get(ctx, &result)
			results = append(results, result)
			waitGroup.Done()
		})
	}

	waitGroup.Wait(ctx)
	if err != nil {
		return 0, err
	}

	waitGroup.Add(6)
	waitGroup.Wait(ctx)
	if err != nil {
		return 0, err
	}

	sum := 0
	for _, v := range results {
		sum = sum + v
	}

	return sum, nil
}

func waitGroupMultipleConcurrentWaitsPanicsWorkflowTest(ctx Context) (int, error) {
	ctx = WithChildWorkflowOptions(ctx, ChildWorkflowOptions{
		ExecutionStartToCloseTimeout: time.Second * 30,
	})

	var err error
	var result1 int
	var result2 int

	waitGroup := NewWaitGroup(ctx)
	waitGroup.Add(2)

	Go(ctx, func(ctx Context) {
		err = ExecuteChildWorkflow(ctx, sleepWorkflow, time.Second*5).Get(ctx, &result1)
		waitGroup.Done()
	})

	Go(ctx, func(ctx Context) {
		err = ExecuteChildWorkflow(ctx, sleepWorkflow, time.Second*10).Get(ctx, &result2)
		waitGroup.Wait(ctx)
	})

	waitGroup.Wait(ctx)
	if err != nil {
		return 0, err
	}

	return result1 + result2, nil
}

func waitGroupNegativeCounterPanicsWorkflowTest(ctx Context) (int, error) {
	ctx = WithChildWorkflowOptions(ctx, ChildWorkflowOptions{
		ExecutionStartToCloseTimeout: time.Second * 30,
	})

	var err error
	var result int
	waitGroup := NewWaitGroup(ctx)

	Go(ctx, func(ctx Context) {
		waitGroup.Done()
		err = ExecuteChildWorkflow(ctx, sleepWorkflow, time.Second*5).Get(ctx, &result)
	})

	waitGroup.Wait(ctx)
	if err != nil {
		return 0, err
	}

	return result, nil
}

func (s *WorkflowUnitTest) Test_waitGroupNegativeCounterPanicsWorkflowTest() {
	env := newTestWorkflowEnv(s.T())
	env.RegisterWorkflow(waitGroupNegativeCounterPanicsWorkflowTest)
	env.ExecuteWorkflow(waitGroupNegativeCounterPanicsWorkflowTest)
	s.True(env.IsWorkflowCompleted())

	resultErr := env.GetWorkflowError().(*PanicError)
	s.EqualValues("negative WaitGroup counter", resultErr.Error())
	s.Contains(resultErr.StackTrace(), "cadence/internal.waitGroupNegativeCounterPanicsWorkflowTest")
}

func (s *WorkflowUnitTest) Test_WaitGroupMultipleConcurrentWaitsPanicsWorkflowTest() {
	env := newTestWorkflowEnv(s.T())
	env.RegisterWorkflow(waitGroupMultipleConcurrentWaitsPanicsWorkflowTest)
	env.RegisterWorkflow(sleepWorkflow)
	env.ExecuteWorkflow(waitGroupMultipleConcurrentWaitsPanicsWorkflowTest)
	s.True(env.IsWorkflowCompleted())

	resultErr := env.GetWorkflowError().(*PanicError)
	s.EqualValues("WaitGroup is reused before previous Wait has returned", resultErr.Error())
	s.Contains(resultErr.StackTrace(), "cadence/internal.waitGroupMultipleConcurrentWaitsPanicsWorkflowTest")
}

func (s *WorkflowUnitTest) Test_WaitGroupMultipleWaitsWorkflowTest() {
	env := newTestWorkflowEnv(s.T())
	env.RegisterWorkflow(waitGroupMultipleWaitsWorkflowTest)
	env.RegisterWorkflow(sleepWorkflow)
	env.ExecuteWorkflow(waitGroupMultipleWaitsWorkflowTest)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	var total int
	env.GetWorkflowResult(&total)
	s.Equal(10, total)
}

func (s *WorkflowUnitTest) Test_WaitGroupWaitForMWorkflowTest() {
	env := newTestWorkflowEnv(s.T())
	env.RegisterWorkflow(waitGroupWaitForMWorkflowTest)
	env.RegisterWorkflow(sleepWorkflow)

	n := 10
	m := 5
	env.ExecuteWorkflow(waitGroupWaitForMWorkflowTest, n, m)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	var total int
	env.GetWorkflowResult(&total)
	s.Equal(m, total)
}

func (s *WorkflowUnitTest) Test_WaitGroupWorkflowTest() {
	env := newTestWorkflowEnv(s.T())
	env.RegisterWorkflow(waitGroupWorkflowTest)
	env.RegisterWorkflow(sleepWorkflow)

	n := 10
	env.ExecuteWorkflow(waitGroupWorkflowTest, n)
	s.True(env.IsWorkflowCompleted())
	s.Nil(env.GetWorkflowError())
	s.NoError(env.GetWorkflowError())

	var total int
	env.GetWorkflowResult(&total)
	s.Equal(n, total)
}

func (s *WorkflowUnitTest) Test_StaleGoroutinesAreShutDown() {
	env := newTestWorkflowEnv(s.T())
	deferred := make(chan struct{})
	after := make(chan struct{})
	wf := func(ctx Context) error {
		Go(ctx, func(ctx Context) {
			defer func() { close(deferred) }()
			_ = Sleep(ctx, time.Hour) // outlive the workflow
			close(after)
		})
		_ = Sleep(ctx, time.Minute)
		return nil
	}
	env.RegisterWorkflow(wf)

	env.ExecuteWorkflow(wf)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	// goroutines are shut down async at the moment, so wait with a timeout.
	// give it up to 1s total.

	started := time.Now()
	maxWait := time.NewTimer(time.Second)
	defer maxWait.Stop()
	select {
	case <-deferred:
		s.T().Logf("deferred callback executed after %v", time.Now().Sub(started))
	case <-maxWait.C:
		s.Fail("deferred func should have been called within 1 second")
	}
	// if deferred code has run, this has already occurred-or-not.
	// if it timed out waiting for the deferred code, it has waited long enough, and this is mostly a curiosity.
	select {
	case <-after:
		s.Fail("code after sleep should not have run")
	default:
		s.T().Log("code after sleep correctly not executed")
	}
}

var _ WorkflowInterceptorFactory = (*tracingInterceptorFactory)(nil)

type tracingInterceptorFactory struct {
	instances []*tracingInterceptor
}

func (t *tracingInterceptorFactory) NewInterceptor(info *WorkflowInfo, next WorkflowInterceptor) WorkflowInterceptor {
	result := &tracingInterceptor{
		WorkflowInterceptorBase: WorkflowInterceptorBase{Next: next},
	}
	t.instances = append(t.instances, result)
	return result
}

var _ WorkflowInterceptor = (*tracingInterceptor)(nil)

type tracingInterceptor struct {
	WorkflowInterceptorBase
	trace []string
}

func (t *tracingInterceptor) ExecuteActivity(ctx Context, activityType string, args ...interface{}) Future {
	t.trace = append(t.trace, "ExecuteActivity "+activityType)
	return t.Next.ExecuteActivity(ctx, activityType, args...)
}

func (t *tracingInterceptor) ExecuteWorkflow(ctx Context, workflowType string, args ...interface{}) []interface{} {
	t.trace = append(t.trace, "ExecuteWorkflow "+workflowType+" begin")
	result := t.Next.ExecuteWorkflow(ctx, workflowType, args...)
	t.trace = append(t.trace, "ExecuteWorkflow "+workflowType+" end")
	return result
}

type WorkflowOptionTest struct {
	suite.Suite
}

func TestWorkflowOption(t *testing.T) {
	suite.Run(t, new(WorkflowOptionTest))
}

func (t *WorkflowOptionTest) TestKnowQueryType_NoHandlers() {
	wo := workflowOptions{queryHandlers: make(map[string]func([]byte) ([]byte, error))}
	t.ElementsMatch(
		[]string{
			QueryTypeStackTrace,
			QueryTypeOpenSessions,
			QueryTypeQueryTypes,
		},
		wo.KnownQueryTypes())
}

func (t *WorkflowOptionTest) TestKnowQueryType_WithHandlers() {
	wo := workflowOptions{queryHandlers: map[string]func([]byte) ([]byte, error){
		"a": nil,
		"b": nil,
	}}

	t.ElementsMatch(
		[]string{
			QueryTypeStackTrace,
			QueryTypeOpenSessions,
			QueryTypeQueryTypes,
			"a",
			"b",
		},
		wo.KnownQueryTypes())
}
