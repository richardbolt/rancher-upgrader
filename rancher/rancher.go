package rancher

// Config is the struct for holding the env variables passed into the program.
type Config struct {
	RancherEnvID             string `required:"true" envconfig:"RANCHER_ENV_ID"`
	RancherServiceID         string `required:"true" envconfig:"RANCHER_SERVICE_ID"`
	BuildTag                 string `default:"latest" envconfig:"BUILD_TAG"`
	RancherAccessKey         string `required:"true" envconfig:"RANCHER_ACCESS_KEY"`
	RancherSecretKey         string `required:"true" envconfig:"RANCHER_SECRET_KEY"`
	RancherURL               string `required:"true" envconfig:"RANCHER_URL"`
	RancherAPIVersion        string `default:"v1" envconfig:"RANCHER_API_VERSION"`
	RancherStartServiceFirst bool   `default:"false" envconfig:"RANCHER_SERVICE_START_FIRST"`
	RancherFinishUpgrade     bool   `default:"true" envconfig:"RANCHER_FINISH_UPGRADE"`
	// Cmd is a command that will be run and checked for exit status before moving onto the next stage of the upgrade.
	Cmd string `default:"" envconfig:"UPGRADE_TEST_CMD"`
	// Wait for at least x seconds (3600 by default) before abandoning the upgrade and rolling back automatically.
	UpgradeWaitTimeout int `default:"3600" envconfig:"UPGRADE_WAIT_TIMEOUT"`
	// Wait for x seconds in between each status check when waiting for services to transition state.
	CheckInterval int `default:"1" envconfig:"CHECK_INTERVAL"`
}

// InServiceStrategy is the upgrade strategy that can be applied to upgrade a service
type InServiceStrategy struct {
	BatchSize      int                    `json:"batchSize"`
	IntervalMillis int                    `json:"intervalMillis"`
	LaunchConfig   map[string]interface{} `json:"launchConfig"`
	StartFirst     bool                   `json:"startFirst"`
}

// Upgrade is the placeholder for the InServiceStrategy
type Upgrade struct {
	InServiceStrategy InServiceStrategy `json:"inServiceStrategy"`
}

// Service is the full service definition complete with useful actions and links
type Service struct {
	Name         string                 `json:"name"`
	State        string                 `json:"state"`
	Actions      Actions                `json:"actions"`
	Links        Links                  `json:"links"`
	LaunchConfig map[string]interface{} `json:"launchConfig"`
	Upgrade      Upgrade                `json:"upgrade"`
}

// Actions are the actions that can be performed on a resource.
type Actions struct {
	Upgrade  string `json:"upgrade"`
	Restart  string `json:"restart"`
	Start    string `json:"start"`
	Rollback string `json:"rollback"`
}

// Links are the urls that can give more information about a resource.
type Links struct {
	Instances string `json:"instances"`
}

// Instances is a holder for the containers that are associated with a given service.
type Instances struct {
	Containers []Container `json:"data"`
}

// Container is the container definition for an instance. Primarily so we can perform actions on it.
type Container struct {
	ID      string  `json:"id"`
	Type    string  `json:"type"`
	State   string  `json:"state"`
	Actions Actions `json:"actions"`
}
