package main

import (
	"fmt"
	"io"
	"log"
	"net"
)

func handleClient(conn net.Conn) {
	defer conn.Close()
	fmt.Println("Client connected:", conn.RemoteAddr())

	// handle client communication
	buffer := make([]byte, 1024)
	for {
		n, err := conn.Read(buffer)
		if err == io.EOF { // Client disconnected
			fmt.Println("Client disconnected:", conn.RemoteAddr())
			break
		} else if err != nil {
			fmt.Println("Error reading from client:", err)
			return
		} 
		if n == 0 {
			break // Client disconnected
		}
		fmt.Println("Received message:", string(buffer[:n]))
		conn.Write([]byte("Message received\n"))
	}
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
