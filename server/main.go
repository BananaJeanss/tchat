package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"reflect"
	"sync"
	"time"
	"unsafe"
)

type ClientInfo struct {
	Conn     net.Conn
	Username string
	IP       string
}

// Change clients to store ClientInfo
var clients sync.Map // key: net.Conn, value: *ClientInfo
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

	clientIP := conn.RemoteAddr().String()
	clientInfo := &ClientInfo{
		Conn:     conn,
		Username: "", // Will be set after handshake
		IP:       clientIP,
	}
	clients.Store(conn, clientInfo)

	buffer := make([]byte, 1024)
	for {
		n, err := conn.Read(buffer)
		if err != nil {
			// handle general disconnects
			if val, ok := clients.Load(conn); ok {
				// get username from ClientInfo
				client := val.(*ClientInfo)
				fmt.Printf("Client disconnected: %s (%s)\n", client.Username, conn.RemoteAddr())
				// broadcast that user has left
				broadcastMessage(map[string]string{
					"type":    "message",
					"user":    "server",
					"message": fmt.Sprintf("%s has left the chat", client.Username),
				})
			} else {
				fmt.Println("Client disconnected:", conn.RemoteAddr())
			}
			clients.Delete(conn)
			return
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

		// handshake process on new connection
		if jsonMsg["type"] == "handshake" {
			if jsonMsg["message"] != "OK" {
				fmt.Println("Invalid handshake message:", jsonMsg["message"])
				conn.Write([]byte("Invalid handshake message"))
				continue
			}

			// check if empty username
			if jsonMsg["user"] == "" {
				fmt.Println("Empty username received, closing connection")
				conn.Write([]byte("Empty username received"))
				conn.Close()
				return
			}

			// disallow "server" as username
			if jsonMsg["user"] == "server" {
				fmt.Println("Username 'server' is reserved, closing connection")
				conn.Write([]byte("Username 'server' is reserved"))
				clients.Delete(conn)
				conn.Close()
				return
			}

			// check if the username is already in use
			var usernameInUse bool
			clients.Range(func(key, value interface{}) bool {
				client := value.(*ClientInfo)
				if client.Username == jsonMsg["user"] {
					usernameInUse = true
					return false // stop iteration
				}
				return true // not in use, continue
			})

			// check if username is between 3-20 characters
			if len(jsonMsg["user"]) < 3 || len(jsonMsg["user"]) > 20 {
				fmt.Println("Username must be between 3 and 20 characters:", jsonMsg["user"])
				conn.Write([]byte("Username must be between 3 and 20 characters"))
				clients.Delete(conn)
				conn.Close()
				return
			}

			if usernameInUse {
				fmt.Println("Username already in use:", jsonMsg["user"])
				conn.Write([]byte(`{
					"type": "alreadyInUse",
					"user": "server",
					"message": "Username already in use"
				}`))
				clients.Delete(conn)
				conn.Close()
				return
			}

			// Set username after handshake
			clientInfo.Username = jsonMsg["user"]

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
			clients.Range(func(key, value interface{}) bool {
				clientCount++
				return true
			})
			time.Sleep(100 * time.Millisecond)
			serverDmUser(fmt.Sprintf("Welcome to %s, there are %d users online", serverName, clientCount), jsonMsg["user"])
			continue
		} else if jsonMsg["type"] == "message" { // when a user sends a message
			// check if message is not empty
			if jsonMsg["message"] == "" {
				continue
			}

			// check if message exceeds character limit, if so, trim
			charLimit := int(serverConfig["messageCharLimit"].(float64))
			if len(jsonMsg["message"]) > charLimit {
				jsonMsg["message"] = jsonMsg["message"][:charLimit]
				// message should already be displayed clientside
			}

			fmt.Printf("Received message from %s: %s\n", jsonMsg["user"], jsonMsg["message"])

			if config, ok := serverConfig["logMessages"].(bool); ok && config {
				// log messages to a file
				logFile, err := os.OpenFile("chat.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
				if err != nil {
					log.Println("Error opening log file:", err)
				} else {
					defer logFile.Close()
					logMessage := fmt.Sprintf("%s [%s]: %s\n", time.Now().Format("2006-01-02 15:04:05"), jsonMsg["user"], jsonMsg["message"])
					if _, err := logFile.WriteString(logMessage); err != nil {
						log.Println("Error writing to log file:", err)
					}
				}
			}

			broadcastMessage(jsonMsg)

		} else if jsonMsg["type"] == "ping" {
			// handle ping message
			fmt.Println("Received ping from:", jsonMsg["user"])
			// send a pong response
			pongMsg := map[string]string{
				"type":    "pong",
			}
			jsonData, err := json.Marshal(pongMsg)
			if err != nil {
				log.Println("Error marshaling pong message:", err)
				continue
			}
			_, err = conn.Write(jsonData)
			if err != nil {
				log.Println("Error sending pong message:", err)
				continue
			}
			fmt.Println("Sent pong response to client:", jsonMsg["user"])
		} else {
			fmt.Println("Received non-message type:", jsonMsg["type"])
		}
	}
}

func serverDmUser(message string, user string) {
	// send a direct message to a user
	clients.Range(func(key, value interface{}) bool {
		client := value.(*ClientInfo)
		if client.Username == user {
			jsonMsg := map[string]string{
				"type":    "message",
				"user":    "server",
				"message": message,
			}
			jsonData, err := json.Marshal(jsonMsg)
			if err != nil {
				log.Println("Error marshaling DM message:", err)
				return false
			}
			_, err = client.Conn.Write(jsonData)
			if err != nil {
				log.Println("Error sending DM to user:", err)
				return false
			}
			return false // stop iteration after sending DM
		}
		return true // continue iterating
	})
}

func validateAnsi(color string) string {
	if ansiColor, ok := ansiColors[color]; ok {
		return ansiColor
	}
	fmt.Println("Invalid ANSI color:", color)
	return ansiColors["blue"] // return blue color if invalid
}

func broadcastMessage(message map[string]string) {
	clients.Range(func(key, value interface{}) bool {
		conn := key.(net.Conn)
		jsonMsg, err := json.Marshal(message)
		if err != nil {
			log.Println("Error marshaling JSON:", err)
			return false
		}

		// validate that the "color" field is a valid ANSI color
		if color, ok := message["color"]; ok {
			jsonMsg = []byte(fmt.Sprintf(`{"user": "%s", "message": "%s", "type": "%s", "color": "%s"}`, message["user"], message["message"], message["type"], validateAnsi(color)))
		} else {
			jsonMsg = []byte(fmt.Sprintf(`{"user": "%s", "message": "%s", "type": "%s", "color": "%s"}`, message["user"], message["message"], message["type"], ansiColors["reset"]))
		}

		jsonMsg, err = json.Marshal(message)
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
		"user":       "server",
		"message":    "HandshakeStart",
		"type":       "handshake",
		"serverName": serverConfig["serverName"].(string),
		"messageCharLimit":  fmt.Sprintf("%d", int(serverConfig["messageCharLimit"].(float64))),
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

func configValidate(config map[string]interface{}) (string, bool) {
	// validate the config map
	configValidateResponse := ""
	isConfigOk := true
	
	// port check
	if port, ok := config["port"].(float64); ok {
		if port < 1 || port > 65535 {
			configValidateResponse += "port must be between 1 and 65535\n"
			isConfigOk = false
		}
	}

	// serverName check
	if serverName, ok := config["serverName"].(string); ok {
		if len(serverName) > 25 {
			configValidateResponse += "serverName must be under 25 chars\n"
			isConfigOk = false
		}
	} else {
		configValidateResponse += "serverName must be a valid string\n"
		isConfigOk = false
	}

	// messageCharLimit check
	if messageCharLimit, ok := config["messageCharLimit"].(float64); ok {
		if messageCharLimit < 1 || messageCharLimit > 1000 {
			configValidateResponse += "messageCharLimit must be between 1 and 1000\n"
			isConfigOk = false
		}
	} else {
		configValidateResponse += "messageCharLimit must be a valid number\n"
		isConfigOk = false
	}

	// logMessages check
	if _, ok := config["logMessages"].(bool); !ok {
		configValidateResponse += "logMessages must be a boolean value\n"
		isConfigOk = false
	}

	// passwordProtected check
	if passwordProtected, ok := config["passwordProtected"].(bool); ok {
		if passwordProtected {
			// if passwordProtected is true, serverPassword must be a non-empty string
			if serverPassword, ok := config["serverPassword"].(string); ok {
				if serverPassword == "" {
					configValidateResponse += "serverPassword must not be empty when passwordProtected is true\n"
					isConfigOk = false
				}
			} else {
				configValidateResponse += "serverPassword must be a valid string when passwordProtected is true\n"
				isConfigOk = false
			}
		}
	} else {
		configValidateResponse += "passwordProtected must be a boolean value\n"
		isConfigOk = false
	}

	return configValidateResponse, isConfigOk
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
				"messageCharLimit":  180.0, // character limit for messages
				"logMessages": false, // whether to log messages to a file
				"passwordProtected": false, // whether the server is password protected
				"serverPassword": "", // server password, if empty, passwordProtected will be set to false
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

// server startup
func main() {

	// load up server config
	serverConfig = loadConfig()

	configMsg, configOk := configValidate(serverConfig)
	if !configOk {
		fmt.Println("Invalid server configuration:")
		fmt.Println(configMsg)
		fmt.Println("Please fix the configuration and try again.")
		return
	}

	// set process name
	SetProcessName(serverConfig["serverName"].(string))

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
