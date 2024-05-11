import kfp
from kfp import dsl


# Define a component by creating a python function and use the `component` decorator
@dsl.component
def print_text():
    import time
    import sys
    for i in range(1000):  # This loop is just for demonstration purposes
        print(f"Hello, World! Count: {i}", file=sys.stderr, flush=True)
        time.sleep(5)

# Create a pipeline function
@dsl.pipeline(
    name='print-text-every-5-seconds-pipeline',
    description='A pipeline that prints text every 5 seconds.'
)
def print_text_pipeline():
    # Use the component within the pipeline function
    print_text()

# Compile the pipeline
if __name__ == '__main__':
    kfp.compiler.Compiler().compile(
        pipeline_func=print_text_pipeline,
        package_path='pipeline.yaml'
    )