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

package instrumentation

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"time"

	mexporter "github.com/GoogleCloudPlatform/opentelemetry-operations-go/exporter/metric"
	"github.com/mitchellh/go-homedir"
	"github.com/rakyll/statik/fs"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/stdout"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/global"
	"go.opentelemetry.io/otel/sdk/metric/controller/basic"
	"google.golang.org/api/option"

	"github.com/GoogleContainerTools/skaffold/cmd/skaffold/app/cmd/statik"
	"github.com/GoogleContainerTools/skaffold/pkg/skaffold/constants"
	"github.com/GoogleContainerTools/skaffold/proto/v1"
)

var (
	allowedUsers = map[string]struct{}{
		"vsc":      {},
		"intellij": {},
		"gcloud":   {},
	}
)

func ExportMetrics(exitCode int) error {
	if !shouldExportMetrics || meter.Command == "" {
		return nil
	}
	home, err := homedir.Dir()
	if err != nil {
		return fmt.Errorf("retrieving home directory: %w", err)
	}
	meter.ExitCode = exitCode
	meter.Duration = time.Since(meter.StartTime)
	return exportMetrics(context.Background(),
		filepath.Join(home, constants.DefaultSkaffoldDir, constants.DefaultMetricFile),
		meter)
}

func exportMetrics(ctx context.Context, filename string, meter skaffoldMeter) error {
	logrus.Debug("exporting metrics")
	p, err := initExporter()
	if p == nil {
		return err
	}

	b, err := ioutil.ReadFile(filename)
	fileExists := err == nil
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var meters []skaffoldMeter
	err = json.Unmarshal(b, &meters)
	if err != nil {
		meters = []skaffoldMeter{}
	}
	meters = append(meters, meter)
	if !isOnline {
		b, _ = json.Marshal(meters)
		return ioutil.WriteFile(filename, b, 0666)
	}

	start := time.Now()
	p.Start(ctx)
	for _, m := range meters {
		createMetrics(ctx, m)
	}
	p.Stop(ctx)
	logrus.Debugf("metrics uploading complete in %s", time.Since(start).String())

	if fileExists {
		return os.Remove(filename)
	}
	return nil
}

func initCloudMonitoringExporterMetrics() (*basic.Controller, error) {
	statikFS, err := statik.FS()
	if err != nil {
		return nil, err
	}
	b, err := fs.ReadFile(statikFS, "/secret/keys.json")
	if err != nil {
		// No keys have been set in this version so do not attempt to write metrics
		if os.IsNotExist(err) {
			return devStdOutExporter()
		}
		return nil, err
	}

	var c creds
	err = json.Unmarshal(b, &c)
	if c.ProjectID == "" || err != nil {
		return nil, fmt.Errorf("no project id found in metrics credentials")
	}

	formatter := func(desc *metric.Descriptor) string {
		return fmt.Sprintf("custom.googleapis.com/skaffold/%s", desc.Name())
	}

	otel.SetErrorHandler(errHandler{})
	return mexporter.InstallNewPipeline(
		[]mexporter.Option{
			mexporter.WithProjectID(c.ProjectID),
			mexporter.WithMetricDescriptorTypeFormatter(formatter),
			mexporter.WithMonitoringClientOptions(option.WithCredentialsJSON(b)),
			mexporter.WithOnError(func(err error) {
				logrus.Debugf("Error with metrics: %v", err)
			}),
		},
	)
}

func devStdOutExporter() (*basic.Controller, error) {
	// export metrics to std out if local env is set.
	if _, ok := os.LookupEnv("SKAFFOLD_EXPORT_TO_STDOUT"); ok {
		_, controller, err := stdout.InstallNewPipeline([]stdout.Option{
			stdout.WithPrettyPrint(),
			stdout.WithWriter(os.Stdout),
		}, nil)
		return controller, err
	}
	return nil, nil
}

func createMetrics(ctx context.Context, meter skaffoldMeter) {
	// There is a minimum 10 second interval that metrics are allowed to upload to Cloud monitoring
	// A metric is uniquely identified by the metric name and the labels and corresponding values
	// This random number is used as a label to differentiate the metrics per user so if two users
	// run `skaffold build` at the same time they will both have their metrics recorded
	randLabel := attribute.String("randomizer", strconv.Itoa(rand.Intn(75000)))

	m := global.Meter("skaffold")

	// cloud monitoring only supports string type labels
	labels := []attribute.KeyValue{
		attribute.String("version", meter.Version),
		attribute.String("os", meter.OS),
		attribute.String("arch", meter.Arch),
		attribute.String("command", meter.Command),
		attribute.String("error", meter.ErrorCode.String()),
		attribute.String("platform_type", meter.PlatformType),
		attribute.String("config_count", strconv.Itoa(meter.ConfigCount)),
	}
	sharedLabels := []attribute.KeyValue{
		randLabel,
	}
	if _, ok := allowedUsers[meter.User]; ok {
		sharedLabels = append(sharedLabels, attribute.String("user", meter.User))
	}
	labels = append(labels, sharedLabels...)

	runCounter := metric.Must(m).NewInt64ValueRecorder("launches", metric.WithDescription("Skaffold Invocations"))
	runCounter.Record(ctx, 1, labels...)

	durationRecorder := metric.Must(m).NewFloat64ValueRecorder("launch/duration",
		metric.WithDescription("durations of skaffold commands in seconds"))
	durationRecorder.Record(ctx, meter.Duration.Seconds(), labels...)
	if meter.Command != "" {
		commandMetrics(ctx, meter, m, sharedLabels...)
		flagMetrics(ctx, meter, m, randLabel)
		if doesBuild.Contains(meter.Command) {
			builderMetrics(ctx, meter, m, sharedLabels...)
		}
		if doesDeploy.Contains(meter.Command) {
			deployerMetrics(ctx, meter, m, sharedLabels...)
		}
	}

	if meter.ErrorCode != 0 {
		errorMetrics(ctx, meter, m, sharedLabels...)
	}
}

