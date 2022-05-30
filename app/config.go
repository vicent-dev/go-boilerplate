package app

import (
	"go-boilerplate/static"
	"log"

	"gopkg.in/yaml.v2"
)

type config struct {
	Server struct {
		Host string `yaml:"host"`
		Port string `yaml:"port"`
	} `yaml:"server"`
	Mysql struct {
		User string `yaml:"user"`
		Pwd  string `yaml:"pwd"`
		Port string `yaml:"port"`
		Host string `yaml:"host"`
		Name string `yaml:"name"`
	} `yaml:"mysql"`
}

func loadConfig() *config {
	c := &config{}

	cFile := static.GetConfigFile()
	err := yaml.Unmarshal(cFile, c)

	if err != nil {
		log.Fatalln(err)
	}

	return c
}
