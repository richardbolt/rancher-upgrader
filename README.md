Rancher Upgrader
================

Rancher Upgrader upgrades a single service in place using the Rancher API executing an external task in
between upgrading and finishing the upgrade, to verify a blue-green deployment, rolling back if the
command fails and finishing the upgrade if successful.

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
UPGRADE_TEST_CMD
BUILD_TAG=latest
UPGRADE_WAIT_TIMEOUT=3600
RANCHER_API_VERSION=v1
```

Example of running with UPGRADE_TEST_CMD:

```
UPGRADE_TEST_CMD="./test-deploy.sh --url http://www.example.com/health -s 200" ./rancher-upgrader
```
