#!/usr/bin/env bash

tag=1.0

export TAG=${tag}

envsubst < k8/deployment.yml | kubectl apply  -f -