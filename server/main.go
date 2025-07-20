package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"reflect"
	"sync"
	"time"
	"unsafe"
)

var clients sync.Map // store connected clients
var serverConfig map[string]interface{}

var ansiColors = map[string]string{
	"reset":   "\033[0m",
	"red":     "\033[31m",
	"green":   "\033[32m",
	"yellow":  "\033[33m",
	"blue":    "\033[34m",
	"magenta": "\033[35m",
	"cyan":    "\033[36m",
	"white":   "\033[37m",
}

func handleClient(conn net.Conn, handshakeDone chan struct{}) {
	defer conn.Close()
	fmt.Println("Client connected:", conn.RemoteAddr())
	clients.Store(conn, true)

	buffer := make([]byte, 1024)
	for {
		n, err := conn.Read(buffer)
		if err != nil {
			if err == io.EOF {
				fmt.Println("Client disconnected:", conn.RemoteAddr())
				clients.Delete(conn)
				return
			} else if n == 0 {
				fmt.Println("Client disconnected?:", conn.RemoteAddr())
				clients.Delete(conn)
				return
			}
		}

		var jsonMsg map[string]string
		if !json.Valid(buffer[:n]) {
			fmt.Println("Invalid JSON received")
			continue
		}
		if err := json.Unmarshal(buffer[:n], &jsonMsg); err != nil {
			fmt.Println("Error parsing JSON:", err)
			return
		}

		if jsonMsg["type"] == "handshake" {
			if jsonMsg["message"] != "OK" {
				fmt.Println("Invalid handshake message:", jsonMsg["message"])
				conn.Write([]byte("Invalid handshake message"))
				continue
			}

			// TODO; issue a unique identifier, store with ip and user
			// to prevent multiple people/ips using the same username

			// Signal handshake completion
			select {
			case handshakeDone <- struct{}{}:
			default:
			}
			// Clear the read deadline after handshake
			conn.SetReadDeadline(time.Time{})
			fmt.Println("Handshake received from client:", jsonMsg["user"])
			broadcastMessage(map[string]string{
				"type":    "message",
				"user":    "server",
				"message": fmt.Sprintf("%s has joined the chat", jsonMsg["user"]),
			})
			var clientCount int
			serverName := serverConfig["serverName"].(string)
			clients.Range(func(key, value interface{}) bool{
				clientCount++
				return true
			})
			time.Sleep(100 * time.Millisecond)
			serverDmUser(fmt.Sprintf("Welcome to %s, there are %d users online", serverName, clientCount))
			continue
		}

		if jsonMsg["type"] == "message" {
			// check if message is not empty
			if jsonMsg["message"] == "" {
				continue
			}

			fmt.Printf("Received message from %s: %s\n", jsonMsg["user"], jsonMsg["message"])

			broadcastMessage(jsonMsg)

		} else {
			fmt.Println("Received non-message type:", jsonMsg["type"])
		}
	}
}

func serverDmUser(message string) {
	// send a direct message to a user
	clients.Range(func(key, value interface{}) bool {
		conn := key.(net.Conn)
		jsonMsg := map[string]string{
			"type":    "message",
			"user":    "server",
			"message": message,
		}
		jsonData, err := json.Marshal(jsonMsg)
		if err != nil {
			log.Println("Error marshaling JSON:", err)
			return false // stop iteration if error occurs
		}
		_, err = conn.Write(jsonData)
		if err != nil {
			log.Println("Error sending message to client:", err)
			return false // stop iteration if error occurs
		}
		return true // continue iterating
	})

}

func broadcastMessage(message map[string]string) {
	clients.Range(func(key, value interface{}) bool {
		conn := key.(net.Conn)
		jsonMsg, err := json.Marshal(message)
		if err != nil {
			log.Println("Error marshaling JSON:", err)
			return false
		}
		_, err = conn.Write(jsonMsg)
		if err != nil {
			log.Println("Error sending message to client:", err)
			return false // stop iteration if error occurs
		}
		return true // continue iterating
	})
}

func SetProcessName(name string) error {
	argv0str := (*reflect.StringHeader)(unsafe.Pointer(&os.Args[0]))
	argv0 := (*[1 << 30]byte)(unsafe.Pointer(argv0str.Data))[:argv0str.Len]

	n := copy(argv0, name)
	if n < len(argv0) {
		argv0[n] = 0
	}

	return nil
}

func sendHandshake(conn net.Conn) error {
	// Send a quick handshake message to validate the user
	// this should expect the username and a message of OK
	handshakeMsg := map[string]string{
		"user":    "server",
		"message": "HandshakeStart",
		"type":    "handshake",
	}

	jsonMsg, err := json.Marshal(handshakeMsg)
	if err != nil {
		return fmt.Errorf("error marshaling handshake message: %w", err)
	}

	// set read deadline
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err = conn.Write(jsonMsg)
	if err != nil {
		return fmt.Errorf("error sending handshake message: %w", err)
	}
	return nil
}

func loadConfig() map[string]interface{} {
	const configFile = "./config.json"
	file, err := os.Open(configFile)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("Config file '%s' not found, creating one!\n", configFile)
			// if doesnt exist, create default config file
			defaultConfig := map[string]interface{}{
				"port":       9076.0, // make sure its float64
				"serverName": "an tchat server",
			}
			file, err := os.Create(configFile)
			if err != nil {
				fmt.Printf("Error creating config file: %v\n", err)
				return nil
			}
			defer file.Close()
			encoder := json.NewEncoder(file)
			encoder.SetIndent("", "  ")
			if err := encoder.Encode(defaultConfig); err != nil {
				fmt.Printf("Error writing default config: %v\n", err)
				return nil
			}
			fmt.Println("Default config created successfully.")
			return defaultConfig
		} else {
			fmt.Printf("Error opening config file: %v\n", err)
		}
		return nil
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	var config map[string]interface{}

	if err := decoder.Decode(&config); err != nil {
		fmt.Printf("Error decoding config file: %v\n", err)
		return nil
	}
	return config
}

func main() {
	// set process name
	SetProcessName("tchat server")

	// load up server config
	serverConfig = loadConfig()

	// start up a tcp server
	port := 9076 // default port
	if p, ok := serverConfig["port"].(float64); ok {
		port = int(p)
	}
	addr := fmt.Sprintf(":%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal("Error starting server:", err)
	}
	defer listener.Close()
	fmt.Println("Chat server started on port", port)

	// accept connections in a loop
	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Println("Error accepting connection:", err)
			continue
		}

		// handshakeDone channel to signal handshake completion
		handshakeDone := make(chan struct{})

		// Send handshake
		if err := sendHandshake(conn); err != nil {
			fmt.Println("Error during handshake:", err)
			conn.Close()
			continue
		}

		// Timeout goroutine
		go func(c net.Conn, done chan struct{}) {
			select {
			case <-done:
				// handshake completed
			case <-time.After(5 * time.Second):
				fmt.Println("Handshake not completed, closing connection")
				c.Close()
			}
		}(conn, handshakeDone)

		// handle each client in a goroutine, pass handshakeDone channel
		go handleClient(conn, handshakeDone)
	}
}
