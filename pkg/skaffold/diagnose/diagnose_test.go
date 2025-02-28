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

package diagnose

import (
	"context"
	"io/ioutil"
	"testing"

	"github.com/GoogleContainerTools/skaffold/pkg/skaffold/runner/runcontext"
	latest_v1 "github.com/GoogleContainerTools/skaffold/pkg/skaffold/schema/latest/v1"
	"github.com/GoogleContainerTools/skaffold/testutil"
)

func TestSizeOfDockerContext(t *testing.T) {
	tests := []struct {
		description        string
		artifactName       string
		DockerfileContents string
		files              map[string]string
		expected           int64
		shouldErr          bool
	}{
		{
			description:        "test size",
			artifactName:       "empty",
			DockerfileContents: "From Scratch",
			expected:           2048,
		},
		{
			description:        "test size for a image with file",
			artifactName:       "image",
			DockerfileContents: "From Scratch \n Copy foo /",
			files:              map[string]string{"foo": "foo"},
			expected:           3072,
		},
		{
			description:        "incorrect docker file",
			artifactName:       "error-artifact",
			DockerfileContents: "From Scratch \n Copy doesNotExists /",
			shouldErr:          true,
		},
	}
	for _, test := range tests {
		testutil.Run(t, test.description, func(t *testutil.T) {
			tmpDir := t.NewTempDir().
				Write("Dockerfile", test.DockerfileContents).
				WriteFiles(test.files)

			dummyArtifact := &latest_v1.Artifact{
				Workspace: tmpDir.Root(),
				ImageName: test.artifactName,
				ArtifactType: latest_v1.ArtifactType{
					DockerArtifact: &latest_v1.DockerArtifact{
						DockerfilePath: "Dockerfile",
					},
				},
			}

			actual, err := sizeOfDockerContext(context.TODO(), dummyArtifact, nil)
			t.CheckErrorAndDeepEqual(test.shouldErr, err, test.expected, actual)
		})
	}
}

func TestCheckArtifacts(t *testing.T) {
	testutil.Run(t, "", func(t *testutil.T) {
		tmpDir := t.NewTempDir().Write("Dockerfile", "FROM busybox")

		err := CheckArtifacts(context.Background(), &mockConfig{
			artifacts: []*latest_v1.Artifact{{
				Workspace: tmpDir.Root(),
				ArtifactType: latest_v1.ArtifactType{
					DockerArtifact: &latest_v1.DockerArtifact{
						DockerfilePath: "Dockerfile",
					},
				},
			}},
		}, ioutil.Discard)

		t.CheckNoError(err)
	})
}

type mockConfig struct {
	runcontext.RunContext // Embedded to provide the default values.
	artifacts             []*latest_v1.Artifact
}

func (c *mockConfig) PipelineForImage() latest_v1.Pipeline {
	var pipeline latest_v1.Pipeline
	pipeline.Build.Artifacts = c.artifacts
	return pipeline
}

func (c *mockConfig) Artifacts() []*latest_v1.Artifact {
	return c.artifacts
}
