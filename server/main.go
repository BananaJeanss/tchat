package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"sync"
)

var clients sync.Map // store connected clients

func handleClient(conn net.Conn) {
	defer conn.Close()
	fmt.Println("Client connected:", conn.RemoteAddr())
	// Add the client to the map
	clients.Store(conn, true)

	// handle client communication
	buffer := make([]byte, 1024)
	for {
		n, err := conn.Read(buffer)
		if err == io.EOF { // Client disconnected
			fmt.Println("Client disconnected:", conn.RemoteAddr())
			clients.Delete(conn) // remove client from map
			break
		} else if err != nil {
			fmt.Println("Error reading from client:", err)
			return
		} 
		if n == 0 {
			fmt.Println("Client disconnected?:", conn.RemoteAddr())
			clients.Delete(conn) // remove client from map
			break // Client disconnected
		}
		fmt.Println("Received message:", string(buffer[:n]))

		broadcastMessage(string(buffer[:n]))
	}
}

func broadcastMessage(message string) {
	clients.Range(func(key, value interface{}) bool {
		conn := key.(net.Conn)
		_, err := conn.Write([]byte(message))
		if err != nil {
			log.Println("Error sending message to client:", err)
			return false // stop iteration if error occurs
		}
		return true // continue iterating
	})
}

func main() {
    // start TCP server
    listener, err := net.Listen("tcp", ":8080")
    if err != nil {
        log.Fatal("Error starting server:", err)
    }
    defer listener.Close()
    
    fmt.Println("Chat server started on :8080")
    
    // Accept and handle client connections
    for {
        conn, err := listener.Accept()
        if err != nil {
            fmt.Println("Error accepting connection:", err)
            continue
        }
        
        // handle each client in a goroutine
        go handleClient(conn)
    }
}
