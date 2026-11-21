#!/bin/sh
set -e

echo "Waiting for Docker daemon to be ready..."
n=0
until [ "$n" -ge 60 ]
do
   docker info > /dev/null 2>&1 && break
   n=$((n+1))
   echo "Waiting for Docker daemon... attempt $n"
   sleep 5
done

if ! docker info > /dev/null 2>&1; then
    echo "Docker daemon failed to start after 60 attempts"
    exit 1
fi

echo "Docker daemon is ready"
