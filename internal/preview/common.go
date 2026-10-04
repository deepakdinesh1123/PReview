package preview

// CommonFlags holds the configuration for a preview deployment.
type CommonFlags struct {
	AppPath          string   `name:"app-path" help:"Directory containing the Dockerfile and application sources (zipped as the image build context)" default:"." type:"existingdir"`
	BaseImageARN     string   `name:"base-image-arn" help:"ARN of the Lambda-managed base MicroVM image" default:"arn:aws:lambda:us-east-2:aws:microvm-image:al2023-1" env:"PREVIEW_BASE_IMAGE_ARN"`
	BuildRoleARN     string   `name:"build-role-arn" help:"IAM role Lambda assumes to read the code artifact and build the image" required:"" env:"PREVIEW_BUILD_ROLE_ARN"`
	ExecutionRoleARN string   `name:"execution-role-arn" help:"Optional IAM role the running MicroVM assumes" env:"PREVIEW_EXECUTION_ROLE_ARN"`
	S3Path           string   `name:"s3-path" help:"s3://bucket/prefix where build artifacts are uploaded" required:"" env:"PREVIEW_S3_PATH"`
	NamePrefix       string   `name:"name-prefix" help:"Prefix of every image name; scopes operations to PReview-managed images" default:"preview" env:"PREVIEW_NAME_PREFIX"`
	Port             int      `name:"port" help:"Port your application listens on inside the MicroVM" default:"8080" env:"PREVIEW_PORT"`
	Memory           int      `name:"memory" help:"Minimum memory to allocate, in MiB" default:"2048" env:"PREVIEW_MEMORY"`
	TTL              string   `name:"ttl" help:"Maximum lifetime of a deployment (e.g. 90m, 8h, infinite). Platform limit is 8h" default:"8h" env:"PREVIEW_TTL"`
	IdleDuration     string   `name:"idle-duration" help:"Inactivity before the MicroVM is suspended (it auto-resumes on the next request)" default:"15m" env:"PREVIEW_IDLE_DURATION"`
	SuspendedFor     string   `name:"suspended-duration" help:"How long a suspended MicroVM is kept before termination" default:"1h" env:"PREVIEW_SUSPENDED_DURATION"`
	IngressNetwork   []string `name:"ingress-network-connector" help:"Ingress network connector (repeatable)" env:"PREVIEW_INGRESS_NETWORK_CONNECTORS"`
	EgressNetwork    []string `name:"egress-network-connector" help:"Egress network connector (repeatable)" env:"PREVIEW_EGRESS_NETWORK_CONNECTORS"`
}
