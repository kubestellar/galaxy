# Copyright 2021 The Kubeflow Authors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
"""Hello world v2 engine pipeline."""

from kfp import dsl
from kfp import compiler

@dsl.component
def say_hello(name: str) -> str:
    hello_text = f'Hello, {name}!'
    return hello_text

@dsl.pipeline(
    name='My pipeline',
    description='My machine learning pipeline',
)
def hello_pipeline(recipient: str) -> str:
    hello_task = say_hello(name=recipient)
    return hello_task.output

compiler.Compiler().compile(hello_pipeline, 'pipeline.yaml')
