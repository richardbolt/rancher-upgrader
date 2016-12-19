package upgrader

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"time"

	"github.com/richardbolt/rancher-upgrader/rancher"
)

type rancherUpgrader struct {
	svcURL string
	client *http.Client
	cfg    rancher.Config
}

// New returns an implementation of the Upgrader interface.
func New(c *http.Client, cfg rancher.Config) Upgrader {
	// serviceURL is the Rancher url to make requests to for the service upgrade.
	svcURL := fmt.Sprintf("%s/%s/projects/%s/services/%s",
		cfg.RancherURL,
		cfg.RancherAPIVersion,
		cfg.RancherEnvID,
		cfg.RancherServiceID,
	)

	return &rancherUpgrader{
		svcURL: svcURL,
		client: c,
		cfg: cfg,
	}
}

// Upgrader defines methods for service upgrading.
type Upgrader interface {
	Upgrade(payload rancher.Upgrade, options ...Option) error
	WaitFor(desiredStates ...string) (*rancher.Service, error)
	GetServiceConfig() (*rancher.Service, error)
	FinishUpgrade() (*rancher.Service, error)
	Cancel() error
	Rollback() error
}

// Option will allow for modifying the Service definition for upgrading.
type Option func(*rancher.Service)

// ImageUUID allows for updating the Service's image UUID when calling Upgrade
func ImageUUID(uuid string) Option {
	return func(s *rancher.Service) {
		s.LaunchConfig["imageUuid"] = uuid
	}
}

// WaitFor blocks until the service "state" goes to desiredState.
func (r *rancherUpgrader) WaitFor(desiredState ...string) (*rancher.Service, error) {
	waitInterval, _ := time.ParseDuration(fmt.Sprintf("%ds", r.cfg.CheckInterval))
	waitTimeout, _ := time.ParseDuration(fmt.Sprintf("%ds", r.cfg.UpgradeWaitTimeout))
	desiredStates := map[string]struct{}{}
	for _, state := range desiredState {
		desiredStates[state] = struct{}{}
	}
	log.Printf("Waiting for service to reach '%s' state\n", desiredState)
	start := time.Now()
	for {
		// Check the service status
		req, err := http.NewRequest(http.MethodGet, r.svcURL, nil)
		req.SetBasicAuth(r.cfg.RancherAccessKey, r.cfg.RancherSecretKey)
		res, err := r.client.Do(req)
		if err != nil {
			// Probably a network error
			log.Println(err.Error())
			continue
		}
		defer res.Body.Close()
		service := rancher.Service{}
		json.NewDecoder(res.Body).Decode(&service)
		log.Println("State", service.State)
		if _, ok := desiredStates[service.State]; ok {
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
func (r *rancherUpgrader) GetServiceConfig() (*rancher.Service, error) {
	// Get the launchConfig for the given service. what we're after is the imageUuid from the launchConfig.
	req, err := http.NewRequest(http.MethodGet, r.svcURL, nil)
	req.SetBasicAuth(r.cfg.RancherAccessKey, r.cfg.RancherSecretKey)
	res, err := r.client.Do(req)
	if err != nil {
		log.Println(err.Error())
		return nil, err
	}
	defer res.Body.Close()
	svcConfig := rancher.Service{}
	err = json.NewDecoder(res.Body).Decode(&svcConfig)
	if err != nil {
		return nil, err
	}
	return &svcConfig, nil
}

// Upgrade kicks off the upgrade process with the given environment cfg and svcConfig.
func (r *rancherUpgrader) Upgrade(payload rancher.Upgrade, options ...Option) error {
	svcConfig, err := r.GetServiceConfig()
	
	if err != nil {
		return err
	}
	
	for _, o := range options {
		o(svcConfig)
	}
	
	log.Printf("Upgrading %s in env %s to version tag '%s'\n", svcConfig.Name, r.cfg.RancherEnvID, r.cfg.BuildTag)
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, svcConfig.Actions.Upgrade, bytes.NewBuffer(data))
	if err != nil {
		return err
	}
	req.Header.Add("Content-Type", "application/json")
	req.SetBasicAuth(r.cfg.RancherAccessKey, r.cfg.RancherSecretKey)
	_, err = r.client.Do(req)
	if err != nil {
		return err
	}
	return nil
}

// FinishUpgrade finishes the upgrade and blocks until the service is in an active state before returning.
func (r *rancherUpgrader) FinishUpgrade() (*rancher.Service, error) {
	req, err := http.NewRequest(http.MethodPost, r.svcURL + "?action=finishupgrade", nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(r.cfg.RancherAccessKey, r.cfg.RancherSecretKey)
	// NB: state becomes "finishing-upgrade" then "active"
	res, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	svc := rancher.Service{}
	err = json.NewDecoder(res.Body).Decode(&svc)
	if err != nil {
		return nil, err
	}
	log.Printf("Finishing upgrade of %s", svc.Name)
	svcCfg, err := r.WaitFor("active")
	if err != nil {
		return nil, err
	}
	return svcCfg, nil
}

// Cancel cancels the service upgrade and rolls back.
func (r *rancherUpgrader) Cancel() error {
	req, err := http.NewRequest(http.MethodPost, r.svcURL + "?action=cancelupgrade", nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(r.cfg.RancherAccessKey, r.cfg.RancherSecretKey)
	// NB: state becomes "finishing-upgrade" then "active"
	res, err := r.client.Do(req)
	if err != nil {
		log.Println(err.Error())
		return err
	}
	defer res.Body.Close()
	response, err := ioutil.ReadAll(res.Body)
	log.Println(string(response))
	svc, err := r.WaitFor("upgraded", "canceled-upgrade", "active")
	if err != nil {
		log.Println(err.Error())
		return err
	}
	if svc != nil {
		// Now we've cancelled the upgrade we need to rollback (and restart containers as necessary)
		err = r.Rollback()
		if err != nil {
			return err
		}
	} else {
		return errors.New("No updated service config available")
	}
	return nil
}

// Rollback rolls the service back and makes sure containers are restarted.
func (r *rancherUpgrader) Rollback() error {
	req, err := http.NewRequest(http.MethodPost, r.svcURL + "?action=rollback", nil)
	req.SetBasicAuth(r.cfg.RancherAccessKey, r.cfg.RancherSecretKey)
	// NB: state becomes "finishing-upgrade" then "active"
	res, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	response, err := ioutil.ReadAll(res.Body)
	log.Println(string(response))

	svc, err := r.WaitFor("active")
	if err != nil {
		return err
	}
	// Now restart the service containers (if any are not running) to make sure we've left things in a running state.
	err = startContainers(r.client, r.cfg, svc)
	if err != nil {
		return err
	}
	log.Println("Rollback successful")
	return nil
}

// startContainers starts the service containers if they were in a startable state.
func startContainers(client *http.Client, cfg rancher.Config, svcConfig *rancher.Service) error {
	// Get the instances to make sure are running:
	req, err := http.NewRequest(http.MethodGet, svcConfig.Links.Instances, nil)
	req.SetBasicAuth(cfg.RancherAccessKey, cfg.RancherSecretKey)
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	instances := rancher.Instances{}
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
