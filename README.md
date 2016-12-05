Upgrader
========

Rancher upgrader upgrades a single service in place using the Rancher API.

Eventually it will be able to execute an external task in between upgrading and finishing the upgrade, to verify a blue-green deployment, rolling back if necessary.

Build
-----

Build the statically linked Linux binary:

```make```

Clean:

```make clean```

Build locally for your current platform:

```make local```

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
UPGRADE_WAIT_TIMEOUT=3600
RANCHER_API_VERSION=v1
```
