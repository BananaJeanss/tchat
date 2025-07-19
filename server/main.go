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
	"unsafe"
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

		// expect messages in json format
		var jsonMsg map[string]string
		if err := json.Unmarshal(buffer[:n], &jsonMsg); err != nil {
			fmt.Println("Error parsing JSON:", err)
			return
		} else {
			fmt.Printf("Received message from %s: %s\n", jsonMsg["user"], jsonMsg["message"])
		}

		broadcastMessage(jsonMsg)
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

func main() {
	// set window title
	SetProcessName("tchat server")

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
