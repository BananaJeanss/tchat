package main

import "fmt"

func askifAgree() string {
	var response string
	fmt.Print("Do you agree? (yes/no): ")
	fmt.Scanln(&response)
	return response
}

func main() {
	fmt.Println("Hello, World!")
	fmt.Println("ion know go")

	var isAgree string = askifAgree()
    var hasAnswered bool

    for !hasAnswered {
        switch isAgree {
	case "yes":
		fmt.Println("sosa")
        hasAnswered = true
	case "no":
		fmt.Println("bang bang")
        hasAnswered = true
	default:
		fmt.Println("i dont know what you said")
        isAgree = askifAgree()
	}
    }

	fmt.Println("You said", isAgree, "wow")
}
