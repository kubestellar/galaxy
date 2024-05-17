#!/bin/bash

# Copyright 2024 The KubeStellar Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -x # echo so that users can understand what is happening
set -e # exit on error

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &> /dev/null && pwd)

${SCRIPT_DIR}/../common/install-ks.sh
${SCRIPT_DIR}/../common/install-loki.sh
${SCRIPT_DIR}/install-kfp.sh
${SCRIPT_DIR}/../common/install-mcs.sh

echo "you can access the KFP console at http://kfp.localtest.me:9080"
echo "you can access the minio console at http://minio.localtest.me:9080 minio/minio123"