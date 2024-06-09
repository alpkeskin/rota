package main

import (
	"github.com/alpkeskin/rota/internal/config"
	"github.com/alpkeskin/rota/internal/server"
	"github.com/alpkeskin/rota/pkg/environ"
)

func init() {
	environ.Init()
}

func main() {
	server := server.New()
	if config.Ac.Check {
		server.Check()
		return
	}

	err := server.Start()
	if err != nil {
		config.Ac.Log.Fatal().Msg(err.Error())
	}
}
