#!/usr/bin/env python3
# Copyright 2020-2023 The Kubeflow Authors
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

# %% [markdown]
# # Data passing tutorial
# Data passing is the most important aspect of Pipelines.
#
# In Kubeflow Pipelines, the pipeline authors compose pipelines by creating component instances (tasks) and connecting them together.
#
# Components have inputs and outputs. They can consume and produce arbitrary data.
#
# Pipeline authors establish connections between component tasks by connecting their data inputs and outputs - by passing the output of one task as an argument to another task's input.
#
# The system takes care of storing the data produced by components and later passing that data to other components for consumption as instructed by the pipeline.
#
# This tutorial shows how to create python components that produce, consume and transform data.
# It shows how to create data passing pipelines by instantiating components and connecting them together.

# %%

from typing import Dict, List

from kfp import compiler
from kfp import dsl
from kfp.dsl import Input, InputPath, Output, OutputPath, Dataset, Model, component


@component(base_image="python:3.10")
def preprocess(
    # An input parameter of type string.
    message: str,
    # Use Output[T] to get a metadata-rich handle to the output artifact
    # of type `Dataset`.
    output_dataset_one: Output[Dataset],
    # A locally accessible filepath for another output artifact of type
    # `Dataset`.
    output_dataset_two_path: OutputPath('Dataset'),
    # A locally accessible filepath for an output parameter of type string.
    output_parameter_path: OutputPath(str),
    # A locally accessible filepath for an output parameter of type bool.
    output_bool_parameter_path: OutputPath(bool),
    # A locally accessible filepath for an output parameter of type dict.
    output_dict_parameter_path: OutputPath(Dict[str, int]),
    # A locally accessible filepath for an output parameter of type list.
    output_list_parameter_path: OutputPath(List[str]),
):
    """Dummy preprocessing step."""

    # Use Dataset.path to access a local file path for writing.
    # One can also use Dataset.uri to access the actual URI file path.
    with open(output_dataset_one.path, 'w') as f:
        f.write(message)

    # OutputPath is used to just pass the local file path of the output artifact
    # to the function.
    with open(output_dataset_two_path, 'w') as f:
        f.write(message)

    with open(output_parameter_path, 'w') as f:
        f.write(message)

    with open(output_bool_parameter_path, 'w') as f:
        f.write(
            str(True))  # use either `str()` or `json.dumps()` for bool values.

    import json
    with open(output_dict_parameter_path, 'w') as f:
        f.write(json.dumps({'A': 1, 'B': 2}))

    with open(output_list_parameter_path, 'w') as f:
        f.write(json.dumps(['a', 'b', 'c']))


@component(base_image="python:3.10")
def train(
    # Use InputPath to get a locally accessible path for the input artifact
    # of type `Dataset`.
    dataset_one_path: InputPath('Dataset'),
    # Use Input[T] to get a metadata-rich handle to the input artifact
    # of type `Dataset`.
    dataset_two: Input[Dataset],
    # An input parameter of type string.
    message: str,
    # Use Output[T] to get a metadata-rich handle to the output artifact
    # of type `Model`.
    model: Output[Model],
    # An input parameter of type bool.
    input_bool: bool,
    # An input parameter of type dict.
    input_dict: Dict[str, int],
    # An input parameter of type List[str].
    input_list: List[str],
    # An input parameter of type int with a default value.
    num_steps: int = 200,
):
    import time
    import sys
    """Dummy Training step."""
    with open(dataset_one_path, 'r') as input_file:
        dataset_one_contents = input_file.read()

    with open(dataset_two.path, 'r') as input_file:
        dataset_two_contents = input_file.read()

    line = (f'dataset_one_contents: {dataset_one_contents} || '
            f'dataset_two_contents: {dataset_two_contents} || '
            f'message: {message} || '
            f'input_bool: {input_bool}, type {type(input_bool)} || '
            f'input_dict: {input_dict}, type {type(input_dict)} || '
            f'input_list: {input_list}, type {type(input_list)} \n')

    with open(model.path, 'w') as output_file:
        for i in range(num_steps):
            print(f"Writing step {i} to file", file=sys.stderr, flush=True)
            output_file.write('Step {}\n{}\n=====\n'.format(i, line))
            time.sleep(3)

    # model is an instance of Model artifact, which has a .metadata dictionary
    # to store arbitrary metadata for the output artifact.
    model.metadata['accuracy'] = 0.9

@dsl.pipeline(pipeline_root='', name='tutorial-data-passing')
def data_passing_pipeline(message: str = 'message'):
    preprocess_task = preprocess(message=message)
    train_task = train(
        dataset_one_path=preprocess_task.outputs['output_dataset_one'],
        dataset_two=preprocess_task.outputs['output_dataset_two_path'],
        message=preprocess_task.outputs['output_parameter_path'],
        input_bool=preprocess_task.outputs['output_bool_parameter_path'],
        input_dict=preprocess_task.outputs['output_dict_parameter_path'],
        input_list=preprocess_task.outputs['output_list_parameter_path'],
    )
    train_task.set_cpu_request('1000m').set_cpu_limit('1000m')
    train_task.set_memory_request('1024Mi').set_memory_limit('1024Mi')
    # below is how to add nvidia GPU request
    # train_task.add_node_selector_constraint('nvidia.com/gpu')
    # train_task.set_gpu_limit(1)

if __name__ == '__main__':
    compiler.Compiler().compile(data_passing_pipeline, __file__ + '.yaml')
