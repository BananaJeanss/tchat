package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/term"
)

var config map[string]interface{}
var serverName string

var lastPingTimestamp time.Time

var muteList = make(map[string]bool) // list of muted users

// rate limit for messages
var (
	messageTimestamps []time.Time
	rateLimitWindow   = 5 * time.Second // 5 seconds window
	rateLimitCount    = 10              // 10 messages per 5 seconds
)

// valid ansi colors
var ansiColors = map[string]string{
	"reset":       "\033[0m",
	"red":         "\033[31m",
	"green":       "\033[32m",
	"yellow":      "\033[33m",
	"blue":        "\033[34m",
	"magenta":     "\033[35m",
	"cyan":        "\033[36m",
	"white":       "\033[37m",
	"bold_black":  "\033[1;30m",
	"bold_red":    "\033[1;31m",
	"bold_green":  "\033[1;32m",
	"bold_yellow": "\033[1;33m",
	"bold_blue":   "\033[1;34m",
	"bold_purple": "\033[1;35m",
	"bold_cyan":   "\033[1;36m",
	"bold_white":  "\033[1;37m",
}

var messageCharLimit = 180 // max characters per message

// clears the screen based on os
func clearScreen() {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "cls")
	default:
		cmd = exec.Command("clear")
	}

	cmd.Stdout = os.Stdout
	cmd.Run()
}

func getTerminalSize() (int, int) {
	width, height, _ := term.GetSize(int(syscall.Stdin))
	return width, height
}

func moveCursor(x, y int) {
	fmt.Printf("\033[%d;%dH", y, x)
}

var messages []string
var maxMessages int

// initializes the chat area based on terminal size
func initChatArea() {
	_, height := getTerminalSize()
	maxMessages = height - 4 // reserve space for header and input
}

func canSendMessage() bool {
	now := time.Now()
	cutoff := now.Add(-rateLimitWindow)

	// remove old timestamps
	i := 0
	for ; i < len(messageTimestamps); i++ {
		if messageTimestamps[i].After(cutoff) {
			break
		}
	}
	messageTimestamps = messageTimestamps[i:]

	// check rate limit
	if len(messageTimestamps) >= rateLimitCount {
		return false
	}

	// append the current timestamp when sending
	messageTimestamps = append(messageTimestamps, now)
	return true
}

// sends messages to the server
func sendMessage(conn net.Conn, user string, msg string, color string) {
	// format to json for consistency
	jsonMsg := fmt.Sprintf(`{"user": "%s", "message": "%s", "type": "message", "color": "%s"}`, user, msg, color)
	_, err := conn.Write([]byte(jsonMsg))
	if err != nil {
		fmt.Println("Error sending message:", err)
		return
	}
}

func validateAnsi(color string) string {
	if ansiColors[color] != "" {
		return ansiColors[color]
	}
	fmt.Println("Invalid color specified, using default (blue)")
	return ansiColors["blue"] // default to blue if invalid
}

func validateColorName(color string) string {
	if _, ok := ansiColors[color]; ok {
		return color
	}
	return "blue"
}

// wraps text on screen
func wrapText(text string, width int) []string {
	var lines []string
	for len(text) > width {
		lines = append(lines, text[:width])
		text = text[width:]
	}
	if len(text) > 0 {
		lines = append(lines, text)
	}
	return lines
}

// strips ansi codes from a string
func stripAnsiCodes(str string) string {
	re := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	return re.ReplaceAllString(str, "")
}

// for messages sent by other users
func addMessage(user string, msg string, color string) {
	// add @ prefix
	displayUser := user
	if user != "" && user[0] != '@' {
		displayUser = "@" + user
	}

	// validate color
	if color == "" || ansiColors[color] == "" {
		color = "blue" // default to blue if color is invalid
	}
	displayUser = validateAnsi(color) + displayUser + ansiColors["reset"] // wrap username

	// color username
	displayUser = "\033[34m" + displayUser + "\033[0m" // blue color

	width, _ := getTerminalSize()

	usernameWidth := len(stripAnsiCodes(displayUser))

	wrappedLines := wrapText(msg, width-usernameWidth) // indent for username

	// if the message is too long, wrap it
	for i, line := range wrappedLines {
		if i == 0 {
			messages = append(messages, fmt.Sprintf("%s: %s", displayUser, line))
		} else {
			messages = append(messages, fmt.Sprintf("%s  %s", strings.Repeat(" ", usernameWidth), line))
		}
	}

	if len(messages) > maxMessages {
		messages = messages[len(messages)-maxMessages:]
	}
}

