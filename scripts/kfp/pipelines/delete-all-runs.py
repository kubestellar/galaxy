from kfp.client import Client
import warnings

with warnings.catch_warnings():
    warnings.filterwarnings("ignore")
    client = Client(host='https://kfp.localtest.me:9443',verify_ssl=False)

    # List all runs
    response = client.list_runs(page_size=100)

    # Delete all runs
    if hasattr(response, 'runs'):
        for run in response.runs:
            print(run.run_id)
            print(f"Deleting run: {run.run_id}")
            client.delete_run(run_id=run.run_id)

    print("All runs have been deleted.")
