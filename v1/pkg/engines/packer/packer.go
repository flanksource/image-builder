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

package packer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"

	"github.com/flanksource/commons/deps"
	"github.com/flanksource/commons/files"
	"github.com/flanksource/commons/logger"

	"sigs.k8s.io/image-builder/api"
	"sigs.k8s.io/image-builder/pkg"
	"sigs.k8s.io/image-builder/pkg/resources/ansible"
)

type Packer struct {
	Comment        string                 `json:"_comment"`
	Builders       []interface{}          `json:"builders"`
	Provisioners   []interface{}          `json:"provisioners"`
	PostProcessors []interface{}          `json:"post-processors"`
	Variables      map[string]interface{} `json:"variables"`
	manifestPath   string
	binary         deps.BinaryFunc
}

type Manifest struct {
	Builds []struct {
		Name          string      `json:"name"`
		BuilderType   string      `json:"builder_type"`
		BuildTime     int         `json:"build_time"`
		Files         interface{} `json:"files"`
		ArtifactID    string      `json:"artifact_id"`
		PackerRunUUID string      `json:"packer_run_uuid"`
		CustomData    interface{} `json:"custom_data"`
	} `json:"builds"`
	LastRunUUID string `json:"last_run_uuid"`
}

func (manifest Manifest) GetImage() (api.Image, error) {
	for _, build := range manifest.Builds {
		switch build.BuilderType {
		case "amazon-ebs":
			return &api.AMI{
				Region: strings.Split(build.ArtifactID, ":")[0],
				ID:     strings.Split(build.ArtifactID, ":")[1],
			}, nil
		}
	}
	return nil, fmt.Errorf("cannot find image type %v", manifest)
}

// type Builder interface {
// 	Map(ctx *pkg.BuildContext) (map[string]interface{}, error)
// 	GetAllowedFields() map[string]reflect.Type
// }
// type Builders struct {
// 	VMWareISO map[string]interface{} `yaml:"vmware-iso,omitempty" structs:"vmware-iso,omitempty"`
// 	VMWareVMX map[string]interface{} `yaml:"vmware-vmx,omitempty"  structs:"vmware-vmx,omitempty"`
// 	Qemu      map[string]interface{} `yaml:"qemu,omitempty"  structs:"qemu,omitempty"`
// 	AWS       map[string]interface{} `yaml:"amazon-ebs,omitempty"  structs:"amazon-ebs,omitempty"`
// 	GCE       map[string]interface{} `yaml:"gce,omitempty"  structs:"gce,omitempty"`
// 	Azure     map[string]interface{} `yaml:"azure,omitempty"  structs:"azure,omitempty"`
// }

type ShellProvisioner struct {
	EnvironmentVars []string `json:"environment_vars"`
	ExecuteCommand  string   `json:"execute_command"`
	Scripts         []string `json:"inline"`
	Type            string   `json:"type"`
}

type AnsibleProvisioner struct {
	Variables       map[string]interface{} `json:"variables"`
	Playbook        string                 `json:"playbook"`
	EnvironmentVars []string               `json:"environment_vars"`
	ExecuteCommand  string                 `json:"execute_command"`
	Scripts         []string               `json:"scripts"`
	Type            string                 `json:"type"`
	ExtraArguments  []string               `json:"extra_arguments"`
}

func AnsibleGetProvisioner(config api.KubernetesConfiguration) (*AnsibleProvisioner, error) {
	// err, args := GetAnsibleArguments(config)
	// if err != nil {
	// 	return nil, err
	// }
	args := make(map[string]interface{})
	extra := []string{"--extra-vars"}
	for k, v := range args {
		extra = append(extra, fmt.Sprintf("%s=%s", k, v))
	}

	// if err := ExtractTo(".ansible"); err != nil {
	// 	return nil, err
	// }

	return &AnsibleProvisioner{
		Type:     "ansible",
		Playbook: "./.ansible/playbook.yml",
		EnvironmentVars: []string{
			"ANSIBLE_SSH_ARGS='{{user `existing_ansible_ssh_args`}} -o IdentitiesOnly=yes'",
			"ANSIBLE_REMOTE_TEMP='/tmp/.ansible/'",
		},
		ExtraArguments: extra,
	}, nil

}
func merge(m1, m2 map[string]interface{}) map[string]interface{} {
	for k, v := range m2 {
		m1[k] = v
	}
	return m1
}