// for messages sent from the server
func addServerMessage(msg string, color ...string) {
	// color the server message, color should always be bold
	if len(color) > 0 && ansiColors[color[0]] != "" {
		msg = ansiColors[color[0]] + msg + ansiColors["reset"]
	} else {
		msg = ansiColors["bold_blue"] + msg + ansiColors["reset"] // default to blue
	}
	messages = append(messages, msg)

	// keep only the messages that fit on screen
	if len(messages) > maxMessages {
		messages = messages[1:] // remove oldest message
	}
}

// redraws message area in terminal, should be called every time something changes
func redrawMessages() {
	_, height := getTerminalSize()

	// Clear the message area (not the whole screen)
	for i := 0; i < height-2; i++ {
		moveCursor(1, i)
		fmt.Print("\033[K") // Clear this line
	}

	// move cursor to the top of the message area
	moveCursor(1, 1)
	// print header
	if config == nil {
		fmt.Print("\033[1;34m--- tchat (unconfigured) ---\033[0m\n")
	} else {
		fmt.Printf("\033[1;34m--- %s on %s:%d as %s ---\033[0m\n",
			serverName,
			config["server"],
			int(config["port"].(float64)),
			config["username"])
	}

	// calculate starting line for messages
	startLine := height - 2 - len(messages)
	if startLine < 2 {
		startLine = 2
	}

	// draw messages from calculated starting position
	for i, msg := range messages {
		moveCursor(1, startLine+i)
		fmt.Println(msg)
	}

	// clear line -2
	moveCursor(1, height-2)
	clearLine()

}

// clears the current cursor line in the terminal
func clearLine() {
	fmt.Print("\033[2K") // Clear entire line
	fmt.Print("\033[0G") // Move cursor to beginning of line
}

