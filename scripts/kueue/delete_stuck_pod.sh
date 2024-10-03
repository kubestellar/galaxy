#!/usr/bin/env bash

function delete_pod () {
    echo "Deleting pod $1"
    kubectl get pods $1 -n $2  -o json > tmp.json
    sed -i 's/"kubernetes"//g' tmp.json
    kubectl replace --raw "/api/v1/namespaces/$1/finalize" -f ./tmp.json
    rm ./tmp.json
}

TERMINATING_POD=$(kubectl get pods -n $2 | awk '$2=="Terminating" {print $1}')

for pod in $TERMINATING_POD
do
    delete_pod $pod $2
done
