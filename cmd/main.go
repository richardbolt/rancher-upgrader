// package main updates services via Rancher in a blue-green deployment manner
// offering the ability to run an external suite between upgrade and upgrade completion.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/richardbolt/rancher-upgrader/types"

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
	var cfg config
	err := envconfig.Process("", &cfg)
	if err != nil {
		log.Fatal(err.Error())
	}

	client = &http.Client{}

	// serviceURL is the Rancher url to make requests to for the service upgrade.
	serviceURL := fmt.Sprintf("%s/%s/projects/%s/services/%s",
		cfg.RancherURL,
		cfg.RancherAPIVersion,
		cfg.RancherEnvID,
		cfg.RancherServiceID,
	)
	// Get the launchConfig for the given service. what we're after is the imageUuid from the launchConfig.
	req, err := http.NewRequest(http.MethodGet, serviceURL, nil)
	req.SetBasicAuth(cfg.RancherAccessKey, cfg.RancherSecretKey)
	res, err := client.Do(req)
	if err != nil {
		log.Fatal(err.Error())
	}
	defer res.Body.Close()
	svcConfig := types.Service{}
	json.NewDecoder(res.Body).Decode(&svcConfig)
	if svcConfig.Actions.Upgrade == "" {
		log.Fatal("Exiting, service was not in an upgradeable state, got: ", svcConfig.State)
	}
	// get the imageUuid as a string from LaunchConfig
	imageUUID := svcConfig.LaunchConfig["imageUuid"].(string)
	// Update the LaunchConfig image tag to the specified BuildTag.
	imageUUID = regexp.MustCompile(":[a-z0-9]+$").ReplaceAllString(imageUUID, ":"+cfg.BuildTag)
	svcConfig.LaunchConfig["imageUuid"] = imageUUID
	// Make the upgrade request to the Rancher API for the given env and service
	data, err := json.Marshal(types.Upgrade{
		InServiceStrategy: types.InServiceStrategy{
			BatchSize:      svcConfig.Upgrade.InServiceStrategy.BatchSize,
			IntervalMillis: svcConfig.Upgrade.InServiceStrategy.IntervalMillis,
			LaunchConfig:   svcConfig.LaunchConfig,
			StartFirst:     cfg.RancherStartServiceFirst,
		},
	})
	log.Printf("Upgrading %s in env %s to version tag '%s'\n", svcConfig.Name, cfg.RancherEnvID, cfg.BuildTag)
	req, err = http.NewRequest(http.MethodPost, svcConfig.Actions.Upgrade, bytes.NewBuffer(data))
	req.Header.Add("Content-Type", "application/json")
	req.SetBasicAuth(cfg.RancherAccessKey, cfg.RancherSecretKey)
	res, err = client.Do(req)
	if err != nil {
		log.Fatal(err.Error())
	}
	// Block until the service "state" goes from "active" to "upgrading" and finally to "upgraded".
	// When we hit "upgraded" we can run external scripts to confirm, and then call ?action=finishupgrade to complete the upgrade.
	_, err = waitFor(cfg, svcConfig, serviceURL, "upgraded")
	if err != nil {
		log.Println("Cancelling upgrade")
		cancel(cfg, svcConfig, serviceURL)
		log.Fatal("Cancelled upgrade")
	}

	// We blocked above until the service was upgraded, now we can run a script to verify before we finish the upgrade.
	// We will block on this script until we get the upgrade completed.
	if cfg.Cmd != "" {
		cmdParts := strings.Split(cfg.Cmd, " ")
		if err := streamingExternalCmd(cmdParts[0], cmdParts[1:]...); err != nil {
			log.Println("External command failed, rolling back the service upgrade")
			err := rollback(cfg, svcConfig, serviceURL)
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
		req, err = http.NewRequest(http.MethodPost, serviceURL+"?action=finishupgrade", nil)
		req.SetBasicAuth(cfg.RancherAccessKey, cfg.RancherSecretKey)
		// NB: state becomes "finishing-upgrade" then "active"
		res, err = client.Do(req)
		if err != nil {
			log.Fatal(err.Error())
		}
		defer res.Body.Close()
		response, err := ioutil.ReadAll(res.Body)
		if err != nil {
			log.Fatal(err.Error())
		}
		log.Println(string(response))
	} else {
		log.Println("Service upgrade successful, skipping the finish upgrade step")
	}
}

