// package main updates services via Rancher in a blue-green deployment manner
// offering the ability to run an external suite between upgrade and upgrade completion.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"time"

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
	// Cmd is a command that will be run and checked for exit status before moving onto the next stage of the upgrade.
	Cmd string `default:"" envconfig:"UPGRADE_TEST_CMD"`
	// Wait for at least x seconds (3600 by default) before abandoning the upgrade and rolling back automatically.
	UpgradeWaitTimeout int `default:"3600" envconfig:"UPGRADE_WAIT_TIMEOUT"`
}

// client is the http.Client to make GET requests
var client *http.Client

type inServiceStrategy struct {
	BatchSize      int                    `json:"batchSize"`
	IntervalMillis int                    `json:"intervalMillis"`
	LaunchConfig   map[string]interface{} `json:"launchConfig"`
	StartFirst     bool                   `json:"startFirst"`
}
type upgradePayload struct {
	InServiceStrategy inServiceStrategy `json:"inServiceStrategy"`
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
	launchConfig := struct {
		State        string                 `json:"state"`
		LaunchConfig map[string]interface{} `json:"launchConfig"`
	}{}
	json.NewDecoder(res.Body).Decode(&launchConfig)
	if launchConfig.State != "active" {
		log.Fatal("Exiting, Service state was not 'active', got: ", launchConfig.State)
	}
	// get the imageUuid as a string from LaunchConfig
	imageUUID := launchConfig.LaunchConfig["imageUuid"].(string)
	// Update the launchConfig image tag to the specified BuildTag.
	imageUUID = regexp.MustCompile(":[a-z0-9]+$").ReplaceAllString(imageUUID, ":"+cfg.BuildTag)
	launchConfig.LaunchConfig["imageUuid"] = imageUUID
	// Make the upgrade request to the Rancher API for the given env and service
	data, err := json.Marshal(upgradePayload{
		InServiceStrategy: inServiceStrategy{
			BatchSize:      1,
			IntervalMillis: 10000,
			LaunchConfig:   launchConfig.LaunchConfig,
			StartFirst:     cfg.RancherStartServiceFirst,
		},
	})
	req, err = http.NewRequest(http.MethodPost, serviceURL+"?action=upgrade", bytes.NewBuffer(data))
	req.Header.Add("Content-Type", "application/json")
	req.SetBasicAuth(cfg.RancherAccessKey, cfg.RancherSecretKey)
	res, err = client.Do(req)
	if err != nil {
		log.Fatal(err.Error())
	}
	// Block until the service "state" goes from "active" to "upgrading" and finally to "upgraded".
	// When we hit "upgraded" we can run external scripts to confirm, and then call ?action=finishupgrade to complete the upgrade.
	t := 0
	waitInterval := 10
	for {
		time.Sleep(time.Second * 10) // Block for 10 seconds each loop cycle.
		t += waitInterval
		if cfg.UpgradeWaitTimeout < t {
			log.Println("Timed out waiting for the upgrade to complete, cancelling.", cfg.UpgradeWaitTimeout, t)
			cancel(cfg, serviceURL)
			log.Fatal("Upgrade cancelled.") // log.Fatal exits the program.
		}
		// Check the upgrade status
		req, err := http.NewRequest(http.MethodGet, serviceURL, nil)
		req.SetBasicAuth(cfg.RancherAccessKey, cfg.RancherSecretKey)
		res, err := client.Do(req)
		if err != nil {
			log.Fatal(err.Error())
		}
		defer res.Body.Close()
		service := struct {
			State string `json:"state"`
		}{}
		json.NewDecoder(res.Body).Decode(&service)
		// State goes from "active" to "upgrading" and finally to "upgraded" where we exit. "removed" means we should have already exited
		log.Println("State", service.State)
		if service.State == "upgraded" {
			break
		}
	}

	// We blocked above until the service was upgraded, now we can run a script to verify before we finish the upgrade.
	// We will block on this script until we get the upgrade completed.
	if cfg.Cmd != "" {
		cmdParts := strings.Split(cfg.Cmd, " ")
		if err := streamingExternalCmd(cmdParts[0], cmdParts[1:]...); err != nil {
			log.Println("External service failed, rolling back the service upgrade")
			rollback(cfg, serviceURL)
			log.Fatal("Rolled back")
		}
	}

	// POST to ?action=finishupgrade will finish the upgrade and ?action=rollback will rollback.
	// Rolling back is dangerous since it will leave the other containers in a stopped state and they will
	// need to be started here automatically.
	req, err = http.NewRequest(http.MethodPost, serviceURL+"?action=finishupgrade", nil)
	req.SetBasicAuth(cfg.RancherAccessKey, cfg.RancherSecretKey)
	// NB: state becomes "finishing-upgrade" then "active"
	res, err = client.Do(req)
	if err != nil {
		log.Fatal(err.Error())
	}
	defer res.Body.Close()
	response, err := ioutil.ReadAll(res.Body)
	log.Println(string(response))

}

// cancel cancels the service upgrade
func cancel(cfg config, serviceURL string) {
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
}

// rollback rolls the service back
// TODO: restart the od containers to actually complete the service rollback.
func rollback(cfg config, serviceURL string) {
	req, err := http.NewRequest(http.MethodPost, serviceURL+"?action=rollback", nil)
	req.SetBasicAuth(cfg.RancherAccessKey, cfg.RancherSecretKey)
	// NB: state becomes "finishing-upgrade" then "active"
	res, err := client.Do(req)
	if err != nil {
		log.Fatal(err.Error())
	}
	defer res.Body.Close()
	response, err := ioutil.ReadAll(res.Body)
	log.Println(string(response))
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
