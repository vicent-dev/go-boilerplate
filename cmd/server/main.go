package main

import "go-boilerplate/app"

func main() {
	s := app.NewServer()

	if err := s.Run(); err != nil {
		panic(err)
	}
}
