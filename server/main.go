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

func handleClient(conn net.Conn, handshakeDone chan struct{}) {
	defer conn.Close()
	fmt.Println("Client connected:", conn.RemoteAddr())
	clients.Store(conn, true)

	buffer := make([]byte, 1024)
	for {
		n, err := conn.Read(buffer)
		if err == io.EOF {
			fmt.Println("Client disconnected:", conn.RemoteAddr())
			clients.Delete(conn)
			break
		} else if err != nil {
			fmt.Println("Error reading from client:", err)
			return
		}
		if n == 0 {
			fmt.Println("Client disconnected?:", conn.RemoteAddr())
			clients.Delete(conn)
			break
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
			// Signal handshake completion
			select {
			case handshakeDone <- struct{}{}:
			default:
			}
			// Clear the read deadline after handshake
			conn.SetReadDeadline(time.Time{})
			fmt.Println("Handshake received from client:", jsonMsg["user"])
			continue
		}

		if jsonMsg["type"] == "message" {
			fmt.Printf("Received message from %s: %s\n", jsonMsg["user"], jsonMsg["message"])
			broadcastMessage(jsonMsg)
		} else {
			fmt.Println("Received non-message type:", jsonMsg["type"])
		}
	}
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

func receiveHandshake(conn net.Conn) error {
	// read the handshake message
	buffer := make([]byte, 1024)
	n, err := conn.Read(buffer)
	if err != nil {
		return fmt.Errorf("error reading handshake message: %w", err)
	}

	if n == 0 {
		return fmt.Errorf("no data received during handshake")
	}

	var jsonMsg map[string]string
	if err := json.Unmarshal(buffer[:n], &jsonMsg); err != nil {
		return fmt.Errorf("error parsing handshake JSON: %w", err)
	}

	if jsonMsg["message"] != "HandshakeStart" {
		return fmt.Errorf("invalid handshake message: %s", jsonMsg["message"])
	}

	fmt.Println("Handshake successful with user:", jsonMsg["user"])
	return nil
}

func main() {
	SetProcessName("tchat server")

	listener, err := net.Listen("tcp", ":8080")
	if err != nil {
		log.Fatal("Error starting server:", err)
	}
	defer listener.Close()

	fmt.Println("Chat server started on :8080")

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
