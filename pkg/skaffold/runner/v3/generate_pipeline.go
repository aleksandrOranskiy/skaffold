/*
Copyright 2021 The Skaffold Authors

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
package v3

import (
	"context"
	"fmt"
	"io"

	latest_v1 "github.com/GoogleContainerTools/skaffold/pkg/skaffold/schema/latest/v1"
)

func (r *SkaffoldRunner) GeneratePipeline(ctx context.Context, out io.Writer, configs []*latest_v1.SkaffoldConfig, configPaths []string, fileOut string) error {
	return fmt.Errorf("not implemented error: SkaffoldRunner(v3).GeneratePipeline")
}
