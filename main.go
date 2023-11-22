package main

import (
	gs "centralReg/grpc_status"
	pb "centralReg/service_reg"
	"context"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"google.golang.org/grpc"
	"log"
	"net/http"
	"sync"
	"time"
)

type ApiService = pb.APIService
type Registration = pb.Registration

var registry = struct {
	sync.RWMutex
	services map[string]*ApiService
}{services: make(map[string]*ApiService)}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

func main() {
	go monitorServices()
	http.HandleFunc("/register", registerService)
	http.HandleFunc("/services", listServices)
	fmt.Println("Service Registry is running on port 8090")
	http.ListenAndServe(":8090", nil)
}

func monitorServices() {
	for {
		registry.RLock()
		for _, service := range registry.services {
			//go checkServiceStatus(service)
			if service.Type == "gRPC" {
				go checkGRPCServiceStatus(service)
			} else {
				go checkRESTServiceStatus(service)
			}
		}
		registry.RUnlock()
		time.Sleep(10 * time.Second) // Check every 10 seconds
	}
}

func checkRESTServiceStatus(service *ApiService) {
	url := fmt.Sprintf("http://%s:%d/status", service.Host, service.Port)
	resp, err := http.Get(url)
	if err != nil || resp.StatusCode != http.StatusOK {
		service.Status = "Down"
	} else {
		service.Status = "Up"
	}
}

func checkGRPCServiceStatus(service *ApiService) {
	// Set up a connection to the server with insecure credentials for simplicity
	conn, err := grpc.Dial(fmt.Sprintf("%s:%d", service.Host, service.Port), grpc.WithInsecure(), grpc.WithBlock())
	if err != nil {
		log.Printf("Failed to dial gRPC service: %v", err)
		service.Status = "Down"
		return
	}
	defer conn.Close()

	// Create a new StatusService client
	client := gs.NewStatusServiceClient(conn)

	// Prepare a context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	// Call the CheckStatus method
	response, err := client.CheckStatus(ctx, &gs.StatusRequest{})
	if err != nil {
		log.Printf("Error calling CheckStatus: %v", err)
		service.Status = "Down"
	} else {
		service.Status = response.Status
		//service.Status = "Up"
		log.Printf("Service %s status checked: %s", service.Name, service.Status)
	}
}

func registerService(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Upgrade to websocket failed:", err)
		return
	}
	defer conn.Close()

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			log.Println("Error reading message:", err)
			break
		}

		var reg Registration
		if err := json.Unmarshal(msg, &reg); err != nil {
			log.Printf("Error decoding registration data: %s", err)
			conn.WriteMessage(websocket.TextMessage, []byte("Invalid registration data"))
			continue
		}

		registry.Lock()
		registry.services[reg.Name] = &ApiService{
			Name: reg.Name,
			Host: reg.Host,
			Port: reg.Port,
			Type: reg.Type, // This should be either "REST" or "gRPC"
		}
		registry.Unlock()

		log.Printf("Service %s registered successfully. Type: %s", reg.Name, reg.Type)
		conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("Service %s registered successfully", reg.Name)))
	}
}

func listServices(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Upgrade to websocket failed:", err)
		return
	}
	defer conn.Close()

	registry.RLock()
	defer registry.RUnlock()

	services := make([]*ApiService, 0, len(registry.services))

	for _, service := range registry.services {
		services = append(services, service)
	}

	// Send the service list over WebSocket
	err = conn.WriteJSON(services)
	if err != nil {
		log.Println("Error sending services over WebSocket:", err)
		return
	}

	log.Println("Sent services list over WebSocket")
}
