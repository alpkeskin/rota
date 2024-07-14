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
	srv := server.New()
	if vars.Ac.Check {
		srv.Check()
		return
	}

	err := srv.Start()
	if err != nil {
		vars.Ac.Log.Fatal().Msgf("Failed to start server: %v", err)
	}
}
