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

package cmd

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"

	"github.com/spf13/cobra"

	"github.com/GoogleContainerTools/skaffold/cmd/skaffold/app/flags"
	"github.com/GoogleContainerTools/skaffold/pkg/skaffold/graph"
	"github.com/GoogleContainerTools/skaffold/pkg/skaffold/runner"
	latest_v1 "github.com/GoogleContainerTools/skaffold/pkg/skaffold/schema/latest/v1"
)

var (
	showBuild                 bool
	renderOutputPath          string
	renderFromBuildOutputFile flags.BuildOutputFileFlag
	offline                   bool
)

// NewCmdRender describes the CLI command to build artifacts render Kubernetes manifests.
func NewCmdRender() *cobra.Command {
	return NewCmd("render").
		WithDescription("[alpha] Perform all image builds, and output rendered Kubernetes manifests").
		WithExample("Hydrate Kubernetes manifests without building the images, using digest resolved from tag in remote registry ", "render --digest-source=remote").
		WithCommonFlags().
		WithFlags([]*Flag{
			{Value: &showBuild, Name: "loud", DefValue: false, Usage: "Show the build logs and output", IsEnum: true},
			{Value: &renderFromBuildOutputFile, Name: "build-artifacts", Shorthand: "a", Usage: "File containing build result from a previous 'skaffold build --file-output'"},
			{Value: &offline, Name: "offline", DefValue: false, Usage: `Do not connect to Kubernetes API server for manifest creation and validation. This is helpful when no Kubernetes cluster is available (e.g. GitOps model). No metadata.namespace attribute is injected in this case - the manifest content does not get changed.`, IsEnum: true},
			{Value: &renderOutputPath, Name: "output", Shorthand: "o", DefValue: "", Usage: "file to write rendered manifests to"},
			{Value: &opts.DigestSource, Name: "digest-source", DefValue: "local", Usage: "Set to 'local' to build images locally and use digests from built images; Set to 'remote' to resolve the digest of images by tag from the remote registry; Set to 'none' to use tags directly from the Kubernetes manifests. Set to 'tag' to use tags directly from the build.", IsEnum: true},
		}).
		NoArgs(doRender)
}

func doRender(ctx context.Context, out io.Writer) error {
	buildOut := ioutil.Discard
	if showBuild {
		buildOut = out
	}

	return withRunner(ctx, out, func(r runner.Runner, configs []*latest_v1.SkaffoldConfig) error {
		var bRes []graph.Artifact

		if renderFromBuildOutputFile.String() != "" {
			bRes = renderFromBuildOutputFile.BuildArtifacts()
		} else {
			var err error
			bRes, err = r.Build(ctx, buildOut, targetArtifacts(opts, configs))
			if err != nil {
				return fmt.Errorf("executing build: %w", err)
			}
		}

		if err := r.Render(ctx, out, bRes, offline, renderOutputPath); err != nil {
			return fmt.Errorf("rendering manifests: %w", err)
		}
		return nil
	})
}
