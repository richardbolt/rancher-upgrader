Rancher Upgrader
================

Rancher Upgrader upgrades a single service in place using the Rancher API executing an external task in
between upgrading and finishing the upgrade, to verify a blue-green deployment, rolling back if the
command fails and finishing the upgrade if successful.

Rancher Upgrader tries to leave the service in a running state if the operations needs to be cancelled.
Cancelling due to a timeout will lead to a cancel upgrade request, a rollback request, and then an
attempt to start any containers that are not in a running state. 

Build
-----

Build the statically linked Linux binary:

```
make
```

Clean:

```
make clean
```

Build locally for your current platform:

```
make local
```

Usage
-----

`./bin/rancher-upgrader`

### Required Env Vars

```
RANCHER_URL
RANCHER_ENV_ID
RANCHER_SERVICE_ID
RANCHER_ACCESS_KEY
RANCHER_SECRET_KEY
```

### Optional Env Vars

```
BUILD_TAG=latest
RANCHER_SERVICE_START_FIRST=false
RANCHER_FINISH_UPGRADE=true # "finishes" the upgrade after it has completed. Make false to leave the old containers around. 
UPGRADE_TEST_CMD # The test command to run verifying the upgrade was successful. 
UPGRADE_WAIT_TIMEOUT=3600 # wait this many seconds during any wait to determine if we should cancel the upgrade and attempt to rollback.
CHECK_INTERVAL=1 # Check every x seconds on the status of the service during operations.
RANCHER_API_VERSION=v1 # Version of the Rancher API to use
```

Example of running with UPGRADE_TEST_CMD:

```
UPGRADE_TEST_CMD="./test-deploy.sh --url http://www.example.com/health -s 200" ./rancher-upgrader
```
