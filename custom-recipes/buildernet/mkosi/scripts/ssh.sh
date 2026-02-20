#!/usr/bin/env bash
# SSH into the BuilderNet VM
set -eu -o pipefail

SSH_PORT=2222

ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -p ${SSH_PORT} bnet@localhost
