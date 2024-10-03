from kfp.client import Client
import warnings

with warnings.catch_warnings():
    warnings.filterwarnings("ignore")
    client = Client(host='http://kfp.localtest.me:9080')

    # List all pipelines
    response = client.list_pipelines(page_size=100)
    if hasattr(response, 'pipelines'):
        for pipeline in response.pipelines:
            print(pipeline)
