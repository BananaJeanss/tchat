package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"syscall"

	"golang.org/x/term"
)

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

func initChatArea() {
	_, height := getTerminalSize()
	maxMessages = height - 4 // reserve space for header and input
}

// sends messages to the server
func sendMessage(conn net.Conn, user string, msg string) {
	message := fmt.Sprintf("%s: %s", user, msg)
	_, err := conn.Write([]byte(message + "\n"))
	if err != nil {
		fmt.Println("Error sending message:", err)
	} else { // if successful, add locally
		addMessage(user, msg)
	}
}

func addMessage(user string, msg string) {
	messages = append(messages, fmt.Sprintf("%s: %s", user, msg))

	// keep only the messages that fit on screen
	if len(messages) > maxMessages {
		messages = messages[1:] // remove oldest message
	}
}

func redrawMessages() {
	_, height := getTerminalSize()

	// Clear the message area (not the whole screen)
	for i := 2; i < height-2; i++ {
		moveCursor(1, i)
		fmt.Print("\033[K") // Clear this line
	}

	// calculate starting line for messages
	startLine := height - 2 - len(messages)
	if startLine < 2 {
		startLine = 2
	}

	// draw messages from calculated starting position
	for i, msg := range messages {
		moveCursor(1, startLine+i)
		fmt.Printf("\033[0;32m%s\033[0m", msg)
	}
}

// clears the current cursor line in the terminal
func clearLine() {
	fmt.Print("\033[2K") // Clear entire line
	fmt.Print("\033[0G") // Move cursor to beginning of line
}

func loadConfig() map[string]interface{} {
	const configFile = "config.json"
	file, err := os.Open(configFile)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("Config file '%s' not found, creating one!\n", configFile)
			// if doesnt exist, create default config file
			defaultConfig := map[string]interface{}{
				"server":   "localhost",
				"port":     8080.0, // make sure its float64
				"username": "user",
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

func main() {
	// load up config
	var config map[string]interface{} = loadConfig()
	fmt.Println("Logged in as", config["username"])

	// connect to the telnet server
	conn, err := net.Dial("tcp", fmt.Sprintf("%s:%d", config["server"], int(config["port"].(float64))))
	if err != nil {
		fmt.Println("Error connecting to server:", err)
		os.Exit(1)
	}
	defer conn.Close()

	// screen init
	clearScreen()
	_, height := getTerminalSize()
	fmt.Printf("--- tchat on %s:%d as %s ---\n",
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
			sendMessage(conn, config["username"].(string), message)
			redrawMessages()
		}
	}
}
