from kfp.client import Client
import argparse
import time
import warnings

with warnings.catch_warnings():
    warnings.filterwarnings("ignore")
    client = Client(host='http://kfp.localtest.me:9080')

def create_runs(number_of_runs):
    # Initialization and run creation logic here.
    print(f"Creating {number_of_runs} runs...")

    # create the runs
    for i in range(number_of_runs):
        print(f"Creating run {i}.")
        client.create_run_from_pipeline_package(
            'data-passing-python.py.yaml',
            arguments={
                'message': 'hello world',
            },
            enable_caching=False,
        )
        time.sleep(60)


def main():
    parser = argparse.ArgumentParser(description='Create a number of pipeline runs.')
    parser.add_argument('runs', type=int, help='The number of runs to create')

    args = parser.parse_args()

    create_runs(args.runs)

if __name__ == '__main__':
    main()
