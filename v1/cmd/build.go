/*
Copyright 2019 The Kubernetes Authors.

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
	"fmt"
	"io/ioutil"
	"strconv"
	"strings"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/commons/lookup"
	"github.com/imdario/mergo"
	"github.com/palantir/stacktrace"

	// initialize konfigadm
	_ "github.com/flanksource/konfigadm/pkg"
	"github.com/flanksource/konfigadm/pkg/phases"
	konfigadm "github.com/flanksource/konfigadm/pkg/types"
	"github.com/spf13/cobra"
	"gopkg.in/flanksource/yaml.v3"
	"sigs.k8s.io/image-builder/api"
	"sigs.k8s.io/image-builder/pkg"
	"sigs.k8s.io/image-builder/pkg/distros"
	"sigs.k8s.io/image-builder/pkg/engines"

	"sigs.k8s.io/image-builder/pkg/converters"
	"sigs.k8s.io/image-builder/pkg/resources"
)

var Engines map[string]pkg.Engine

func getEngine(opts *api.KubernetesConfiguration) (pkg.Engine, error) {
	kind, ok := opts.Engine["kind"]
	if !ok {
		kind = "qemu"
	}
	if engine, ok := Engines[fmt.Sprintf("%s", kind)]; !ok {
		return nil, fmt.Errorf("unknown engine: %v: \n %#v", kind, opts.Engine)
	} else {
		return engine, nil
	}
}

func parseConfig(configFile string) (*api.KubernetesConfiguration, map[string]interface{}, error) {
	var config = &api.KubernetesConfiguration{}
	data, err := ioutil.ReadFile(configFile)
	if err != nil {
		return nil, nil, err
	}

	// 1st run: unmarshall using konfigadm as a subkey
	if err := yaml.Unmarshal(data, config); err != nil {
		return nil, nil, err
	}

	// 2nd run: unmarshall the root yaml, so that an image-builder yaml can be
	// fed directly into konfigadm
	konfigadmSpec := &konfigadm.Config{}
	konfigadmSpec.Init()
	if err := yaml.Unmarshal(data, konfigadmSpec); err != nil {
		return nil, nil, err
	}
	config.Konfigadm.Init()
	konfigadmSpec.ImportConfig(config.Konfigadm)
	config.Konfigadm = *konfigadmSpec
	data, err = yaml.Marshal(config)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to round-trip YAML: %v", err)
	}

	raw := make(map[string]interface{})
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal to map[string]interface: %v", err)
	}
	return config, raw, nil

}
func getConfig(cmd *cobra.Command, args []string) (*api.KubernetesConfiguration, map[string]interface{}, error) {
	var base *api.KubernetesConfiguration
	var rawBase map[string]interface{}
	for _, config := range configFile {
		cfg, raw, err := parseConfig(config)
		if err != nil {
			return nil, nil, err
		}
		if base == nil {
			base = cfg
			rawBase = raw
		} else {
			if err := mergo.Merge(base, cfg); err != nil {
				return nil, nil, err
			}
			if err := mergo.Merge(&rawBase, raw); err != nil {
				return nil, nil, err
			}

		}
	}
	extras, _ := cmd.Flags().GetStringSlice("extras")
	for _, extra := range extras {
		key := strings.Split(extra, "=")[0]
		val := extra[len(key)+1:]
		logger.Debugf("Looking up %s to set it to: %s", key, val)

		value, err := lookup.LookupString(&base, key)
		if err != nil {
			logger.Fatalf("Cannot lookup %s: %v", key, err)
		}
		logger.Infof("Overriding %s %v => %v", key, value, val)
		switch value.Interface().(type) {
		case string:
			value.SetString(val)
		case int:
			i, err := strconv.ParseInt(val, 10, 64)
			if err != nil {
				logger.Fatalf("Cannot convert %s to int", val)
			}
			value.SetInt(i)
		case bool:
			b, err := strconv.ParseBool(val)
			if err != nil {
				logger.Fatalf("Cannot convert %s to a boolean", val)
			}
			value.SetBool(b)
		}
	}

	return base, rawBase, nil
}

func getContext(cmd *cobra.Command, args []string) (*pkg.BuildContext, error) {
	config, raw, err := getConfig(cmd, args)
	if err != nil {
		return nil, err
	}
	input, err := api.GetImage(config.Input)
	if err != nil {
		return nil, stacktrace.Propagate(err, "unable to parse input")
	}

	// logger.Tracef("%v => %# v", config.Input, input)
	// logger.Prettyf("", input)
	var outputs []api.Image
	for _, driver := range config.Output {
		output, err := api.GetImage(driver)
		if err != nil {
			return nil, err
		}
		outputs = append(outputs, output)
	}

	engine, err := getEngine(config)
	if err != nil {
		return nil, stacktrace.Propagate(err, "unable to parse engine")
	}

	distro, err := distros.GetDistroByName(config.DistroName)
	if err != nil {
		return nil, fmt.Errorf("cannot find distro: %s", config.DistroName)
	}

	logger.Tracef("Found distro for %s: %s ", config.DistroName, distro)
	// get the distribution details for the OS / Driver combo
	from := distro.GetDistribution().GetImageByKind(input.Kind())
	// merge the image details (e.g. URL / AMI) into the user-provided config
	input, err = api.Merge(input, from)
	if err != nil {
		return nil, err
	}
	logger.Infof("Found image for %s: %#v", input.Kind(), from)

	dryRun, _ := cmd.Flags().GetBool("dry-run")

	var defaults map[string]map[string]interface{}
	if err := yaml.Unmarshal(resources.FSMustByte(false, "/defaults.yml"), &defaults); err != nil {
		return nil, err
	}
	ctx := &pkg.BuildContext{
		Raw:      raw,
		Input:    input,
		Output:   outputs,
		Engine:   engine,
		Distro:   distro,
		Config:   *config,
		Defaults: defaults,
		DryRun:   dryRun,
		Logger:   logger.StandardLogger(),
	}
	ctx.Tracef("distro=%v input=%+v outputs=%v", distro, input, outputs)
	return ctx, nil
}

var Build = cobra.Command{
	Use:   "build",
	Short: "Build an image ",
	Args:  cobra.MinimumNArgs(0),
	RunE: func(cmd *cobra.Command, args []string) error {

		ctx, err := getContext(cmd, args)
		if err != nil {
			return err
		}
		logger.Secretf("%s", ctx)

		if os, ok := phases.OperatingSystems[ctx.Distro.GetDistribution().OS]; !ok {
			names := []string{}
			for k := range phases.OperatingSystems {
				names = append(names, k)
			}
			return fmt.Errorf("Unsupported OS by konfigadm: %v, supported os: %v", ctx.Distro.GetDistribution().OS, names)
		} else {
			ctx.Config.Konfigadm.Context.Flags = os.GetTags()
		}
		// Configures an image and returns the result or an error
		outputImage, err := ctx.Engine.Configure(*ctx)
		if err != nil {
			return err
		}

		// once configured, the output becomes the input into the processing chain
		ctx.Input = outputImage

		for _, output := range ctx.Output {
			logger.Infof("Converting %s to %s", ctx.Input, output)
			// Converts an image to the target type
			converted, err := converters.Convert(ctx, ctx.Input, output)
			if err != nil {
				return err
			}
			ctx.Input = converted
		}
		if ctx.DryRun {
			return nil
		}

		if ctx.Input == nil {
			logger.Fatalf("empty image created")
		}
		logger.Infof("Created new image: %s", ctx.Input)
		// print image output so that it can be used directly in scripts e.g $(image-builder build)
		fmt.Printf("%s", ctx.Input)
		return nil
	},
}

var configFile []string

func init() {

	Engines = make(map[string]pkg.Engine)

	for _, engine := range []pkg.Engine{engines.Qemu{}, engines.Docker{}, engines.Packer{}, engines.NullEngine} {
		Engines[engine.Kind()] = engine
	}
	logger.Tracef("Engines: %s", Engines)
	Build.PersistentFlags().Bool("dry-run", false, "")
	Build.PersistentFlags().StringSliceP("extras", "e", []string{}, "Extra variables to override, in the form of var=value")
	Build.Flags().StringSliceVarP(&configFile, "config", "c", []string{"image-builder.yaml"}, "")
}
