// package main updates services via Rancher in a blue-green deployment manner
// offering the ability to run an external suite between upgrade and upgrade completion.
package main

import (
	"log"
	"net/http"
	"regexp"
	"strings"

	"github.com/richardbolt/rancher-upgrader/upgrader"
	"github.com/richardbolt/rancher-upgrader/rancher"
	"github.com/kelseyhightower/envconfig"
)

// config is the struct for holding the env variables passed into the program.
type config struct {
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

// client is the http.Client to make GET requests
var client *http.Client

func init() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
}

func main() {
	var cfg rancher.Config
	err := envconfig.Process("", &cfg)
	if err != nil {
		log.Fatal(err.Error())
	}

	client = &http.Client{}
	ru := upgrader.New(client, cfg)

	// Get the launchConfig for the given service. what we're after is the imageUuid from the launchConfig.
	svcConfig, err := ru.GetServiceConfig()
	if svcConfig.Actions.Upgrade == "" {
		log.Fatal("Exiting, service was not in an upgradeable state, got: ", svcConfig.State)
	}
	// get the imageUuid as a string from LaunchConfig
	imageUUID := svcConfig.LaunchConfig["imageUuid"].(string)
	// Update the LaunchConfig image tag to the specified BuildTag.
	imageUUID = regexp.MustCompile(":[a-z0-9]+$").ReplaceAllString(imageUUID, ":"+cfg.BuildTag)

	// Make the upgrade request to the Rancher API for the given env and service
	err = ru.Upgrade(rancher.Upgrade{
		InServiceStrategy: rancher.InServiceStrategy{
			BatchSize:      svcConfig.Upgrade.InServiceStrategy.BatchSize,
			IntervalMillis: svcConfig.Upgrade.InServiceStrategy.IntervalMillis,
			LaunchConfig:   svcConfig.LaunchConfig,
			StartFirst:     cfg.RancherStartServiceFirst,
		},
	}, upgrader.ImageUUID(imageUUID))
	if err != nil {
		log.Fatal(err.Error())
	}
	// Block until the service "state" goes from "active" to "upgrading" and finally to "upgraded".
	// When we hit "upgraded" we can run external scripts to confirm, and then call ?action=finishupgrade to complete the upgrade.
	_, err = ru.WaitFor("upgraded")
	if err != nil {
		log.Println("Cancelling upgrade")
		ru.Cancel()
		log.Fatal("Cancelled upgrade")
	}

	// We blocked above until the service was upgraded, now we can run a script to verify before we finish the upgrade.
	// We will block on this script until we get the upgrade completed.
	if cfg.Cmd != "" {
		cmdParts := strings.Split(cfg.Cmd, " ")
		if err := upgrader.StreamingExternalCmd(cmdParts[0], cmdParts[1:]...); err != nil {
			log.Println("External command failed, rolling back the service upgrade")
			err := ru.Rollback()
			if err != nil {
				log.Fatal("Failed to rollback", err.Error())
			}
			log.Fatal("Rolled back")
		}
	}

	// POST to ?action=finishupgrade will finish the upgrade and ?action=rollback will rollback.
	// Rolling back is dangerous since it will leave the other containers in a stopped state and they will
	// need to be started here automatically.
	if cfg.RancherFinishUpgrade {
		log.Println("Service upgraded, finishing the upgrade")
		svc, err := ru.FinishUpgrade()
		if err != nil {
			log.Fatal(err.Error())
		}
		log.Printf("Service upgrade successful, finished upgrade of %s\n", svc.Name)
	} else {
		log.Println("Service upgrade successful, skipping the finish upgrade step")
	}
}