// loads the config from a file, if it doesn't exist, creates a default one
func loadConfig() map[string]interface{} {
	const configFile = "./config.json"
	file, err := os.Open(configFile)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("Config file '%s' not found, creating one!\n", configFile)
			// if doesnt exist, create default config file
			defaultConfig := map[string]interface{}{
				"server":   "localhost",
				"port":     9076.0, // make sure its float64
				"username": "user",
				"color":    "blue", // has to be an ansi color, otherwise server rejects + goes to default (blue)
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
	fmt.Println("Config loaded successfully:")
	return config
}

func clearMessages() {
	// clear the messages slice
	messages = []string{}
	redrawMessages()
}

func sendPing(conn net.Conn) {
	pingMsg := map[string]string{
		"type": "ping",
		"user": config["username"].(string),
	}
	jsonPing, err := json.Marshal(pingMsg)
	if err != nil {
		fmt.Println("Error marshaling ping message:", err)
		return
	}
	_, err = conn.Write(jsonPing)
	if err != nil {
		fmt.Println("Error sending ping message:", err)
		return
	} else {
		lastPingTimestamp = time.Now() // update last ping timestamp
	}
}

// sets process name for the terminal window
func SetProcessName(name string) error {
	argv0str := (*reflect.StringHeader)(unsafe.Pointer(&os.Args[0]))
	argv0 := (*[1 << 30]byte)(unsafe.Pointer(argv0str.Data))[:argv0str.Len]

	n := copy(argv0, name)
	if n < len(argv0) {
		argv0[n] = 0
	}

	return nil
}

func addMute(user string) {
	if user == "" {
		fmt.Println("Cannot mute an empty user.")
		return
	}
	if _, exists := muteList[user]; exists {
		fmt.Printf("User %s is already muted.\n", user)
		return
	}
	muteList[user] = true
	addServerMessage(fmt.Sprintf("You have muted %s.", user), "bold_yellow")
	redrawMessages()
}

func removeMute(user string) {
	if user == "" {
		fmt.Println("Cannot unmute an empty user.")
		return
	}
	if _, exists := muteList[user]; !exists {
		fmt.Printf("User %s is not muted.\n", user)
		return
	}
	delete(muteList, user)
	addServerMessage(fmt.Sprintf("You have unmuted %s.", user), "bold_yellow")
	redrawMessages()
}

// formats the address for handling IPv6 addresses correctly
func formatAddress(addr string, port int) string {
	if strings.Contains(addr, ":") && !strings.HasPrefix(addr, "[") {
		return fmt.Sprintf("[%s]:%d", addr, port)
	}
	return fmt.Sprintf("%s:%d", addr, port)
}

// main process
func main() {
	// set window title
	SetProcessName("tchat")

	// load up config
	config = loadConfig()
	fmt.Println("Logged in as", config["username"])

	// connect to the TCP chat server
	conn, err := net.Dial("tcp", formatAddress(fmt.Sprintf("%v", config["server"]), int(config["port"].(float64))))
	if err != nil {
		fmt.Println("Error connecting to server:", err)
		os.Exit(1)
	}
	defer conn.Close()

	// setup goroutine to handle incoming data
	go func() {
		buffer := make([]byte, 1024)
		for {
			n, err := conn.Read(buffer)
			if err != nil {
				if err == io.EOF {
					fmt.Println("Server disconnected.")
					os.Exit(1)
				} else {
					fmt.Println("Error reading from server:", err)
					os.Exit(1)
				}
				return
			}
			if n == 0 {
				fmt.Println("Server disconnected")
				os.Exit(1)
			}

			// parse the incoming message
			var jsonMsg map[string]string
			if err := json.Unmarshal(buffer[:n], &jsonMsg); err != nil {
				fmt.Println("Error parsing JSON:", err)
				return
			}

			switch jsonMsg["type"] {
			case "message":
				// check if user or server
				if jsonMsg["user"] == "server" {
					// no idea why this works tbh
					if jsonMsg["message"] == "Username already in use" {
						os.Stdout.Sync() // flush stdout
						os.Exit(1)
						break
					}
					addServerMessage(jsonMsg["message"])
				} else {
					// check if user is muted first
					if muteList[jsonMsg["user"]] {
						continue
					} else {
						// add user message
						addMessage(jsonMsg["user"], jsonMsg["message"], jsonMsg["color"])
					}
				}

				redrawMessages()

				// restore cursor to input line
				_, currentHeight := getTerminalSize()
				moveCursor(1, currentHeight-1)
				fmt.Print("Message: ")
			case "pong":
				// handle ping response
				if lastPingTimestamp.IsZero() {
					continue
				} else {
					pingDifference := time.Since(lastPingTimestamp)
					addServerMessage(fmt.Sprint("Pong! Latency: ", pingDifference.Milliseconds(), "ms"), "bold_green")
					lastPingTimestamp = time.Time{} // unset after pong
					redrawMessages()

					// restore cursor to input line
					_, currentHeight := getTerminalSize()
					moveCursor(1, currentHeight-1)
					fmt.Print("Message: ")
				}
			case "handshake":
				// handle handshake
				serverName = jsonMsg["serverName"]
				// parse messageCharLimit from string to int
				if val, ok := jsonMsg["messageCharLimit"]; ok {
					var parsedLimit int
					_, err := fmt.Sscanf(val, "%d", &parsedLimit)
					if err == nil {
						messageCharLimit = parsedLimit

					}
				}

				handshakeResp := map[string]string{
					"type":    "handshake",
					"user":    config["username"].(string),
					"message": "OK",
				}
				jsonResp, err := json.Marshal(handshakeResp)
				if err != nil {
					fmt.Println("Error marshaling handshake response:", err)
					return
				}
				_, err = conn.Write(jsonResp)
				if err != nil {
					fmt.Println("Error sending handshake response:", err)
					return
				}
			default:
				fmt.Println("Received unknown message type:", jsonMsg["type"])
			}
		}
	}()

	// screen init
	clearScreen()
	_, height := getTerminalSize()
	fmt.Printf("\033[1;34m--- %s on %s:%d as %s ---\033[0m\n",
		"tchat",
		config["server"],
		int(config["port"].(float64)),
		config["username"])
	initChatArea()

	// create a scanner that reads from stdin
	scanner := bufio.NewScanner(os.Stdin)

	for {
		// clean the message line first and set cursor position
		moveCursor(1, height-1)
		clearLine()

		fmt.Print("Message: ")

		if scanner.Scan() {
			message := scanner.Text()

			// check for empty message
			if message == "" {
				continue
			}

			// char limit check
			if len(message) > messageCharLimit {
				message = message[:messageCharLimit] // truncate message if too long
				addServerMessage(fmt.Sprintf("Message too long, truncated to %d characters.", messageCharLimit), "bold_red")
				redrawMessages()
			}

			// ratelimit check
			if !canSendMessage() {
				addServerMessage("You are sending messages too fast, please wait a bit.", "bold_red")
				redrawMessages()
				continue
			}

			// command handling
			if strings.HasPrefix(message, "//") {
				// split command and arguments
				cmdLine := strings.TrimSpace(message[2:])
				parts := strings.Fields(cmdLine)
				cmd := parts[0]
				args := parts[1:]

				switch cmd {
				case "clear":
					clearMessages()
					addServerMessage("Chat cleared.")
				case "color":
					if len(args) < 1 {
						addServerMessage("Usage: //color <color>", "bold_red")
						redrawMessages()
						continue
					}
					newColor := validateColorName(args[0])
					if newColor != config["color"] {
						config["color"] = newColor
						addServerMessage(fmt.Sprintf("Color changed to %s.", newColor), "bold_green")
					} else {
						addServerMessage(fmt.Sprintf("Color is already set to %s.", newColor), "bold_yellow")
					}
					redrawMessages()
				case "ping":
					sendPing(conn)
				case "mute":
					if len(args) < 1 {
						addServerMessage("Usage: //mute <username>", "bold_red")
						redrawMessages()
						continue
					}
					userToMute := args[0]
					if userToMute == config["username"].(string) {
						addServerMessage("You cannot mute yourself.", "bold_red")
						redrawMessages()
						continue
					}
					if muteList[userToMute] {
						addServerMessage(fmt.Sprintf("User %s is already muted.", userToMute), "bold_yellow")
					} else {
						addMute(userToMute)
					}
				case "unmute":
					if len(args) < 1 {
						addServerMessage("Usage: //unmute <username>", "bold_red")
						redrawMessages()
						continue
					}
					userToUnmute := args[0]
					if userToUnmute == config["username"].(string) {
						addServerMessage("You cannot unmute yourself.", "bold_red")
						redrawMessages()
						continue
					}
					if _, exists := muteList[userToUnmute]; !exists {
						addServerMessage(fmt.Sprintf("User %s is not muted.", userToUnmute), "bold_yellow")
					} else {
						removeMute(userToUnmute)
					}
				case "mutelist":
					if len(muteList) == 0 {
						addServerMessage("You have no muted users.", "bold_yellow")
					} else {
						muteListMsg := "Muted users: "
						for user := range muteList {
							muteListMsg += user + ", "
						}
						muteListMsg = strings.TrimSuffix(muteListMsg, ", ")
						addServerMessage(muteListMsg, "bold_yellow")
						redrawMessages()
					}
				case "exit", "quit", "bye":
					fmt.Println("Exiting chat...")
					os.Exit(0)
				default:
					addServerMessage(fmt.Sprintf("Unknown command: %s", message[2:]), "bold_red")
					redrawMessages()
					continue
				}
			} else {
				sendMessage(conn, config["username"].(string), message, validateColorName(config["color"].(string)))
				redrawMessages()
			}

		}
	}
}
