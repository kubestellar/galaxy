/*
Copyright 2024 The KubeStellar Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"time"
	"crypto/tls"
)

const (
	defaultTimeInterval         = "10s"
	defaultInitialTimeRange     = "24h"
	limit                       = "4000"
	defaultContainer            = "main"
	defaultDevLokiBaseURL       = "http://loki.loki:3100"
	defaultOpenshiftLokiBaseURL = "https://logging-loki-querier-http.openshift-logging.svc.cluster.local:3100"
	defaultLogType              = "application"
)

type LogData struct {
	Status string `json:"status"`
	Data   Data   `json:"data"`
}

type Data struct {
	ResultType string   `json:"resultType"`
	Result     []Result `json:"result"`
}

type Result struct {
	Stream Stream     `json:"stream"`
	Values [][]string `json:"values"`
}

type Stream struct {
	Stream    string `json:"stream"`
	App       string `json:"app"`
	Container string `json:"container"`
	Filename  string `json:"filename"`
	Job       string `json:"job"`
	Namespace string `json:"namespace"`
	NodeName  string `json:"node_name"`
	Pod       string `json:"pod"`
}

type Query struct {
	URL             string
	Namespace       string
	Pod             string
	NodeName        string
	Container       string
	Limit           string
	LokiInstallType string
	LogType         string
	TlsCertFile     string
	TlsKeyFile      string
}

func (q *Query) Run(start string) (string, error) {
	params := url.Values{}
	params.Set("start", start)

	if q.LokiInstallType == "openshift" {
		params.Set("query", fmt.Sprintf(`{kubernetes_pod_name="%s", kubernetes_namespace_name="%s", log_type="%s"} | json | hostname="%s" | kubernetes_container_name="%s"`,
                q.Pod, q.Namespace, q.LogType, q.NodeName, q.Container))
	} else {
		params.Set("query", fmt.Sprintf(`{pod="%s",namespace="%s",container="%s",node_name="%s"}`,
		q.Pod, q.Namespace, q.Container, q.NodeName))
	}
	params.Set("limit", limit)

	queryUrl := fmt.Sprintf("%s/loki/api/v1/query_range?%s", q.URL, params.Encode())

	req, err := http.NewRequest(http.MethodGet, queryUrl, nil)
        if err != nil {
                 return "", fmt.Errorf("client: could not create request: %v", err)
        }
	client := &http.Client{}

	if q.LokiInstallType == "openshift" { 
	        req.Header.Set("X-Scope-OrgID", q.LogType)
		clientTLSCert, err := tls.LoadX509KeyPair(q.TlsCertFile, q.TlsKeyFile)
        	if err != nil {
                	return "", fmt.Errorf("Error loading certificate and key file: %v", err)
        	}

        	tlsConfig := &tls.Config{
                	InsecureSkipVerify: true,
                	Certificates: []tls.Certificate{clientTLSCert},
        	}

        	tr := &http.Transport{
                	TLSClientConfig: tlsConfig,
        	}

        	client = &http.Client{Transport: tr}
	}

        resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("error querying Loki: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error reading response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("non-OK HTTP status code: %d", resp.StatusCode)
	}

	return string(body), nil
}

func main() {
	loki_install_type := os.Getenv("LOKI_INSTALL_TYPE")
	if loki_install_type == "" {
		log.Fatal("LOKI_INSTALL_TYPE env variable is not set.")
	} else if loki_install_type != "openshift" && loki_install_type != "dev" {
		log.Fatal("LOKI_INSTALL_TYPE has to be either `openshift` or `dev`.")
	}

	// When LOKI_INSTALL_TYPE is "openshift", LOG_TYPE should be one of these three: application, infrastructure or audit
	// When LOKI_INSTALL_TYPE is "openshift", it is compulsory to specify TLS_CERT_FILE and TLS_KEY_FILE
	// You can get the TLS_CERT_FILE and TLS_KEY_FILE from the secret "logging-loki-querier-http" in the "openshift-logging" namespace on the openshift cluster where the loki instance has been installed
	log_type := os.Getenv("LOG_TYPE")
	tls_cert_file := os.Getenv("TLS_CERT_FILE")
	tls_key_file := os.Getenv("TLS_KEY_FILE")
	if loki_install_type == "openshift" {
		if log_type == "" {
			log.Printf("LOG_TYPE not defined, using default: %s", defaultLogType)
			log_type = defaultLogType
		}
		if tls_cert_file == "" {
			log.Fatal("TLS_CERT_FILE env variable is not set.")
		}
		if tls_key_file == "" {
                        log.Fatal("TLS_KEY_FILE env variable is not set.")
                }
	}

	namespace := os.Getenv("POD_NAMESPACE")
	if namespace == "" {
		log.Fatal("POD_NAMESPACE env variable is not set.")
	} else {
		log.Printf("POD_NAMESPACE: %s", namespace)
	}

	pod := os.Getenv("POD_NAME")
	if pod == "" {
		log.Fatal("POD_NAME env variable is not set.")
	} else {
		log.Printf("POD_NAME: %s", pod)
	}

	hostName := os.Getenv("HOST_NAME")
	if hostName == "" {
		log.Fatal("HOST_NAME env variable is not set.")
	} else {
		log.Printf("HOST_NAME: %s", hostName)
	}

	lokiBaseURL := os.Getenv("LOKI_BASE_URL")
	if lokiBaseURL == "" {
		if loki_install_type == "openshift" {
			log.Printf("LOKI_BASE_URL not defined, using default: %s", defaultOpenshiftLokiBaseURL)
			lokiBaseURL = defaultOpenshiftLokiBaseURL
		} else {
			log.Printf("LOKI_BASE_URL not defined, using default: %s", defaultDevLokiBaseURL)
                        lokiBaseURL = defaultDevLokiBaseURL
		}
	} else {
		log.Printf("LOKI_BASE_URL: %s", lokiBaseURL)
	}

	container := os.Getenv("CONTAINER")
	if container == "" {
		log.Printf("CONTAINER not defined, using default: %s", defaultContainer)
		container = defaultContainer
	} else {
		log.Printf("CONTAINER: %s", container)
	}

	initialTimeRangeStr := os.Getenv("INITIAL_TIME_RANGE")
	if initialTimeRangeStr == "" {
		log.Printf("INITIAL_TIME_RANGE not defined, using default: %s", defaultInitialTimeRange)
		initialTimeRangeStr = defaultInitialTimeRange
	} else {
		log.Printf("INITIAL_TIME_RANGE: %s", initialTimeRangeStr)
	}

	timeIntervalStr := os.Getenv("TIME_INTERVAL")
	if timeIntervalStr == "" {
		log.Printf("TIME_INTERVAL not defined, using default: %s", defaultTimeInterval)
		timeIntervalStr = defaultTimeInterval
	} else {
		log.Printf("TIME_INTERVAL: %s", timeIntervalStr)
	}

	initialTimeInterval, err := time.ParseDuration(initialTimeRangeStr)
	if err != nil {
		log.Fatal("error converting initialTimeRangeStr", err)
	}

	timeInterval, err := time.ParseDuration(timeIntervalStr)
	if err != nil {
		log.Fatal("error converting timeIntervalStr", err)
	}

	query := Query{
		URL:       lokiBaseURL,
		Namespace: namespace,
		NodeName:  hostName,
		Pod:       pod,
		Container: container,
		Limit:     limit,
		LokiInstallType: loki_install_type,
		LogType: log_type,
		TlsCertFile: tls_cert_file,
		TlsKeyFile: tls_key_file,
	}

	initialStartTime := fmt.Sprintf("%d", time.Now().Add(-initialTimeInterval).UnixNano())
	start := initialStartTime

	for {
		if start == "" {
			continue
		}

		body, err := query.Run(start)
		if err != nil {
			fmt.Println("Error:", err)
			if body != "" {
				fmt.Println("Body:", body)
			}
			return
		}

		var logData LogData

		err = json.Unmarshal([]byte(body), &logData)
		if err != nil {
			fmt.Println("JSON unmarshal error:", err)
			continue
		}

		values := [][]string{}

		for _, result := range logData.Data.Result {
			values = append(values, result.Values...)
		}

		// Sort the slice by the timestamp as string in descending order.
		sort.Slice(values, func(i, j int) bool {
			return values[i][0] < values[j][0]
		})

		// Print the sorted slice
		for _, value := range values {
			fmt.Printf("%s\n", value[1])
		}

		time.Sleep(timeInterval)

		logSize := len(values)
		if logSize >= 1 {
			// define the start for the next query just 1 ms more than the last one so that the results
			// are not overlapping with the already printed log
			start, err = incrememtTimestampString(values[logSize-1][0], 1)
			if err != nil {
				log.Printf("incrememtTimestampString: error converting timestamp: %s", err)
			}
		}
	}
}

// convertAndIncrementTimeStamp - converts the string timestamp to an int64, adds the supplied increment and
// converts back to string
func incrememtTimestampString(tsMs string, increment int64) (string, error) {
	ts, err := strconv.ParseInt(tsMs, 10, 64)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d", ts+increment), nil
}
