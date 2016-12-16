package types

// InServiceStrategy is the upgrade strategy that can be applied to upgrade a service
type InServiceStrategy struct {
	BatchSize      int                    `json:"batchSize"`
	IntervalMillis int                    `json:"intervalMillis"`
	LaunchConfig   map[string]interface{} `json:"launchConfig"`
	StartFirst     bool                   `json:"startFirst"`
}

// Upgrade is the plaheholder for the InServiceStrategy
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
