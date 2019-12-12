#!/usr/bin/env bash

set -e

tag=$(openssl rand -base64 12)

docker build -t rtmp-server:${tag} .

gcr=gcr.io/engineering-sandbox-228018/rtmp-server:${tag}
gcrLatest=gcr.io/engineering-sandbox-228018/rtmp-server:latest

docker tag rtmp-server:${tag} ${gcr}
docker tag rtmp-server:${tag} ${gcrLatest}

docker push ${gcr}
docker push ${gcrLatest}

export TAG=${tag}

envsubst < k8/deployment.yml | kubectl apply --namespace ns-aylon-armstrong -f -