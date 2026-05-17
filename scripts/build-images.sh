#!/usr/bin/env bash
set -euo pipefail

images=(image-base image-node image-python image-full)

for image in "${images[@]}"; do
  echo "building $image"
  nix build ".#$image"
  docker load < result
  rm -f result
  rm -f result-*
done
