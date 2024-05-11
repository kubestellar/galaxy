#!/bin/bash

set -x # echo so that users can understand what is happening
set -e # exit on error

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &> /dev/null && pwd)
source "${SCRIPT_DIR}/setup-shell.sh"


${SCRIPT_DIR}/../common/install-ks.sh
${SCRIPT_DIR}/install-aw.sh