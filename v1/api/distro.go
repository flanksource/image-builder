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

package api

// Distribution describes a specific version of an Operation System
// and includes details for how to find images that is used by the various drivers
type Distribution struct {
	OS                  string       `yaml:"os,omitempty"`
	AMI                 *AMI         `yaml:"ami,omitempty"`
	Qemu                *DiskImage   `yaml:"qemu,omitempty"`
	GCE                 *GCEImage    `yaml:"gce,omitempty"`
	Azure               *AzureImage  `yaml:"azure,omitempty"`
	Docker              *DockerImage `yaml:"docker,omitempty"`
	ISO                 *ISO         `yaml:"iso,omitempty"`
	OVA                 *OVA         `yaml:"ova,omitempty`
	Distribution        string       `yaml:"distribution,omitempty"`
	DistributionRelease string       `yaml:"distribution_release,omitempty"`
	DistributionVersion string       `yaml:"distribution_version,omitempty"`
	Family              string       `yaml:"family,omitempty"`
	SSHUsername         string       `yaml:"ssh_username,omitempty"`
}

func (d Distribution) String() string {
	return d.OS
}

func (d Distribution) GetImageByKind(kind string) Image {
	switch kind {
	case "qemu", "img", "qcow2":
		if d.Qemu == nil {
			return nil
		}
		return *d.Qemu
	case "ova", "vpshere", "vm":
		if d.OVA == nil {
			return nil
		}
		return *d.OVA
	case "ami", "amazon-ebs", "aws":
		if d.AMI == nil {
			return nil
		}
		return *d.AMI
	case "azure":
		if d.Azure == nil {
			return nil
		}
		return *d.Azure
	case "gce":
		if d.GCE == nil {
			return nil
		}
		return *d.GCE
	case "docker":
		if d.Docker == nil {
			return nil
		}
		return *d.Docker
	case "iso":
		if d.ISO == nil {
			return nil
		}
		return *d.ISO
	}
	return nil

}
