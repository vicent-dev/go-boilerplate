package main

import "go-boilerplate/app"

func main() {
	s := app.NewServer()

	s.Consume("ping")
}
