package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/gorilla/websocket"
)

const (
	defaultLokiBaseURL = "ws://loki.loki:3100"
)

// Define struct to match the nested JSON structure.
type StreamEntry struct {
	Stream struct {
		App        string `json:"app"`
		Container  string `json:"container"`
		Filename   string `json:"filename"`
		Job        string `json:"job"`
		Namespace  string `json:"namespace"`
		NodeName   string `json:"node_name"`
		Pod        string `json:"pod"`
		StreamType string `json:"stream"`
	} `json:"stream"`
	Values [][]string `json:"values"`
}

type LogData struct {
	Streams []StreamEntry `json:"streams"`
}

func main() {
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
		log.Printf("LOKI_BASE_URL not defined, using default: %s", defaultLokiBaseURL)
		lokiBaseURL = defaultLokiBaseURL
	} else {
		log.Printf("LOKI_BASE_URL: %s", lokiBaseURL)
	}

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

	// Construct the WebSocket URL for streaming logs from Loki
	//	url := "ws://localhost:3100/loki/api/v1/tail?query={pod=\"tutorial-data-passing-rlqf4-357838429\",namespace=\"kubeflow\"}"
	url := fmt.Sprintf("%s/loki/api/v1/tail?query={pod=\"%s\",namespace=\"%s\",node_name=\"%s\"}", lokiBaseURL, pod, namespace, hostName)

	header := http.Header{}

	c, _, err := websocket.DefaultDialer.Dial(url, header)
	if err != nil {
		log.Fatal("dial:", err)
	}
	defer c.Close()

	done := make(chan struct{})

	go func() {
		defer close(done)
		for {
			_, message, err := c.ReadMessage()
			if err != nil {
				log.Println("read:", err)
				return
			}

			var logData LogData
			err = json.Unmarshal([]byte(message), &logData)
			if err != nil {
				log.Fatalf("Failed to parse JSON: %v", err)
			}

			for _, stream := range logData.Streams {
				for _, value := range stream.Values {
					fmt.Printf("%s\n", value[1])
				}
			}

		}
	}()

	for {
		select {
		case <-done:
			return
		case <-interrupt:
			log.Println("interrupt")

			// Cleanly close the connection by sending a close message and then waiting
			// for the server to close the connection.
			err := c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			if err != nil {
				log.Println("write close:", err)
				return
			}
			select {
			case <-done:
			case <-time.After(time.Second):
			}
			return
		}
	}
}
