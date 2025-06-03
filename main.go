// main.go
package main

import "ticket-system/cmd"

// Import the main PocketBase app
// Alias 'm'
// Alias for clarity, or just 'models'
// For schema definitions
// For types.Pointer
// For daos.Dao if needed, but app.Dao() is common

func main() {
	if err := cmd.Start(); err != nil {
		panic(err)
	}
}
