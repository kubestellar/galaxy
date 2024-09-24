from kfp.client import Client

client = Client(host='http://kfp.localtest.me:9080')
run = client.create_run_from_pipeline_package(
    'pipeline.yaml',
)
