package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"
	"unsafe"

	goaway "github.com/TwiN/go-away" // for profanity check
)

type ClientInfo struct {
	Conn          net.Conn    // connection to the client
	Username      string      // username of the client
	IP            string      // IP address of the client
	isApproved    bool        // whether the client has been approved after handshake (used for passwordProtected)
	MsgTimestamps []time.Time // timestamps of the last 10 messages sent by the client
}

// Change clients to store ClientInfo
var clients sync.Map // key: net.Conn, value: *ClientInfo
var serverConfig map[string]interface{}

// store 10 latest messages
var messageHistory []map[string]string
var messageHistoryMutex sync.Mutex

// ip ban table
var ipBanTable sync.Map // key: string (IP address), value: bool (banned or not)


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

			// first off, check if password matches with serverPassword if passwordProtected is enabled
			if serverConfig["passwordProtected"].(bool) {
				if jsonMsg["serverPassword"] != serverConfig["serverPassword"].(string) {
					fmt.Println("Invalid password received, closing connection")
					errMsg := map[string]string{
						"type":    "invalidPassword",
						"user":    "server",
						"message": "Invalid password",
					}
					jsonData, err := json.Marshal(errMsg)
					if err != nil {
						fmt.Println("Error marshaling error message:", err)
						return
					}
					_, err = conn.Write(jsonData)
					if err != nil {
						fmt.Println("Error sending error message:", err)
						return
					}
					clients.Delete(conn)
					conn.Close()
					return
				}
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

				errMsg := map[string]string{
					"type":    "alreadyInUse",
					"user":    "server",
					"message": "Username already in use",
				}

				jsonData, err := json.Marshal(errMsg)
				if err != nil {
					fmt.Println("Error marshaling error message:", err)
					return
				}
				_, err = conn.Write(jsonData)
				if err != nil {
					fmt.Println("Error sending error message:", err)
					return
				}

				clients.Delete(conn)
				conn.Close()
				return
			}

			// atp the client checks out, approve the client
			clientInfo.isApproved = true
			fmt.Println("Client approved:", jsonMsg["user"])
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

			// send message history here if enabled
			if serverConfig["sendMessageHistory"].(bool) {
				sendMessageHistory(conn)
			}

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
			// check if client is approved
			if val, ok := clients.Load(conn); ok {
				client := val.(*ClientInfo)
				if !client.isApproved {
					fmt.Println("Client not approved, ignoring message from:", client.Username)
					continue
				}
				// check if ratelimited
				if isRateLimited(client) {
					fmt.Printf("Rate limit exceeded for user: %s\n", client.Username)
					warnMsg := map[string]string{
						"type":    "message",
						"user":    "server",
						"message": "You are sending messages too fast, please wait a bit.",
					}
					jsonData, _ := json.Marshal(warnMsg)
					conn.Write(jsonData)
					continue
				}
			} else {
				continue
			}

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

			// profanity check if enabled in config
			if serverConfig["profanityCheck"].(bool) {
				if goaway.IsProfane(jsonMsg["message"]) {
					jsonMsg["message"] = goaway.Censor(jsonMsg["message"])
				}
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
				"type": "pong",
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



func isRateLimited(client *ClientInfo) bool {
	const rateLimitWindow = 5 * time.Second
	const rateLimitCount = 10 // max of 10 messages in 5 seconds

	now := time.Now()
	cutoff := now.Add(-rateLimitWindow)

	// remove old timestamps outside window
	i := 0
	for ; i < len(client.MsgTimestamps); i++ {
		if client.MsgTimestamps[i].After(cutoff) {
			break
		}
	}
	client.MsgTimestamps = client.MsgTimestamps[i:]

	if len(client.MsgTimestamps) >= rateLimitCount {
		return true
	}

	client.MsgTimestamps = append(client.MsgTimestamps, now)
	return false
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

func sendMessageHistory(conn net.Conn) {
	messageHistoryMutex.Lock()
	defer messageHistoryMutex.Unlock()

	if len(messageHistory) == 0 {
		return // no messages to send
	}

	for _, msg := range messageHistory {
		jsonData, err := json.Marshal(msg)
		if err != nil {
			log.Println("Error marshaling message history:", err)
			continue
		}
		_, err = conn.Write(jsonData)
		if err != nil {
			log.Println("Error sending message history to client:", err)
			break
		}
		// small delay to avoid messing up the client
		time.Sleep(10 * time.Millisecond)
	}
}

func broadcastMessage(message map[string]string) {
	// store message in history
	if message["type"] == "message" {
		if config, ok := serverConfig["sendMessageHistory"].(bool); ok && config {
			messageHistoryMutex.Lock()
			messageHistory = append(messageHistory, message)
			if len(messageHistory) > 10 {
				messageHistory = messageHistory[1:] // keep only the latest 10
			}
			messageHistoryMutex.Unlock()
		}
	}

	clients.Range(func(key, value interface{}) bool {
		clientInfo := value.(*ClientInfo)
		if !clientInfo.isApproved {
			return true // skip unapproved clients
		}
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
		"user":              "server",
		"message":           "HandshakeStart",
		"type":              "handshake",
		"serverName":        serverConfig["serverName"].(string),
		"messageCharLimit":  fmt.Sprintf("%d", int(serverConfig["messageCharLimit"].(float64))),
		"passwordProtected": fmt.Sprintf("%t", serverConfig["passwordProtected"].(bool)),
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

// validates the server configuration
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
				"port":               9076.0, // make sure its float64
				"serverName":         "an tchat server",
				"messageCharLimit":   180.0, // character limit for messages
				"logMessages":        false, // whether to log messages to a file
				"passwordProtected":  false, // whether the server is password protected
				"serverPassword":     "",    // server password, if empty, passwordProtected will be set to false
				"sendMessageHistory": true,  // whether to send message history to new clients
				"profanityCheck":     true,  // whether to enable profanity check
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

// ...existing code...
func handleServerCommand(cmdLine string) {
    args := strings.Fields(cmdLine)
    if len(args) == 0 {
        return
    }
    switch args[0] {
    case "//clearchat":
        messageHistoryMutex.Lock()
        messageHistory = nil
        messageHistoryMutex.Unlock()
        broadcastMessage(map[string]string{
            "type":    "clearChat",
            "user":    "server",
            "message": "Chat history has been cleared by the server.",
        })
        fmt.Println("Chat cleared.")
    case "//kick":
        if len(args) < 2 {
            fmt.Println("Usage: //kick <username>")
            return
        }
        username := args[1]
        kicked := false
        clients.Range(func(key, value interface{}) bool {
            c := value.(*ClientInfo)
            if c.Username == username {
                c.Conn.Close()
                kicked = true
                return false
            }
            return true
        })
        if kicked {
            broadcastMessage(map[string]string{
                "type":    "message",
                "user":    "server",
                "message": fmt.Sprintf("%s has been kicked by the server.", username),
            })
            fmt.Printf("User %s kicked.\n", username)
        } else {
            fmt.Println("User not found.")
        }
    case "//broadcast":
		if len(args) < 2 {
			fmt.Println("Usage: /broadcast <message>")
			return
		}
		message := strings.Join(args[1:], " ")
		broadcastMessage(map[string]string{
			"type":    "message",
			"user":    "server",
			"message": message,
		})
	case "//ban": // TODO: write bans to a json file for persistence
		if len(args) < 2 {
			fmt.Println("Usage: //ban <user>")
			return
		}
		username := args[1]
		banned := false
		clients.Range(func(key, value interface{}) bool {
			c := value.(*ClientInfo)
			if c.Username == username {
				// add to ipBanTable
				ip := c.IP
				if _, exists := ipBanTable.Load(ip); !exists {
					ipBanTable.Store(ip, true)
					banned = true
					c.Conn.Close()
					clients.Delete(c.Conn)
					broadcastMessage(map[string]string{
						"type":    "message",
						"user":    "server",
						"message": fmt.Sprintf("%s has been banned from the server.", username),
					})
					fmt.Printf("User %s banned.\n", username)
				}
				return false
			}
			return true
		})
		if !banned {
			fmt.Println("User not found.")
		}
	default:
		fmt.Println("Unknown command:", args[0])
	}
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

	// goroutine for handling serverside commands
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for {
			fmt.Print("server> ")
			if !scanner.Scan() {
				break
			}
			cmdLine := scanner.Text()
			if cmdLine == "" {
				continue
			}
			handleServerCommand(cmdLine)
		}
	}()

	// accept connections in a loop
	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Println("Error accepting connection:", err)
			continue
		}

		// check if the IP is banned first
		// i have no idea if this actually works but trust the process 
		ip := conn.RemoteAddr().(*net.TCPAddr).IP.String()
		if _, banned := ipBanTable.Load(ip); banned {
			fmt.Println("Connection from banned IP:", ip)
			conn.Close()
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
