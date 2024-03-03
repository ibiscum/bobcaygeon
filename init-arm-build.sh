#!/bin/bash

# hack for now
sudo chown -R $(id -u):$(id -g) go.mod
go mod vendor

docker run --rm --privileged multiarch/qemu-user-static:register --reset

docker run --rm -v "$PWD":/usr/gopath/src/github.com/ibiscum/bobcaygeon -w /usr/gopath/src/github.com/ibiscum/bobcaygeon balenalib/raspberry-pi-golang ./build-arm.sh
