/*
Copyright 2019 The Skaffold Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package status

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fakekubeclientset "k8s.io/client-go/kubernetes/fake"
	utilpointer "k8s.io/utils/pointer"

	"github.com/GoogleContainerTools/skaffold/pkg/diag"
	"github.com/GoogleContainerTools/skaffold/pkg/diag/validator"
	"github.com/GoogleContainerTools/skaffold/pkg/skaffold/deploy/kubectl"
	"github.com/GoogleContainerTools/skaffold/pkg/skaffold/deploy/label"
	"github.com/GoogleContainerTools/skaffold/pkg/skaffold/deploy/resource"
	"github.com/GoogleContainerTools/skaffold/pkg/skaffold/runner/runcontext"
	latest_v1 "github.com/GoogleContainerTools/skaffold/pkg/skaffold/schema/latest/v1"
	"github.com/GoogleContainerTools/skaffold/pkg/skaffold/util"
	"github.com/GoogleContainerTools/skaffold/proto/v1"
	"github.com/GoogleContainerTools/skaffold/testutil"
	testEvent "github.com/GoogleContainerTools/skaffold/testutil/event"
)

func TestGetDeployments(t *testing.T) {
	labeller := label.NewLabeller(true, nil)
	tests := []struct {
		description string
		deps        []*appsv1.Deployment
		expected    []*resource.Deployment
		shouldErr   bool
	}{
		{
			description: "multiple deployments in same namespace",
			deps: []*appsv1.Deployment{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "dep1",
						Namespace: "test",
						Labels: map[string]string{
							label.RunIDLabel: labeller.GetRunID(),
							"random":         "foo",
						},
					},
					Spec: appsv1.DeploymentSpec{ProgressDeadlineSeconds: utilpointer.Int32Ptr(10)},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "dep2",
						Namespace: "test",
						Labels: map[string]string{
							label.RunIDLabel: labeller.GetRunID(),
						},
					},
					Spec: appsv1.DeploymentSpec{ProgressDeadlineSeconds: utilpointer.Int32Ptr(20)},
				},
			},
			expected: []*resource.Deployment{
				resource.NewDeployment("dep1", "test", 10*time.Second),
				resource.NewDeployment("dep2", "test", 20*time.Second),
			},
		},
		{
			description: "command flag deadline is less than deployment spec.",
			deps: []*appsv1.Deployment{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "dep1",
						Namespace: "test",
						Labels: map[string]string{
							label.RunIDLabel: labeller.GetRunID(),
							"random":         "foo",
						},
					},
					Spec: appsv1.DeploymentSpec{ProgressDeadlineSeconds: utilpointer.Int32Ptr(300)},
				},
			},
			expected: []*resource.Deployment{
				resource.NewDeployment("dep1", "test", 300*time.Second),
			},
		},
		{
			description: "multiple deployments with no progress deadline set",
			deps: []*appsv1.Deployment{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "dep1",
						Namespace: "test",
						Labels: map[string]string{
							label.RunIDLabel: labeller.GetRunID(),
						},
					},
					Spec: appsv1.DeploymentSpec{ProgressDeadlineSeconds: utilpointer.Int32Ptr(100)},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "dep2",
						Namespace: "test",
						Labels: map[string]string{
							label.RunIDLabel: labeller.GetRunID(),
						},
					},
				},
			},
			expected: []*resource.Deployment{
				resource.NewDeployment("dep1", "test", 100*time.Second),
				resource.NewDeployment("dep2", "test", 200*time.Second),
			},
		},
		{
			description: "multiple deployments with progress deadline set to max",
			deps: []*appsv1.Deployment{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "dep1",
						Namespace: "test",
						Labels: map[string]string{
							label.RunIDLabel: labeller.GetRunID(),
						},
					},
					Spec: appsv1.DeploymentSpec{ProgressDeadlineSeconds: utilpointer.Int32Ptr(600)},
				},
			},
			expected: []*resource.Deployment{
				resource.NewDeployment("dep1", "test", 200*time.Second),
			},
		},
		{
			description: "no deployments",
			expected:    []*resource.Deployment{},
		},
		{
			description: "multiple deployments in different namespaces",
			deps: []*appsv1.Deployment{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "dep1",
						Namespace: "test",
						Labels: map[string]string{
							label.RunIDLabel: labeller.GetRunID(),
						},
					},
					Spec: appsv1.DeploymentSpec{ProgressDeadlineSeconds: utilpointer.Int32Ptr(100)},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "dep2",
						Namespace: "test1",
						Labels: map[string]string{
							label.RunIDLabel: labeller.GetRunID(),
						},
					},
					Spec: appsv1.DeploymentSpec{ProgressDeadlineSeconds: utilpointer.Int32Ptr(100)},
				},
			},
			expected: []*resource.Deployment{
				resource.NewDeployment("dep1", "test", 100*time.Second),
			},
		},
		{
			description: "deployment in correct namespace but not deployed by skaffold",
			deps: []*appsv1.Deployment{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "dep1",
						Namespace: "test",
						Labels: map[string]string{
							"some-other-tool": "helm",
						},
					},
					Spec: appsv1.DeploymentSpec{ProgressDeadlineSeconds: utilpointer.Int32Ptr(100)},
				},
			},
			expected: []*resource.Deployment{},
		},
		{
			description: "deployment in correct namespace deployed by skaffold but different run",
			deps: []*appsv1.Deployment{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "dep1",
						Namespace: "test",
						Labels: map[string]string{
							label.RunIDLabel: "9876-6789",
						},
					},
					Spec: appsv1.DeploymentSpec{ProgressDeadlineSeconds: utilpointer.Int32Ptr(100)},
				},
			},
			expected: []*resource.Deployment{},
		},
	}

	for _, test := range tests {
		testutil.Run(t, test.description, func(t *testutil.T) {
			objs := make([]runtime.Object, len(test.deps))
			for i, dep := range test.deps {
				objs[i] = dep
			}
			client := fakekubeclientset.NewSimpleClientset(objs...)
			actual, err := getDeployments(context.Background(), client, "test", labeller, 200*time.Second)
			t.CheckErrorAndDeepEqual(test.shouldErr, err, &test.expected, &actual,
				cmp.AllowUnexported(resource.Deployment{}, resource.Status{}),
				cmpopts.IgnoreInterfaces(struct{ diag.Diagnose }{}))
		})
	}
}

func TestGetDeployStatus(t *testing.T) {
	tests := []struct {
		description  string
		counter      *counter
		deployments  []*resource.Deployment
		expected     string
		expectedCode proto.StatusCode
		shouldErr    bool
	}{
		{
			description: "one error",
			counter:     &counter{total: 2, failed: 1},
			deployments: []*resource.Deployment{
				resource.NewDeployment("foo", "test", time.Second).
					WithPodStatuses([]proto.StatusCode{proto.StatusCode_STATUSCHECK_NODE_DISK_PRESSURE}),
			},
			expected:     "1/2 deployment(s) failed",
			expectedCode: proto.StatusCode_STATUSCHECK_NODE_DISK_PRESSURE,
			shouldErr:    true,
		},
		{
			description: "no error",
			counter:     &counter{total: 2},
			deployments: []*resource.Deployment{
				withStatus(
					resource.NewDeployment("r1", "test", 1),
					proto.ActionableErr{ErrCode: proto.StatusCode_STATUSCHECK_SUCCESS},
				),
				withStatus(
					resource.NewDeployment("r2", "test", 1),
					proto.ActionableErr{ErrCode: proto.StatusCode_STATUSCHECK_SUCCESS},
				),
			},
		},
		{
			description: "multiple errors",
			counter:     &counter{total: 3, failed: 2},
			expected:    "2/3 deployment(s) failed",
			deployments: []*resource.Deployment{
				resource.NewDeployment("foo", "test", time.Second).
					WithPodStatuses([]proto.StatusCode{proto.StatusCode_STATUSCHECK_NODE_DISK_PRESSURE}),
			},
			expectedCode: proto.StatusCode_STATUSCHECK_NODE_DISK_PRESSURE,
			shouldErr:    true,
		},
		{
			description: "0 deployments",
			counter:     &counter{},
		},
		{
			description: "unable to retrieve pods for deployment",
			counter:     &counter{total: 1, failed: 1},
			deployments: []*resource.Deployment{
				withStatus(
					resource.NewDeployment("deployment", "test", 1),
					proto.ActionableErr{ErrCode: proto.StatusCode_STATUSCHECK_DEPLOYMENT_FETCH_ERR},
				),
			},
			shouldErr:    true,
			expectedCode: proto.StatusCode_STATUSCHECK_DEPLOYMENT_FETCH_ERR,
		},
	}

	for _, test := range tests {
		testutil.Run(t, test.description, func(t *testutil.T) {
			testEvent.InitializeState([]latest_v1.Pipeline{{}})
			errCode, err := getSkaffoldDeployStatus(test.counter, test.deployments)
			t.CheckError(test.shouldErr, err)
			if test.shouldErr {
				t.CheckErrorContains(test.expected, err)
				t.CheckDeepEqual(test.expectedCode, errCode)
			}
		})
	}
}

func TestPrintSummaryStatus(t *testing.T) {
	labeller := label.NewLabeller(true, nil)
	tests := []struct {
		description string
		namespace   string
		deployment  string
		pending     int32
		ae          proto.ActionableErr
		expected    string
	}{
		{
			description: "no deployment left and current is in success",
			namespace:   "test",
			deployment:  "dep",
			pending:     0,
			ae:          proto.ActionableErr{ErrCode: proto.StatusCode_STATUSCHECK_SUCCESS},
			expected:    " - test:deployment/dep is ready.\n",
		},
		{
			description: "default namespace",
			namespace:   "default",
			deployment:  "dep",
			pending:     0,
			ae:          proto.ActionableErr{ErrCode: proto.StatusCode_STATUSCHECK_SUCCESS},
			expected:    " - deployment/dep is ready.\n",
		},
		{
			description: "no deployment left and current is in error",
			namespace:   "test",
			deployment:  "dep",
			pending:     0,
			ae:          proto.ActionableErr{ErrCode: proto.StatusCode_STATUSCHECK_DEADLINE_EXCEEDED, Message: "context deadline expired"},
			expected:    " - test:deployment/dep failed. Error: context deadline expired.\n",
		},
		{
			description: "more than 1 deployment left and current is in success",
			namespace:   "test",
			deployment:  "dep",
			pending:     4,
			ae:          proto.ActionableErr{ErrCode: proto.StatusCode_STATUSCHECK_SUCCESS},
			expected:    " - test:deployment/dep is ready. [4/10 deployment(s) still pending]\n",
		},
		{
			description: "more than 1 deployment left and current is in error",
			namespace:   "test",
			deployment:  "dep",
			pending:     8,
			ae:          proto.ActionableErr{ErrCode: proto.StatusCode_STATUSCHECK_DEADLINE_EXCEEDED, Message: "context deadline expired"},
			expected:    " - test:deployment/dep failed. Error: context deadline expired.\n",
		},
		{
			description: "skip printing if status check is cancelled",
			namespace:   "test",
			deployment:  "dep",
			pending:     4,
			ae:          proto.ActionableErr{ErrCode: proto.StatusCode_STATUSCHECK_USER_CANCELLED},
			expected:    "",
		},
	}

	for _, test := range tests {
		testutil.Run(t, test.description, func(t *testutil.T) {
			checker := statusChecker{labeller: labeller}
			out := new(bytes.Buffer)
			rc := newCounter(10)
			rc.pending = test.pending
			testEvent.InitializeState([]latest_v1.Pipeline{{}})
			r := withStatus(resource.NewDeployment(test.deployment, test.namespace, 0), test.ae)
			// report status once and set it changed to false.
			r.ReportSinceLastUpdated(false)
			r.UpdateStatus(test.ae)
			checker.printStatusCheckSummary(out, r, *rc)
			t.CheckDeepEqual(test.expected, out.String())
		})
	}
}

func TestPrintStatus(t *testing.T) {
	labeller := label.NewLabeller(true, nil)
	tests := []struct {
		description string
		rs          []*resource.Deployment
		expectedOut string
		expected    bool
	}{
		{
			description: "single resource successful marked complete - skip print",
			rs: []*resource.Deployment{
				withStatus(
					resource.NewDeployment("r1", "test", 1),
					proto.ActionableErr{ErrCode: proto.StatusCode_STATUSCHECK_SUCCESS},
				),
			},
			expected: true,
		},
		{
			description: "single resource in error marked complete -skip print",
			rs: []*resource.Deployment{
				withStatus(
					resource.NewDeployment("r1", "test", 1),
					proto.ActionableErr{ErrCode: proto.StatusCode_STATUSCHECK_UNKNOWN, Message: "error"},
				),
			},
			expected: true,
		},
		{
			description: "multiple resources 1 not complete",
			rs: []*resource.Deployment{
				withStatus(
					resource.NewDeployment("r1", "test", 1),
					proto.ActionableErr{ErrCode: proto.StatusCode_STATUSCHECK_SUCCESS},
				),
				withStatus(
					resource.NewDeployment("r2", "test", 1).
						WithPodStatuses([]proto.StatusCode{proto.StatusCode_STATUSCHECK_IMAGE_PULL_ERR}),
					proto.ActionableErr{ErrCode: proto.StatusCode_STATUSCHECK_DEPLOYMENT_ROLLOUT_PENDING,
						Message: "pending\n"},
				),
			},
			expectedOut: ` - test:deployment/r2: pod failed
    - test:pod/foo: pod failed
`,
		},
		{
			description: "multiple resources 1 not complete and retry-able error",
			rs: []*resource.Deployment{
				withStatus(
					resource.NewDeployment("r1", "test", 1),
					proto.ActionableErr{ErrCode: proto.StatusCode_STATUSCHECK_SUCCESS},
				),
				withStatus(
					resource.NewDeployment("r2", "test", 1),
					proto.ActionableErr{
						ErrCode: proto.StatusCode_STATUSCHECK_KUBECTL_CONNECTION_ERR,
						Message: resource.MsgKubectlConnection},
				),
			},
			expectedOut: ` - test:deployment/r2: kubectl connection error
`,
		},
		{
			description: "skip printing if status check is cancelled",
			rs: []*resource.Deployment{
				withStatus(
					resource.NewDeployment("r1", "test", 1),
					proto.ActionableErr{ErrCode: proto.StatusCode_STATUSCHECK_USER_CANCELLED},
				),
			},
			expected:    true,
			expectedOut: "",
		},
	}

	for _, test := range tests {
		testutil.Run(t, test.description, func(t *testutil.T) {
			out := new(bytes.Buffer)
			testEvent.InitializeState([]latest_v1.Pipeline{{}})
			checker := statusChecker{labeller: labeller}
			actual := checker.printStatus(test.rs, out)
			t.CheckDeepEqual(test.expectedOut, out.String())
			t.CheckDeepEqual(test.expected, actual)
		})
	}
}

func withStatus(d *resource.Deployment, ae proto.ActionableErr) *resource.Deployment {
	d.UpdateStatus(ae)
	return d
}

func TestCounterCopy(t *testing.T) {
	tests := []struct {
		description string
		c           *counter
		expected    counter
	}{
		{
			description: "initial counter is copied correctly ",
			c:           newCounter(10),
			expected:    *newCounter(10),
		},
		{
			description: "counter with updated pending is copied correctly",
			c:           &counter{total: 10, pending: 2},
			expected:    counter{total: 10, pending: 2},
		},
		{
			description: "counter with updated failed and pending is copied correctly",
			c:           &counter{total: 10, pending: 5, failed: 3},
			expected:    counter{total: 10, pending: 5, failed: 3},
		},
	}
	for _, test := range tests {
		testutil.Run(t, test.description, func(t *testutil.T) {
			t.CheckDeepEqual(test.expected, test.c.copy(), cmp.AllowUnexported(counter{}))
		})
	}
}

func TestResourceMarkProcessed(t *testing.T) {
	tests := []struct {
		description string
		c           *counter
		err         error
		expected    counter
	}{
		{
			description: "when deployment failed, counter is updated",
			c:           newCounter(10),
			err:         errors.New("some ae"),
			expected:    counter{total: 10, failed: 1, pending: 9},
		},
		{
			description: "when deployment is successful, counter is updated",
			c:           newCounter(10),
			expected:    counter{total: 10, failed: 0, pending: 9},
		},
		{
			description: "counter when 1 deployment is updated correctly",
			c:           newCounter(1),
			expected:    counter{total: 1, failed: 0, pending: 0},
		},
	}
	for _, test := range tests {
		testutil.Run(t, test.description, func(t *testutil.T) {
			t.CheckDeepEqual(test.expected, test.c.markProcessed(test.err), cmp.AllowUnexported(counter{}))
		})
	}
}

func TestPollDeployment(t *testing.T) {
	rolloutCmd := "kubectl --context kubecontext rollout status deployment dep --namespace test --watch=false"
	tests := []struct {
		description string
		dep         *resource.Deployment
		runs        [][]validator.Resource
		command     util.Command
		expected    proto.StatusCode
	}{
		{
			description: "pollDeploymentStatus errors out immediately when container error can't recover",
			dep:         resource.NewDeployment("dep", "test", time.Second),
			command:     testutil.CmdRunOut(rolloutCmd, "Waiting for replicas to be available"),
			runs: [][]validator.Resource{
				{validator.NewResource(
					"test",
					"pod",
					"dep-pod",
					"Pending",
					proto.ActionableErr{ErrCode: proto.StatusCode_STATUSCHECK_CONTAINER_TERMINATED},
					[]string{"err"})},
			},
			expected: proto.StatusCode_STATUSCHECK_DEPLOYMENT_ROLLOUT_PENDING,
		},
		{
			description: "pollDeploymentStatus waits when a container can recover and eventually succeeds",
			dep:         resource.NewDeployment("dep", "test", time.Second),
			command: testutil.CmdRunOutErr(
				// pending due to recoverable error
				rolloutCmd, "", errors.New("Unable to connect to the server")).
				// successfully rolled out run
				AndRunOut(rolloutCmd, "successfully rolled out"),
			runs: [][]validator.Resource{
				// pod pending due to some k8 infra related recoverable error.
				{validator.NewResource(
					"test",
					"pod",
					"dep-pod",
					"Pending",
					proto.ActionableErr{ErrCode: proto.StatusCode_STATUSCHECK_NODE_DISK_PRESSURE},
					[]string{"err"})},
				// pod recovered
				{validator.NewResource(
					"test",
					"pod",
					"dep-pod",
					"Running",
					proto.ActionableErr{ErrCode: proto.StatusCode_STATUSCHECK_SUCCESS},
					nil)},
			},
			expected: proto.StatusCode_STATUSCHECK_SUCCESS,
		},
	}
	for _, test := range tests {
		testutil.Run(t, test.description, func(t *testutil.T) {
			t.Override(&util.DefaultExecCommand, test.command)
			t.Override(&defaultPollPeriodInMilliseconds, 100)
			testEvent.InitializeState([]latest_v1.Pipeline{{}})
			mockVal := mockValidator{runs: test.runs}
			dep := test.dep.WithValidator(mockVal)

			pollDeploymentStatus(context.Background(), &statusConfig{}, dep)

			t.CheckDeepEqual(test.expected, test.dep.Status().ActionableError().ErrCode)
		})
	}
}

type mockValidator struct {
	runs      [][]validator.Resource
	iteration int
}

func (m mockValidator) Run(context.Context) ([]validator.Resource, error) {
	if m.iteration < len(m.runs) {
		m.iteration++
	}
	// keep replaying the last result.
	return m.runs[m.iteration-1], nil
}

func (m mockValidator) WithLabel(string, string) diag.Diagnose {
	return m
}

func (m mockValidator) WithValidators([]validator.Validator) diag.Diagnose {
	return m
}

type statusConfig struct {
	runcontext.RunContext // Embedded to provide the default values.
}

func (c *statusConfig) GetKubeContext() string { return kubectl.TestKubeContext }
