from kfp.client import Client

client = Client(host='https://kfp.localtest.me:9443',verify_ssl=False)
run = client.create_run_from_pipeline_package(
    'pipeline.yaml',
)
