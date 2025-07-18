package main

import (
	"fmt"
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

func addMessage(msg string) {
	messages = append(messages, msg)

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

func main() {
	clearScreen()

	_, height := getTerminalSize()

	fmt.Println("tchat initialized")

	initChatArea()

	var message string

	for {
		// clean the message line first and set cursor position
		moveCursor(1, height-1)
		clearLine()

		fmt.Print("Message: ")
		fmt.Scanln(&message)
		addMessage(message)
		redrawMessages()
	}

}
