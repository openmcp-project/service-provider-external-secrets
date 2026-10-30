package externalsecrets

const (
	// ChartPullSecretPrefix is the prefix with which the registry pull secret
	// will be prefixed in the tenant namespace in the platform cluster
	ChartPullSecretPrefix = "sp-eso-"
	// DefaultNamespace is the default namespace in which the external-secrets-operator will be deployed in the mcp
	DefaultNamespace = "external-secrets"
	// DefaultChartURL is the default value to reference the repository that hosts the external-secrets Helm chart.
	// Note that this value is expected to be defaulted by the API server and has to be kept in sync with the kubebuilder:default marker.
	DefaultChartURL = "oci://ghcr.io/external-secrets/charts/external-secrets"
	// HelmReleaseName is the name of the External Secrets Operator HelmRelease resource
	HelmReleaseName = "external-secrets"
	// OCIRepositoryName is the name of the External Secrets Operator OCIRepository resource
	OCIRepositoryName = "external-secrets"
	// LabelManagedByValue defines the managed-by value that is added to every managed object.
	LabelManagedByValue = "service-provider-external-secrets"
)
