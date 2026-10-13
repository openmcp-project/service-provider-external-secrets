package externalsecrets

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

func TestRemoteControllerValues(t *testing.T) {
	values := &apiextensionsv1.JSON{Raw: []byte(`{"replicaCount":2,"extraEnv":[{"name":"KEEP","value":"yes"},{"name":"KUBECONFIG","value":"old"}],"podAnnotations":{"custom":"retained"}}`)}
	result, err := ConfigureRemoteMCPControllers(values, "access", "tenant", "first")
	require.NoError(t, err)
	var got struct {
		ReplicaCount      int                            `json:"replicaCount"`
		ExtraEnv          []struct{ Name, Value string } `json:"extraEnv"`
		PodAnnotations    map[string]string              `json:"podAnnotations"`
		NamespaceOverride string                         `json:"namespaceOverride"`
		InstallCRDs       bool                           `json:"installCRDs"`
		Webhook           struct{ Create bool }          `json:"webhook"`
	}
	require.NoError(t, json.Unmarshal(result.Raw, &got))
	require.Equal(t, 2, got.ReplicaCount)
	require.Equal(t, "tenant", got.NamespaceOverride)
	require.False(t, got.InstallCRDs)
	require.False(t, got.Webhook.Create)
	require.Len(t, got.ExtraEnv, 2)
	require.Equal(t, "KEEP", got.ExtraEnv[0].Name)
	require.Equal(t, "/etc/open-control-plane/mcp/kubeconfig", got.ExtraEnv[1].Value)
	require.Equal(t, "retained", got.PodAnnotations["custom"])
	require.Equal(t, "first", got.PodAnnotations["open-control-plane.io/mcp-credential-hash"])
	rotated, err := ConfigureRemoteMCPControllers(result, "access", "tenant", "second")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(rotated.Raw, &got))
	require.Len(t, got.ExtraEnv, 2)
	require.Equal(t, "second", got.PodAnnotations["open-control-plane.io/mcp-credential-hash"])
}

func TestRemoteControllerRejectsMalformedValues(t *testing.T) {
	for _, raw := range []string{`{"extraEnv":"bad"}`, `{"extraVolumes":{}}`, `{"rbac":true}`, `{"podAnnotations":[]}`} {
		t.Run(raw, func(t *testing.T) {
			_, err := ConfigureRemoteMCPControllers(&apiextensionsv1.JSON{Raw: []byte(raw)}, "access", "tenant", "hash")
			require.Error(t, err)
		})
	}
}