func extract(fs http.FileSystem, file http.File, path string, to string) error {

	stat, _ := file.Stat()
	if stat.IsDir() {
		ls, _ := file.Readdir(-1)
		for _, childInfo := range ls {
			childPath := path + "/" + childInfo.Name()
			child, err := fs.Open(childPath)
			if err != nil {
				return fmt.Errorf("failed top open %s: %v", childPath, err)
			}
			if childInfo.IsDir() {
				if err := extract(fs, child, childPath, to); err != nil {
					return fmt.Errorf("failed to extract directory %s: %v", childPath, err)
				}
			} else {
				if _, err := files.CopyFromReader(child, to+childPath, childInfo.Mode()); err != nil {
					return err
				}
			}
		}
	} else {
		fmt.Printf("%s -> %s\n", path, stat.Name())
	}

	return nil

}

func NewPacker(ctx pkg.BuildContext) (*Packer, error) {
	packer := Packer{}

	tmp := "/Users/p01moshealei/Desktop/test5"
	fs, _ := ansible.FS(false).Open("/ansible")
	if err := extract(ansible.FS(false), fs, "/ansible", tmp); err != nil {
		return nil, err
	}

	engine := ctx.Raw["engine"].(map[string]interface{})

	if _, ok := engine["builders"]; !ok {
		return nil, errors.New("must specify at least 1 builder")
	}

	for name, _builder := range engine["builders"].(map[string]interface{}) {

		builder := map[string]interface{}{
			"type": name,
		}

		if _builder != nil {
			for k, v := range _builder.(map[string]interface{}) {
				builder[k] = v
			}
		}

		opts, err := ctx.Input.GetPackerOptions()
		if err != nil {
			return nil, err
		}

		builder = merge(builder, opts)
		builder = merge(builder, ctx.Defaults[name])
		packer.Builders = append(packer.Builders, builder)
	}

	bash, err := ctx.Config.Konfigadm.ToBash()
	if err != nil {
		return nil, err
	}

	version := "1.5.5"
	if ver, ok := engine["version"]; ok {
		version = ver.(string)
	}
	packer.binary = deps.Binary("packer", version, ".bin")
	packer.manifestPath = files.TempFileName("manifest", ".json")
	packer.Provisioners = []interface{}{ShellProvisioner{
		Type:           "shell",
		ExecuteCommand: "sudo sh -c '{{ .Vars }} {{ .Path }}'",
		Scripts:        []string{bash},
	}}
	packer.PostProcessors = []interface{}{
		map[string]string{
			"type":       "manifest",
			"output":     packer.manifestPath,
			"strip_path": "true",
		},
	}
	return &packer, nil
}

func (packer *Packer) Build(ctx pkg.BuildContext) (*Manifest, error) {
	data, err := json.MarshalIndent(packer, "", "    ")
	if err != nil {
		return nil, err
	}

	if ctx.DryRun {
		fmt.Println(string(data))
		return &Manifest{}, nil
	}

	tmp := "packer-image-builder.json"
	if !logger.IsTraceEnabled() {
		defer os.Remove(tmp)
	}
	if err := ioutil.WriteFile(tmp, data, 0644); err != nil {
		return nil, err
	}
	logger.Secretf("\n%s\n", string(data))

	if err := packer.binary(" build %s", tmp); err != nil {
		return nil, err
	}

	manifestData, err := ioutil.ReadFile(packer.manifestPath)
	if err != nil {
		return nil, err
	}
	manifest := Manifest{}
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return nil, err
	}

	return &manifest, nil
}
