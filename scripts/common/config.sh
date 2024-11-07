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

# Common configuration values for all scripts

# wec used in the setup
clusters=(cluster1 cluster2);
core=kind-kubeflex

# charts versions
CLUSTER_METRICS_CHART_VERSION=0.0.1-alpha.8
MC_SCHEDULING_CHART_VERSION=0.0.1-alpha.8
SHADOW_PODS_VERSION=0.0.1-alpha.8
SUSPEND_WEBHOOK_VERSION=0.0.1-alpha.8
KUEUE_KS_VERSION=0.0.1-alpha.8

# Kubeflow Pipeline Version
PIPELINE_VERSION=2.2.0