// waitFor blocks until the service "state" goes to desiredState.
func waitFor(cfg config, svcConfig types.Service, serviceURL string, desiredState ...string) (*types.Service, error) {

	waitInterval, _ := time.ParseDuration(fmt.Sprintf("%ds", cfg.CheckInterval))
	waitTimeout, _ := time.ParseDuration(fmt.Sprintf("%ds", cfg.UpgradeWaitTimeout))
	desiredStates := map[string]struct{}{}
	for _, state := range desiredState {
		desiredStates[state] = struct{}{}
	}
	log.Printf("Waiting for service to reach '%s' state\n", desiredState)
	start := time.Now()
	for {
		// Check the service status
		req, err := http.NewRequest(http.MethodGet, serviceURL, nil)
		req.SetBasicAuth(cfg.RancherAccessKey, cfg.RancherSecretKey)
		res, err := client.Do(req)
		if err != nil {
			// Probably a network error
			log.Println(err.Error())
			continue
		}
		defer res.Body.Close()
		service := types.Service{}
		json.NewDecoder(res.Body).Decode(&service)
		log.Println("State", service.State)
		if _, ok := desiredStates[service.State]; ok == true {
			// state was one of the desiredStates
			return &service, nil
		}
		// Block for cfg.CheckInterval seconds each loop cycle.
		time.Sleep(waitInterval)
		if time.Since(start) > waitTimeout {
			log.Printf("Timed out waiting for '%s'", desiredState)
			return &service, errors.New("Timed out waiting for desiredState")
		}
	}
}

// cancel cancels the service upgrade and rolls back.
func cancel(cfg config, svcConfig types.Service, serviceURL string) error {
	req, err := http.NewRequest(http.MethodPost, serviceURL+"?action=cancelupgrade", nil)
	req.SetBasicAuth(cfg.RancherAccessKey, cfg.RancherSecretKey)
	// NB: state becomes "finishing-upgrade" then "active"
	res, err := client.Do(req)
	if err != nil {
		log.Fatal(err.Error())
	}
	defer res.Body.Close()
	response, err := ioutil.ReadAll(res.Body)
	log.Println(string(response))
	svc, err := waitFor(cfg, svcConfig, serviceURL, "upgraded", "canceled-upgrade", "active")
	if err != nil {
		log.Println(err.Error())
	}
	if svc != nil {
		// Now we've cancelled the upgrade we need to rollback (and restart containers as necessary)
		err = rollback(cfg, *svc, serviceURL)
		if err != nil {
			return err
		}
	} else {
		return errors.New("No updated service config available")
	}
	return nil
}

// rollback rolls the service back
// TODO: restart the od containers to actually complete the service rollback.
func rollback(cfg config, svcConfig types.Service, serviceURL string) error {
	req, err := http.NewRequest(http.MethodPost, serviceURL+"?action=rollback", nil)
	req.SetBasicAuth(cfg.RancherAccessKey, cfg.RancherSecretKey)
	// NB: state becomes "finishing-upgrade" then "active"
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	response, err := ioutil.ReadAll(res.Body)
	log.Println(string(response))

	svc, err := waitFor(cfg, svcConfig, serviceURL, "active")
	if err != nil {
		return err
	}
	if svc != nil {
		svcConfig = *svc
	}
	// Now restart the service containers (if any are not running) to make sure we've left things in a running state.
	err = startContainers(cfg, svcConfig, serviceURL)
	if err != nil {
		return err
	}
	log.Println("Rollback successful")
	return nil
}

// startContainers starts the service containers if they were in a startable state.
func startContainers(cfg config, svcConfig types.Service, serviceURL string) error {
	// Get the instances to make sure are running:
	req, err := http.NewRequest(http.MethodGet, svcConfig.Links.Instances, nil)
	req.SetBasicAuth(cfg.RancherAccessKey, cfg.RancherSecretKey)
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	instances := types.Instances{}
	err = json.NewDecoder(res.Body).Decode(&instances)
	if err != nil {
		return err
	}
	// Make sure to start the instances if they can be started:
	for _, container := range instances.Containers {
		if container.Actions.Start == "" {
			log.Printf("%s %s was in a %s state and could not be started", container.Type, container.ID, container.State)
			continue
		}
		log.Printf("Starting %s %s which was in a %s state", container.Type, container.ID, container.State)
		req, err := http.NewRequest(http.MethodPost, container.Actions.Start, nil)
		req.SetBasicAuth(cfg.RancherAccessKey, cfg.RancherSecretKey)
		res, err = client.Do(req)
		if err != nil {
			return err
		}
	}
	return nil
}

// streamingExternalCmd takes a command string with a list of string args and runs the command.
// It streams the command output to stdout and stderr (to stderr) and returns an error if the command
// exits with a non-zero status code.
func streamingExternalCmd(command string, args ...string) error {
	cmd := exec.Command(command, args...)
	cmdReader, err := cmd.StdoutPipe()
	if err != nil {
		log.Println("Error creating StdoutPipe for external command", err)
		return err
	}
	// Asyncify the output from the command and print it out.
	scanner := bufio.NewScanner(cmdReader)
	go func() {
		for scanner.Scan() {
			fmt.Printf(scanner.Text())
		}
	}()

	log.Println("Starting external command")
	err = cmd.Start()
	if err != nil {
		log.Println("Error with external command", err)
		return err
	}

	err = cmd.Wait()
	if err != nil {
		log.Println("Error waiting for external command", err)
		return err
	}
	return nil
}
