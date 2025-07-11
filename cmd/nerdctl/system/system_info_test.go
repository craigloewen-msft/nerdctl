/*
   Copyright The containerd Authors.

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

package system

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/containerd/nerdctl/mod/tigron/expect"
	"github.com/containerd/nerdctl/mod/tigron/require"
	"github.com/containerd/nerdctl/mod/tigron/test"
	"github.com/containerd/nerdctl/mod/tigron/tig"

	"github.com/containerd/nerdctl/v2/pkg/infoutil"
	"github.com/containerd/nerdctl/v2/pkg/inspecttypes/dockercompat"
	"github.com/containerd/nerdctl/v2/pkg/testutil"
	"github.com/containerd/nerdctl/v2/pkg/testutil/nerdtest"
)

func testInfoComparator(stdout string, t tig.T) {
	var dinf dockercompat.Info
	err := json.Unmarshal([]byte(stdout), &dinf)
	assert.NilError(t, err, "failed to unmarshal stdout")
	unameM := infoutil.UnameM()
	assert.Assert(t, dinf.Architecture == unameM, fmt.Sprintf("expected info.Architecture to be %q, got %q", unameM, dinf.Architecture))
}

func TestInfo(t *testing.T) {
	testCase := nerdtest.Setup()

	// Note: some functions need to be tested without the automatic --namespace nerdctl-test argument, so we need
	// to retrieve the binary name.
	// Note that we know this works already, so no need to assert err.
	bin, _ := exec.LookPath(testutil.GetTarget())

	testCase.SubTests = []*test.Case{
		{
			Description: "info",
			Command:     test.Command("info", "--format", "{{json .}}"),
			Expected:    test.Expects(0, nil, testInfoComparator),
		},
		{
			Description: "info convenience form",
			Command:     test.Command("info", "--format", "json"),
			Expected:    test.Expects(0, nil, testInfoComparator),
		},
		{
			Description: "info with namespace",
			Require:     require.Not(nerdtest.Docker),
			Command: func(data test.Data, helpers test.Helpers) test.TestableCommand {
				return helpers.Custom(bin, "info")
			},
			Expected: test.Expects(0, nil, expect.Contains("Namespace:	default")),
		},
		{
			Description: "info with namespace env var",
			Env: map[string]string{
				"CONTAINERD_NAMESPACE": "test",
			},
			Require: require.Not(nerdtest.Docker),
			Command: func(data test.Data, helpers test.Helpers) test.TestableCommand {
				return helpers.Custom(bin, "info")
			},
			Expected: test.Expects(0, nil, expect.Contains("Namespace:	test")),
		},
	}

	testCase.Run(t)
}
