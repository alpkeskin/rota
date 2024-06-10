package main

import (
	"github.com/alpkeskin/rota/internal/server"
	"github.com/alpkeskin/rota/internal/vars"
	"github.com/alpkeskin/rota/pkg/environ"
)

func init() {
	environ.Init()
}

func main() {
	server := server.New()
	if vars.Ac.Check {
		server.Check()
		return
	}

	err := server.Start()
	if err != nil {
		vars.Ac.Log.Fatal().Msg(err.Error())
	}
}
