#!/bin/sh

# Try curl first
if curl -s -w "\n%{http_code}" "$1" >/dev/null 2>&1; then
    curl -s "$1"
    exit 0
fi

# If curl fails, try wget
if wget -qO- "$1" >/dev/null 2>&1; then
    wget -qO- "$1"
    exit 0
fi

# No client found, try to install curl
if [ -f "/etc/alpine-release" ]; then
    apk add --no-cache curl >/dev/null 2>&1 || exit 1
elif [ -f "/etc/debian_version" ]; then
    apt-get update >/dev/null 2>&1 && apt-get install -y curl >/dev/null 2>&1 || exit 1
else
    echo "No package manager found and no HTTP client available" >&2
    exit 1
fi

# Try curl again after installation
if curl -s "$1"; then
    exit 0
else
    echo "Failed to make request even after installing curl" >&2
    exit 1
fi
