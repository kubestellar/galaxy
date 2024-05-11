#!/bin/bash

set -x # echo so that users can understand what is happening
set -e # exit on error

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &> /dev/null && pwd)

${SCRIPT_DIR}/../common/install-ks.sh
${SCRIPT_DIR}/../common/install-loki.sh
${SCRIPT_DIR}/install-kfp.sh
${SCRIPT_DIR}/../common/install-mcs.sh

echo "you can access the KFP console at http://kfp.localtest.me:9080"
echo "you can access the minio console at http://minio.localtest.me:9080 minio/minio123"