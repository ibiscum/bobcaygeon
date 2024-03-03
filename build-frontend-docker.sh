#!/bin/bash

echo "Building web frontend"
docker run --rm -v "$PWD":/usr/gopath/src/github.com/ibiscum/bobcaygeon -w /usr/gopath/src/github.com/ibiscum/bobcaygeon node:10 sh -c 'cd cmd/frontend/webui && npm install && npm run build'
echo "Building go binaries"
docker run --rm -v "$PWD":/home/ibiscum/bobcaygeon -w /home/ibiscum/bobcaygeon golang:1.12-stretch ./build-frontend-binary.sh
