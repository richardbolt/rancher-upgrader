package actions

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"time"

	"github.com/richardbolt/rancher-upgrader/types"
)

// WaitFor blocks until the service "state" goes to desiredState.
func WaitFor(client *http.Client, cfg types.Config, svcConfig *types.Service, serviceURL string, desiredState ...string) (*types.Service, error) {
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

// GetServiceConfig gets the service configuration for the given environment cfg and serviceURL.
func GetServiceConfig(client *http.Client, cfg types.Config, serviceURL string) (*types.Service, error) {
	// Get the launchConfig for the given service. what we're after is the imageUuid from the launchConfig.
	req, err := http.NewRequest(http.MethodGet, serviceURL, nil)
	req.SetBasicAuth(cfg.RancherAccessKey, cfg.RancherSecretKey)
	res, err := client.Do(req)
	if err != nil {
		log.Println(err.Error())
		return nil, err
	}
	defer res.Body.Close()
	svcConfig := types.Service{}
	err = json.NewDecoder(res.Body).Decode(&svcConfig)
	if err != nil {
		return nil, err
	}
	return &svcConfig, nil
}

// Upgrade kicks off the upgrade process with the given environment cfg and svcConfig.
func Upgrade(client *http.Client, cfg types.Config, svcConfig *types.Service, serviceURL string, payload types.Upgrade) error {
	log.Printf("Upgrading %s in env %s to version tag '%s'\n", svcConfig.Name, cfg.RancherEnvID, cfg.BuildTag)
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, svcConfig.Actions.Upgrade, bytes.NewBuffer(data))
	if err != nil {
		return err
	}
	req.Header.Add("Content-Type", "application/json")
	req.SetBasicAuth(cfg.RancherAccessKey, cfg.RancherSecretKey)
	_, err = client.Do(req)
	if err != nil {
		return err
	}
	return nil
}

func FinishUpgrade(client *http.Client, cfg types.Config, svcConfig *types.Service, serviceURL string) error {
	req, err := http.NewRequest(http.MethodPost, serviceURL+"?action=finishupgrade", nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(cfg.RancherAccessKey, cfg.RancherSecretKey)
	// NB: state becomes "finishing-upgrade" then "active"
	res, err := client.Do(req)
	if err != nil {
		log.Fatal(err.Error())
	}
	defer res.Body.Close()
	response, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return err
	}
	log.Println(string(response))
	return nil
}

// Cancel cancels the service upgrade and rolls back.
func Cancel(client *http.Client, cfg types.Config, svcConfig *types.Service, serviceURL string) error {
	req, err := http.NewRequest(http.MethodPost, serviceURL+"?action=cancelupgrade", nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(cfg.RancherAccessKey, cfg.RancherSecretKey)
	// NB: state becomes "finishing-upgrade" then "active"
	res, err := client.Do(req)
	if err != nil {
		log.Println(err.Error())
		return err
	}
	defer res.Body.Close()
	response, err := ioutil.ReadAll(res.Body)
	log.Println(string(response))
	svc, err := WaitFor(client, cfg, svcConfig, serviceURL, "upgraded", "canceled-upgrade", "active")
	if err != nil {
		log.Println(err.Error())
		return err
	}
	if svc != nil {
		// Now we've cancelled the upgrade we need to rollback (and restart containers as necessary)
		err = Rollback(client, cfg, svc, serviceURL)
		if err != nil {
			return err
		}
	} else {
		return errors.New("No updated service config available")
	}
	return nil
}

// Rollback rolls the service back and makes sure containers are restarted.
func Rollback(client *http.Client, cfg types.Config, svcConfig *types.Service, serviceURL string) error {
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

	svc, err := WaitFor(client, cfg, svcConfig, serviceURL, "active")
	if err != nil {
		return err
	}
	// Now restart the service containers (if any are not running) to make sure we've left things in a running state.
	err = startContainers(client, cfg, svc, serviceURL)
	if err != nil {
		return err
	}
	log.Println("Rollback successful")
	return nil
}

// startContainers starts the service containers if they were in a startable state.
func startContainers(client *http.Client, cfg types.Config, svcConfig *types.Service, serviceURL string) error {
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
