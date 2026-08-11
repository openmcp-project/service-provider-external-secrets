package externalsecrets

const (
	// DefaultNamespace is the default namespace in which the external-secrets-operator will be deployed in the mcp
	DefaultNamespace = "external-secrets"
	// ChartPullSecretPrefix is the prefix with which the registry pull secret
	// will be prefixed in the tenant namespace in the platform cluster
	ChartPullSecretPrefix = "sp-eso-"
)
