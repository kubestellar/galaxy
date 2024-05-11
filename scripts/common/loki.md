# Hacking with Loki

## Installing loki

On the hub:

```shell
helm upgrade --install loki grafana/loki-stack \
  --create-namespace \
  --namespace loki \
  --set promtail.enabled=false \
  --set grafana.enabled=true \
  --set loki.persistence.enabled=true \
  --set loki.persistence.size=10Gi
```

add Loki nodeport service

```yaml
kubectl apply -f - <<EOF
apiVersion: v1
kind: Service
metadata:
  annotations:
    app: loki
  name: loki-nodeport
  namespace: loki
spec:
  ports:
  - name: http-metrics
    port: 3100
    protocol: TCP
    targetPort: http-metrics
  selector:
    app: loki
    release: loki
  sessionAffinity: None
  type: NodePort
EOF  
```  

On the WECs:

```shell
helm --kube-context ${cluster} upgrade --install promtail grafana/promtail --set "config.clients[0].url=http://kubeflex-control-plane:${nodePort}/loki/api/v1/push" 
```

## How to connect with loki

To connect to API

```shell
kubectl port-forward svc/loki 3100:3100
```

```shell
podName=<some pod name>
namespace=kubeflow
curl -G -s "http://localhost:3100/loki/api/v1/query_range" \
    --data-urlencode 'query={pod="${podName}",namespace="${namespace}"}' 
```    


To connect with UI (if enabled)


```
kubectl port-forward --namespace loki service/loki-grafana 3000:80
```

Get the admin secret with:

```
kubectl get secret --namespace loki loki-grafana -o jsonpath="{.data.admin-password}" | base64 --decode ; echo
```

Example of queries

```shell
pod=<pod-name>
namespace=<namespace>
node=<node-name>
current_time=$(date +%s)
end_time=$((current_time * 1000000000))
start_time=$(($end_time - 3600 * 1000000000))
curl -G -s "http://localhost:3100/loki/api/v1/query_range" \
  --data-urlencode "query={pod=\"${pod}\",namespace=\"${namespace}\",
  node_name=\"${node}\"}" \
  --data-urlencode "start=$start_time" \
  --data-urlencode "end=$end_time" \
  --data-urlencode "limit=5000"
```  

Live log streaming:

```shell
pod=<pod-name>
namespace=<namespace>
node=<node-name>
websocat "ws://localhost:3100/loki/api/v1/tail?query={pod=\"${pod}\",namespace=\"${namespace}\",node_name=\"${node}\"}"
```  