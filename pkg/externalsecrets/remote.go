// Copyright 2026.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package externalsecrets

import (
	"encoding/json"
	"fmt"
	"slices"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

// ConfigureRemoteMCPControllers installs only the controller on the platform cluster.
// The MCP must already expose the External Secrets APIs without a conversion webhook.
func ConfigureRemoteMCPControllers(values *apiextensionsv1.JSON, secret, namespace, hash string) (*apiextensionsv1.JSON, error) {
	if secret == "" || namespace == "" {
		return nil, fmt.Errorf("MCP credential and controller namespace must be set")
	}
	root, err := decodeRemoteValues(values)
	if err != nil {
		return nil, err
	}
	if err := setRemotePodValues(root, secret); err != nil {
		return nil, err
	}
	if err := disableLocalAPIServices(root); err != nil {
		return nil, err
	}
	if err := setCredentialAnnotation(root, hash); err != nil {
		return nil, err
	}
	root["namespaceOverride"], _ = json.Marshal(namespace)
	out, err := json.Marshal(root)
	if err != nil {
		return nil, err
	}
	return &apiextensionsv1.JSON{Raw: out}, nil
}

func decodeValue(root map[string]json.RawMessage, key string, value any) error {
	if raw, ok := root[key]; ok {
		if err := json.Unmarshal(raw, value); err != nil {
			return fmt.Errorf("invalid %s: %w", key, err)
		}
	}
	return nil
}

func setRemotePodValues(root map[string]json.RawMessage, secret string) error {
	var volumes []corev1.Volume
	var mounts []corev1.VolumeMount
	var env []corev1.EnvVar
	for _, field := range []struct {
		name  string
		value any
	}{
		{"extraVolumes", &volumes}, {"extraVolumeMounts", &mounts}, {"extraEnv", &env},
	} {
		if err := decodeValue(root, field.name, field.value); err != nil {
			return err
		}
	}
	volumes = slices.DeleteFunc(volumes, func(v corev1.Volume) bool { return v.Name == "mcp-kubeconfig" })
	volumes = append(volumes, corev1.Volume{Name: "mcp-kubeconfig", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: secret, Items: []corev1.KeyToPath{{Key: "kubeconfig", Path: "kubeconfig"}}}}})
	mounts = slices.DeleteFunc(mounts, func(v corev1.VolumeMount) bool {
		return v.Name == "mcp-kubeconfig" || v.MountPath == "/etc/open-control-plane/mcp"
	})
	mounts = append(mounts, corev1.VolumeMount{Name: "mcp-kubeconfig", MountPath: "/etc/open-control-plane/mcp", ReadOnly: true})
	env = slices.DeleteFunc(env, func(v corev1.EnvVar) bool { return v.Name == "KUBECONFIG" })
	env = append(env, corev1.EnvVar{Name: "KUBECONFIG", Value: "/etc/open-control-plane/mcp/kubeconfig"})
	root["extraVolumes"], _ = json.Marshal(volumes)
	root["extraVolumeMounts"], _ = json.Marshal(mounts)
	root["extraEnv"], _ = json.Marshal(env)
	return nil
}

func disableLocalAPIServices(root map[string]json.RawMessage) error {
	root["installCRDs"] = json.RawMessage("false")
	for _, name := range []string{"rbac", "webhook", "certController"} {
		settings := map[string]json.RawMessage{}
		if err := decodeValue(root, name, &settings); err != nil {
			return err
		}
		if settings == nil {
			settings = map[string]json.RawMessage{}
		}
		settings["create"] = json.RawMessage("false")
		root[name], _ = json.Marshal(settings)
	}
	return nil
}

func setCredentialAnnotation(root map[string]json.RawMessage, hash string) error {
	annotations := map[string]string{}
	if err := decodeValue(root, "podAnnotations", &annotations); err != nil {
		return err
	}
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations["open-control-plane.io/mcp-credential-hash"] = hash
	root["podAnnotations"], _ = json.Marshal(annotations)
	return nil
}

func decodeRemoteValues(values *apiextensionsv1.JSON) (map[string]json.RawMessage, error) {
	root := map[string]json.RawMessage{}
	if values != nil && len(values.Raw) > 0 {
		if err := json.Unmarshal(values.Raw, &root); err != nil {
			return nil, err
		}
	}
	if root == nil {
		root = map[string]json.RawMessage{}
	}
	return root, nil
}