func flagMetrics(ctx context.Context, meter skaffoldMeter, m metric.Meter, randLabel attribute.KeyValue) {
	flagCounter := metric.Must(m).NewInt64ValueRecorder("flags", metric.WithDescription("Tracks usage of enum flags"))
	for k, v := range meter.EnumFlags {
		labels := []attribute.KeyValue{
			attribute.String("flag_name", k),
			attribute.String("flag_value", v),
			attribute.String("command", meter.Command),
			attribute.String("error", meter.ErrorCode.String()),
			randLabel,
		}
		flagCounter.Record(ctx, 1, labels...)
	}
}

func commandMetrics(ctx context.Context, meter skaffoldMeter, m metric.Meter, labels ...attribute.KeyValue) {
	commandCounter := metric.Must(m).NewInt64ValueRecorder(meter.Command,
		metric.WithDescription(fmt.Sprintf("Number of times %s is used", meter.Command)))
	labels = append(labels, attribute.String("error", meter.ErrorCode.String()))
	commandCounter.Record(ctx, 1, labels...)

	if meter.Command == "dev" || meter.Command == "debug" {
		iterationCounter := metric.Must(m).NewInt64ValueRecorder(fmt.Sprintf("%s/iterations", meter.Command),
			metric.WithDescription(fmt.Sprintf("Number of iterations in a %s session", meter.Command)))

		counts := make(map[string]map[proto.StatusCode]int)

		for _, iteration := range meter.DevIterations {
			if _, ok := counts[iteration.Intent]; !ok {
				counts[iteration.Intent] = make(map[proto.StatusCode]int)
			}
			m := counts[iteration.Intent]
			m[iteration.ErrorCode]++
		}
		for intention, errorCounts := range counts {
			for errorCode, count := range errorCounts {
				iterationCounter.Record(ctx, int64(count),
					append(labels,
						attribute.String("intent", intention),
						attribute.String("error", errorCode.String()),
					)...)
			}
		}
	}
}

func deployerMetrics(ctx context.Context, meter skaffoldMeter, m metric.Meter, labels ...attribute.KeyValue) {
	deployerCounter := metric.Must(m).NewInt64ValueRecorder("deployer", metric.WithDescription("Deployers used"))
	for _, deployer := range meter.Deployers {
		deployerCounter.Record(ctx, 1, append(labels, attribute.String("deployer", deployer))...)
	}
	if meter.HelmReleasesCount > 0 {
		multiReleasesCounter := metric.Must(m).NewInt64ValueRecorder("helmReleases", metric.WithDescription("Multiple helm releases used"))
		multiReleasesCounter.Record(ctx, 1, append(labels, attribute.Int("count", meter.HelmReleasesCount))...)
	}
}

func builderMetrics(ctx context.Context, meter skaffoldMeter, m metric.Meter, labels ...attribute.KeyValue) {
	builderCounter := metric.Must(m).NewInt64ValueRecorder("builders", metric.WithDescription("Builders used"))
	artifactCounter := metric.Must(m).NewInt64ValueRecorder("artifacts", metric.WithDescription("Number of artifacts used"))
	dependenciesCounter := metric.Must(m).NewInt64ValueRecorder("artifact-dependencies", metric.WithDescription("Number of artifacts with dependencies"))
	for builder, count := range meter.Builders {
		bLabel := attribute.String("builder", builder)
		builderCounter.Record(ctx, 1, append(labels, bLabel)...)
		artifactCounter.Record(ctx, int64(count), append(labels, bLabel)...)
		dependenciesCounter.Record(ctx, int64(meter.BuildDependencies[builder]), append(labels, bLabel)...)
	}
}

func errorMetrics(ctx context.Context, meter skaffoldMeter, m metric.Meter, labels ...attribute.KeyValue) {
	errCounter := metric.Must(m).NewInt64ValueRecorder("errors", metric.WithDescription("Skaffold errors"))
	errCounter.Record(ctx, 1, append(labels, attribute.String("error", meter.ErrorCode.String()))...)

	labels = append(labels, attribute.String("command", meter.Command))

	switch meter.ErrorCode {
	case proto.StatusCode_UNKNOWN_ERROR:
		unknownErrCounter := metric.Must(m).NewInt64ValueRecorder("errors/unknown", metric.WithDescription("Unknown Skaffold Errors"))
		unknownErrCounter.Record(ctx, 1, labels...)
	case proto.StatusCode_TEST_UNKNOWN:
		unknownCounter := metric.Must(m).NewInt64ValueRecorder("test/unknown", metric.WithDescription("Unknown test Skaffold Errors"))
		unknownCounter.Record(ctx, 1, labels...)
	case proto.StatusCode_DEPLOY_UNKNOWN:
		unknownCounter := metric.Must(m).NewInt64ValueRecorder("deploy/unknown", metric.WithDescription("Unknown deploy Skaffold Errors"))
		unknownCounter.Record(ctx, 1, labels...)
	case proto.StatusCode_BUILD_UNKNOWN:
		unknownCounter := metric.Must(m).NewInt64ValueRecorder("build/unknown", metric.WithDescription("Unknown build Skaffold Errors"))
		unknownCounter.Record(ctx, 1, labels...)
	}
}
